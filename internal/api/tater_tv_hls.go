package api

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

const (
	taterTVHLSSegmentSeconds = 4
	taterTVHLSRunWindow      = 12 * time.Hour
	taterTVHLSPlaylistLimit  = 90
	taterTVHLSFirstWait      = 20 * time.Second
	taterTVHLSIdleTimeout    = 5 * time.Minute
)

type taterTVHLSManager struct {
	mu       sync.Mutex
	sessions map[string]*taterTVHLSSession
}

type taterTVHLSSession struct {
	key              string
	publicID         string
	number           string
	profileID        string
	requestedAccel   string
	ffmpegPath       string
	cfg              *config.Config
	transcodeCfg     config.TranscodingConfig
	profile          transcodeProfile
	accel            string
	channel          taterTVChannel
	guideStartedAt   time.Time
	sessionStartedAt time.Time
	root             string
	cancel           context.CancelFunc

	mu       sync.Mutex
	segments []taterTVHLSSegment
	seen     map[string]bool
	done     bool
	err      error
	accessed time.Time
}

type taterTVHLSSegment struct {
	Sequence      int64
	Duration      float64
	Path          string
	Discontinuity bool
	Title         string
	Kind          string
}

type taterTVParsedHLSSegment struct {
	Duration float64
	File     string
}

var globalTaterTVHLS = &taterTVHLSManager{sessions: map[string]*taterTVHLSSession{}}

func taterTVResetHLS() {
	globalTaterTVHLS.mu.Lock()
	defer globalTaterTVHLS.mu.Unlock()
	for _, session := range globalTaterTVHLS.sessions {
		session.stop()
	}
	globalTaterTVHLS.sessions = map[string]*taterTVHLSSession{}
}

func (h *TaterTVStreamHandler) serveHLSPlaylist(w http.ResponseWriter, r *http.Request) {
	session, playerToken, ok := h.prepareHLSSession(w, r)
	if !ok {
		return
	}
	deadline := time.Now().Add(taterTVHLSFirstWait)
	for time.Now().Before(deadline) {
		if session.segmentCount() > 0 || session.finished() {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	playlist := session.playlist(playerToken)
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(playlist))
}

func (h *TaterTVStreamHandler) serveHLSSegment(w http.ResponseWriter, r *http.Request) {
	cfg, _, ok := h.authorizeTaterTVRequest(w, r)
	if !ok {
		return
	}
	number := taterTVChannelNumberFromPath(r.URL.Path)
	sessionID, relPath, ok := taterTVHLSSegmentPath(r.URL.Path)
	if !ok || number == "" {
		http.Error(w, "Segment not found", http.StatusNotFound)
		return
	}
	profileID, _ := taterTVRequestedTranscodeProfile(cfg, r)
	requestedAccel := h.requestedHLSAccel(cfg, r)
	key := taterTVHLSKey(number, sessionID, profileID, requestedAccel)
	session := globalTaterTVHLS.get(key)
	if session == nil {
		http.Error(w, "HLS session not found", http.StatusNotFound)
		return
	}
	path, err := safeLocalPath(session.root, relPath)
	if err != nil {
		http.Error(w, "Invalid segment path", http.StatusBadRequest)
		return
	}
	if stat, err := os.Stat(path); err != nil || stat.IsDir() {
		http.Error(w, "Segment not found", http.StatusNotFound)
		return
	}
	session.touch()
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, path)
}

func (h *TaterTVStreamHandler) prepareHLSSession(w http.ResponseWriter, r *http.Request) (*taterTVHLSSession, string, bool) {
	cfg, playerToken, ok := h.authorizeTaterTVRequest(w, r)
	if !ok {
		return nil, "", false
	}
	number := taterTVChannelNumberFromPath(r.URL.Path)
	if number == "" {
		http.Error(w, "Channel number required", http.StatusBadRequest)
		return nil, "", false
	}
	if !taterTubeTVEnabled(cfg) {
		http.Error(w, "Tube TV is not enabled", http.StatusServiceUnavailable)
		return nil, "", false
	}
	baseURL := taterHTTPRequestBaseURL(r)
	guide, err := taterTVEnsureGuide(cfg, baseURL, time.Now())
	if err != nil {
		http.Error(w, "Failed to build TV lineup", http.StatusServiceUnavailable)
		return nil, "", false
	}
	channel, ok := taterTVFindChannel(guide.Channels, number)
	if !ok || channel.TotalDuration <= 0 || len(channel.Schedule) == 0 {
		http.Error(w, "Channel not found", http.StatusNotFound)
		return nil, "", false
	}

	ffmpegPath := effectiveFFmpegPath(cfg.Transcoding.FFmpegPath)
	if _, err := exec.LookPath(ffmpegPath); err != nil {
		slog.ErrorContext(r.Context(), "FFmpeg not available for Tube TV HLS", "path", ffmpegPath, "error", err)
		http.Error(w, "Transcoding unavailable: ffmpeg not found", http.StatusServiceUnavailable)
		return nil, "", false
	}

	profileID, profile := taterTVRequestedTranscodeProfile(cfg, r)
	requestedAccel := h.requestedHLSAccel(cfg, r)
	transcoder := &StreamHandler{configGetter: h.configGetter, streamTracker: h.streamTracker}
	accel, selectedHardwareDevice := transcoder.selectTranscodeAcceleration(r.Context(), ffmpegPath, cfg.Transcoding, profile, requestedAccel)
	transcodeCfg := cfg.Transcoding
	if selectedHardwareDevice != "" {
		transcodeCfg.HardwareDevice = selectedHardwareDevice
	}

	publicID := taterTVHLSPublicID(r, guide.StartedAt)
	key := taterTVHLSKey(channel.Number, publicID, profileID, requestedAccel)
	session := globalTaterTVHLS.get(key)
	if session != nil {
		if session.finished() {
			globalTaterTVHLS.removeIfSame(key, session)
		} else {
			session.touch()
			return session, playerToken, true
		}
	}

	session = globalTaterTVHLS.get(key)
	if session != nil {
		session.touch()
		return session, playerToken, true
	}

	sessionRoot := filepath.Join(taterTVHLSRoot(cfg), taterTVSafeName(key, "channel"))
	ctx, cancel := context.WithCancel(context.Background())
	session = &taterTVHLSSession{
		key:              key,
		publicID:         publicID,
		number:           channel.Number,
		profileID:        profileID,
		requestedAccel:   requestedAccel,
		ffmpegPath:       ffmpegPath,
		cfg:              cfg,
		transcodeCfg:     transcodeCfg,
		profile:          profile,
		accel:            accel,
		channel:          channel,
		guideStartedAt:   guide.StartedAt,
		sessionStartedAt: time.Now(),
		root:             sessionRoot,
		cancel:           cancel,
		seen:             map[string]bool{},
		accessed:         time.Now(),
	}
	session, created := globalTaterTVHLS.addOrGet(key, session)
	if created {
		go session.run(ctx)
	}
	return session, playerToken, true
}

func (h *TaterTVStreamHandler) authorizeTaterTVRequest(w http.ResponseWriter, r *http.Request) (*config.Config, string, bool) {
	if h.configGetter == nil {
		http.Error(w, "Configuration unavailable", http.StatusServiceUnavailable)
		return nil, "", false
	}
	cfg := h.configGetter()
	if cfg == nil {
		http.Error(w, "Configuration unavailable", http.StatusServiceUnavailable)
		return nil, "", false
	}
	token := strings.TrimSpace(r.URL.Query().Get("player_token"))
	if token == "" {
		token = bearerToken(r.Header.Get("Authorization"))
	}
	if token == "" {
		token = strings.TrimSpace(r.Header.Get("X-Tater-Player-Token"))
	}
	if _, ok := findTaterPlayerByToken(cfg, token); !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="Tater TV Channel"`)
		http.Error(w, "Unauthorized: valid player_token required", http.StatusUnauthorized)
		return nil, "", false
	}
	return cfg, token, true
}

func (h *TaterTVStreamHandler) requestedHLSAccel(cfg *config.Config, r *http.Request) string {
	requested := strings.TrimSpace(r.URL.Query().Get("hwaccel"))
	if requested == "" && cfg != nil {
		requested = cfg.Transcoding.HardwareAcceleration
	}
	if requested == "" {
		requested = "none"
	}
	return strings.ToLower(requested)
}

func (m *taterTVHLSManager) get(key string) *taterTVHLSSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[key]
	return session
}

func (m *taterTVHLSManager) addOrGet(key string, session *taterTVHLSSession) (*taterTVHLSSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked()
	if existing := m.sessions[key]; existing != nil {
		return existing, false
	}
	m.sessions[key] = session
	return session, true
}

func (m *taterTVHLSManager) removeIfSame(key string, session *taterTVHLSSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.sessions[key]; existing == session {
		session.stop()
		delete(m.sessions, key)
	}
}

func (m *taterTVHLSManager) pruneLocked() {
	now := time.Now()
	for key, session := range m.sessions {
		if now.Sub(session.lastAccessed()) > taterTVHLSIdleTimeout || session.finishedAndIdle(now) {
			session.stop()
			delete(m.sessions, key)
		}
	}
}

func (s *taterTVHLSSession) run(ctx context.Context) {
	defer func() {
		s.mu.Lock()
		s.done = true
		s.mu.Unlock()
	}()
	_ = os.RemoveAll(s.root)
	if err := os.MkdirAll(s.root, 0755); err != nil {
		s.setError(err)
		return
	}

	items, err := taterTVResolveStreamItems(s.cfg, s.channel, s.guideStartedAt, s.sessionStartedAt, taterTVHLSRunWindow.Seconds())
	if err != nil {
		s.setError(err)
		return
	}
	if len(items) == 0 {
		s.setError(fmt.Errorf("no channel items"))
		return
	}
	logoFile := ""
	if taterTVChannelLogosEnabled(s.cfg) && s.channel.LogoPath != "" {
		if resolvedLogo, err := taterTVResolveLogoFile(ctx, s.cfg, s.channel.LogoPath); err == nil {
			logoFile = resolvedLogo
		} else {
			slog.WarnContext(ctx, "Tube TV HLS logo unavailable", "channel", s.number, "logo_path", s.channel.LogoPath, "error", err)
		}
	}

	if err := s.transcodeContinuous(ctx, items, logoFile); err != nil {
		s.setError(err)
		slog.WarnContext(ctx, "Tube TV continuous HLS failed",
			"channel", s.number,
			"items", len(items),
			"error", err)
	}
}

func (s *taterTVHLSSession) transcodeContinuous(ctx context.Context, items []taterTVStreamItem, logoFile string) error {
	streamDirRel := "live"
	streamDir := filepath.Join(s.root, streamDirRel)
	if err := os.MkdirAll(streamDir, 0755); err != nil {
		return err
	}
	concatPath := filepath.Join(s.root, "channel.ffconcat")
	if err := writeTaterTVConcatList(concatPath, items); err != nil {
		return err
	}
	playlistPath := filepath.Join(streamDir, "index.m3u8")
	segmentPattern := filepath.Join(streamDir, "seg-%05d.ts")
	args := buildTaterTVContinuousHLSArgs(s.transcodeCfg, s.profile, s.accel, concatPath, logoFile, s.channel.LogoPosition, playlistPath, segmentPattern)
	var stderr limitedBuffer
	cmd := exec.CommandContext(ctx, s.ffmpegPath, args...)
	cmd.Stderr = &stderr

	slog.InfoContext(ctx, "Starting Tube TV continuous HLS transcode",
		"channel", s.number,
		"items", len(items),
		"profile", s.profileID,
		"hardware_acceleration", s.accel)

	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			s.appendContinuousSegments(streamDirRel, playlistPath)
			if err != nil {
				return fmt.Errorf("%w: %s", err, stderr.String())
			}
			return nil
		case <-ticker.C:
			s.appendContinuousSegments(streamDirRel, playlistPath)
			if time.Since(s.lastAccessed()) > taterTVHLSIdleTimeout {
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				<-done
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func writeTaterTVConcatList(path string, items []taterTVStreamItem) error {
	var builder strings.Builder
	builder.WriteString("ffconcat version 1.0\n")
	for _, item := range items {
		if strings.TrimSpace(item.Path) == "" {
			continue
		}
		builder.WriteString("file ")
		builder.WriteString(ffconcatQuote(item.Path))
		builder.WriteByte('\n')
		if item.StartSeconds > 0 {
			builder.WriteString("inpoint ")
			builder.WriteString(strconv.FormatFloat(item.StartSeconds, 'f', 3, 64))
			builder.WriteByte('\n')
		}
		if item.DurationSeconds > 0 {
			builder.WriteString("outpoint ")
			builder.WriteString(strconv.FormatFloat(item.StartSeconds+item.DurationSeconds, 'f', 3, 64))
			builder.WriteByte('\n')
			builder.WriteString("duration ")
			builder.WriteString(strconv.FormatFloat(item.DurationSeconds, 'f', 3, 64))
			builder.WriteByte('\n')
		}
	}
	return os.WriteFile(path, []byte(builder.String()), 0644)
}

func ffconcatQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}

func (s *taterTVHLSSession) appendItemSegments(itemDirRel, playlistPath string, item taterTVStreamItem) {
	segments, err := parseTaterTVHLSPlaylist(playlistPath)
	if err != nil {
		return
	}
	itemAlreadyAppended := false
	s.mu.Lock()
	for relPath := range s.seen {
		if strings.HasPrefix(relPath, itemDirRel+"/") {
			itemAlreadyAppended = true
			break
		}
	}
	s.mu.Unlock()
	for _, segment := range segments {
		relPath := filepath.ToSlash(filepath.Join(itemDirRel, segment.File))
		absPath := filepath.Join(s.root, filepath.FromSlash(relPath))
		if stat, err := os.Stat(absPath); err != nil || stat.IsDir() || stat.Size() == 0 {
			continue
		}
		s.mu.Lock()
		if s.seen[relPath] {
			s.mu.Unlock()
			continue
		}
		sequence := int64(len(s.segments))
		s.seen[relPath] = true
		s.segments = append(s.segments, taterTVHLSSegment{
			Sequence:      sequence,
			Duration:      segment.Duration,
			Path:          relPath,
			Discontinuity: !itemAlreadyAppended && sequence > 0,
			Title:         item.Title,
			Kind:          item.Kind,
		})
		s.mu.Unlock()
		itemAlreadyAppended = true
	}
}

func (s *taterTVHLSSession) appendContinuousSegments(streamDirRel, playlistPath string) {
	segments, err := parseTaterTVHLSPlaylist(playlistPath)
	if err != nil {
		return
	}
	for _, segment := range segments {
		relPath := filepath.ToSlash(filepath.Join(streamDirRel, segment.File))
		absPath := filepath.Join(s.root, filepath.FromSlash(relPath))
		if stat, err := os.Stat(absPath); err != nil || stat.IsDir() || stat.Size() == 0 {
			continue
		}
		s.mu.Lock()
		if s.seen[relPath] {
			s.mu.Unlock()
			continue
		}
		sequence := int64(len(s.segments))
		s.seen[relPath] = true
		s.segments = append(s.segments, taterTVHLSSegment{
			Sequence: sequence,
			Duration: segment.Duration,
			Path:     relPath,
		})
		s.mu.Unlock()
	}
}

func (s *taterTVHLSSession) segmentURI(relPath, playerToken string) string {
	u := "/api/tater/tv/channel/" + url.PathEscape(s.number) + "/hls/" + url.PathEscape(s.publicID)
	for _, part := range strings.Split(filepath.ToSlash(relPath), "/") {
		u += "/" + url.PathEscape(part)
	}
	q := url.Values{}
	q.Set("player_token", playerToken)
	q.Set("profile", s.profileID)
	q.Set("hwaccel", s.requestedAccel)
	return u + "?" + q.Encode()
}

func (s *taterTVHLSSession) playlist(playerToken string) string {
	s.touch()
	s.mu.Lock()
	defer s.mu.Unlock()

	start := 0
	if len(s.segments) > taterTVHLSPlaylistLimit {
		start = len(s.segments) - taterTVHLSPlaylistLimit
	}
	segments := append([]taterTVHLSSegment(nil), s.segments[start:]...)
	target := taterTVHLSSegmentSeconds
	for _, segment := range segments {
		if int(math.Ceil(segment.Duration)) > target {
			target = int(math.Ceil(segment.Duration))
		}
	}
	sequence := int64(0)
	if len(segments) > 0 {
		sequence = segments[0].Sequence
	}
	var builder strings.Builder
	builder.WriteString("#EXTM3U\n")
	builder.WriteString("#EXT-X-VERSION:3\n")
	builder.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	builder.WriteString("#EXT-X-TARGETDURATION:" + strconv.Itoa(target) + "\n")
	builder.WriteString("#EXT-X-MEDIA-SEQUENCE:" + strconv.FormatInt(sequence, 10) + "\n")
	for _, segment := range segments {
		if segment.Discontinuity {
			builder.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		builder.WriteString("#EXTINF:" + strconv.FormatFloat(segment.Duration, 'f', 3, 64) + ",\n")
		builder.WriteString(s.segmentURI(segment.Path, playerToken) + "\n")
	}
	if s.done {
		builder.WriteString("#EXT-X-ENDLIST\n")
	}
	if len(segments) == 0 && s.err != nil {
		builder.WriteString("#EXT-X-ENDLIST\n")
	}
	return builder.String()
}

func (s *taterTVHLSSession) segmentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.segments)
}

func (s *taterTVHLSSession) finished() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

func (s *taterTVHLSSession) finishedAndIdle(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done && now.Sub(s.accessed) > time.Hour
}

func (s *taterTVHLSSession) lastAccessed() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accessed
}

func (s *taterTVHLSSession) touch() {
	s.mu.Lock()
	s.accessed = time.Now()
	s.mu.Unlock()
}

func (s *taterTVHLSSession) stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *taterTVHLSSession) setError(err error) {
	s.mu.Lock()
	s.err = err
	s.done = true
	s.mu.Unlock()
}

func parseTaterTVHLSPlaylist(path string) ([]taterTVParsedHLSSegment, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	out := []taterTVParsedHLSSegment{}
	duration := 0.0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#EXTINF:") {
			raw := strings.TrimPrefix(line, "#EXTINF:")
			raw = strings.TrimSuffix(raw, ",")
			duration, _ = strconv.ParseFloat(raw, 64)
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasSuffix(line, ".tmp") {
			continue
		}
		out = append(out, taterTVParsedHLSSegment{Duration: duration, File: filepath.Base(line)})
		duration = 0
	}
	return out, scanner.Err()
}

func buildTaterTVChannelHLSArgs(cfg config.TranscodingConfig, profile transcodeProfile, accel string, inputPath string, startSeconds, durationSeconds float64, logoFile, logoPosition, outputPlaylist, segmentPattern string) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-nostdin",
	}
	args = append(args, transcodeHardwareInitArgs(cfg, accel)...)
	args = append(args, "-re")
	if startSeconds > 0 {
		args = append(args, "-ss", strconv.FormatFloat(startSeconds, 'f', 3, 64))
	}
	args = append(args, "-i", inputPath)
	logoFile = strings.TrimSpace(logoFile)
	if logoFile != "" {
		args = append(args, "-loop", "1", "-framerate", "30", "-i", logoFile)
	}
	if durationSeconds > 0 {
		args = append(args, "-t", strconv.FormatFloat(durationSeconds, 'f', 3, 64))
	}
	videoCodec, filters := transcodeVideoSettings(accel, cfg.HardwareDevice, profile)
	if logoFile != "" {
		args = append(args,
			"-filter_complex", taterTVChannelLogoFilter(filters, profile, logoPosition),
			"-map", "[vout]",
			"-map", "0:a:0?",
			"-sn",
		)
	} else {
		args = append(args,
			"-map", "0:v:0",
			"-map", "0:a:0?",
			"-sn",
		)
		if filters != "" {
			args = append(args, "-vf", filters)
		}
	}
	args = append(args,
		"-af", "aresample=async=1:first_pts=0",
		"-c:v", videoCodec,
		"-b:v", profile.VideoBitrate,
		"-maxrate", profile.MaxRate,
		"-bufsize", profile.BufferSize,
	)
	args = appendVideoEncoderOptions(args, videoCodec, profile)
	args = append(args,
		"-c:a", "aac",
		"-b:a", profile.AudioBitrate,
		"-ac", "2",
		"-ar", "48000",
		"-fflags", "+genpts",
		"-avoid_negative_ts", "make_zero",
		"-force_key_frames", "expr:gte(t,n_forced*"+strconv.Itoa(taterTVHLSSegmentSeconds)+")",
		"-f", "hls",
		"-hls_time", strconv.Itoa(taterTVHLSSegmentSeconds),
		"-hls_segment_type", "mpegts",
		"-hls_flags", "independent_segments+temp_file",
		"-hls_list_size", "0",
		"-hls_segment_filename", segmentPattern,
		outputPlaylist,
	)
	return args
}

func buildTaterTVContinuousHLSArgs(cfg config.TranscodingConfig, profile transcodeProfile, accel string, concatPath string, logoFile, logoPosition, outputPlaylist, segmentPattern string) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-nostdin",
	}
	args = append(args, transcodeHardwareInitArgs(cfg, accel)...)
	args = append(args,
		"-re",
		"-f", "concat",
		"-safe", "0",
		"-i", concatPath,
	)
	logoFile = strings.TrimSpace(logoFile)
	if logoFile != "" {
		args = append(args, "-loop", "1", "-framerate", "30", "-i", logoFile)
	}
	videoCodec, filters := transcodeVideoSettings(accel, cfg.HardwareDevice, profile)
	if logoFile != "" {
		args = append(args,
			"-filter_complex", taterTVChannelLogoFilter(filters, profile, logoPosition),
			"-map", "[vout]",
			"-map", "0:a:0?",
			"-sn",
		)
	} else {
		args = append(args,
			"-map", "0:v:0",
			"-map", "0:a:0?",
			"-sn",
		)
		if filters != "" {
			args = append(args, "-vf", filters)
		}
	}
	args = append(args,
		"-af", "aresample=async=1:first_pts=0",
		"-c:v", videoCodec,
		"-b:v", profile.VideoBitrate,
		"-maxrate", profile.MaxRate,
		"-bufsize", profile.BufferSize,
	)
	args = appendVideoEncoderOptions(args, videoCodec, profile)
	args = append(args,
		"-c:a", "aac",
		"-b:a", profile.AudioBitrate,
		"-ac", "2",
		"-ar", "48000",
		"-fflags", "+genpts",
		"-avoid_negative_ts", "make_zero",
		"-force_key_frames", "expr:gte(t,n_forced*"+strconv.Itoa(taterTVHLSSegmentSeconds)+")",
		"-f", "hls",
		"-hls_time", strconv.Itoa(taterTVHLSSegmentSeconds),
		"-hls_segment_type", "mpegts",
		"-hls_flags", "independent_segments+temp_file",
		"-hls_list_size", "0",
		"-hls_segment_filename", segmentPattern,
		outputPlaylist,
	)
	return args
}

func taterTVHLSRoot(cfg *config.Config) string {
	root := ""
	if cfg != nil {
		root = strings.TrimSpace(cfg.Metadata.RootPath)
	}
	if root == "" {
		root = "/config/metadata"
	}
	return filepath.Join(root, "tube-tv-hls")
}

func taterTVHLSPublicID(r *http.Request, guideStartedAt time.Time) string {
	session := strings.TrimSpace(r.URL.Query().Get("tv_session"))
	if session == "" && !guideStartedAt.IsZero() {
		session = strconv.FormatInt(guideStartedAt.Unix(), 10)
	}
	if session == "" {
		session = "live"
	}
	return taterTVSafeName(session, "live")
}

func taterTVHLSKey(number, sessionID, profileID, requestedAccel string) string {
	raw := strings.Join([]string{number, sessionID, profileID, requestedAccel}, "|")
	sum := sha1.Sum([]byte(raw))
	return number + "-" + hex.EncodeToString(sum[:])[:16]
}

func taterTVHLSSegmentPath(requestPath string) (sessionID, relPath string, ok bool) {
	rest := strings.TrimPrefix(requestPath, "/api/tater/tv/channel/")
	parts := strings.Split(rest, "/")
	if len(parts) < 5 || parts[1] != "hls" {
		return "", "", false
	}
	session, err := url.PathUnescape(parts[2])
	if err != nil || session == "" {
		return "", "", false
	}
	relParts := []string{}
	for _, part := range parts[3:] {
		decoded, err := url.PathUnescape(part)
		if err != nil || decoded == "" {
			return "", "", false
		}
		relParts = append(relParts, decoded)
	}
	return taterTVSafeName(session, "live"), filepath.ToSlash(filepath.Join(relParts...)), true
}
