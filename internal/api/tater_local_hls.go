package api

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log/slog"
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
	"github.com/TaterTotterson/tater-tube-server/internal/nzbfilesystem"
)

const (
	taterLocalHLSSessionQuery = "tater_hls_session"
	taterLocalHLSSegmentQuery = "tater_hls_segment"
	taterLocalHLSSegmentTime  = 4
	taterLocalHLSFirstWait    = 15 * time.Second
	taterLocalHLSIdleTimeout  = 5 * time.Minute
)

type taterLocalHLSManager struct {
	mu       sync.Mutex
	sessions map[string]*taterLocalHLSSession
}

type taterLocalHLSSession struct {
	id           string
	playerID     string
	playerToken  string
	playlistURL  string
	root         string
	playlistPath string
	cancel       context.CancelFunc
	tracker      *StreamTracker
	stream       *nzbfilesystem.ActiveStream

	mu       sync.Mutex
	accessed time.Time
	done     bool
	err      error
	once     sync.Once
}

type taterLocalHLSCommand struct {
	args           []string
	profileID      string
	profileName    string
	effectiveAccel string
	hardwareDevice string
	videoCodec     string
	videoMode      string
	audioMode      string
	audioCodec     string
	audioChannels  int
}

var globalTaterLocalHLS = &taterLocalHLSManager{sessions: map[string]*taterLocalHLSSession{}}

func (h *LocalStreamHandler) serveLocalHLSPlaylist(
	w http.ResponseWriter,
	r *http.Request,
	cfg *config.Config,
	player *config.PlayerConfig,
	playerToken string,
	path string,
	info os.FileInfo,
) {
	session, err := h.prepareLocalHLSSession(r, cfg, player, playerToken, path, info)
	if err != nil {
		slog.ErrorContext(r.Context(), "Unable to prepare local HLS playback", "error", err)
		http.Error(w, "Unable to prepare Apple TV playback", http.StatusServiceUnavailable)
		return
	}

	deadline := time.Now().Add(taterLocalHLSFirstWait)
	for time.Now().Before(deadline) {
		if session.playlistReady() || session.finished() {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	playlist, err := session.playlist()
	if err != nil {
		if failure := session.failure(); failure != nil {
			slog.ErrorContext(r.Context(), "Local HLS playback failed", "error", failure)
		}
		http.Error(w, "Apple TV playback did not become ready", http.StatusServiceUnavailable)
		return
	}
	session.touch()
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(playlist)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(playlist)
}

func (h *LocalStreamHandler) serveLocalHLSSegment(w http.ResponseWriter, r *http.Request, player *config.PlayerConfig) {
	sessionID := strings.TrimSpace(r.URL.Query().Get(taterLocalHLSSessionQuery))
	segmentName := filepath.Base(strings.TrimSpace(r.URL.Query().Get(taterLocalHLSSegmentQuery)))
	contentType, supported := taterHLSSegmentContentType(segmentName)
	if sessionID == "" || segmentName == "." || segmentName == "" || !supported {
		http.Error(w, "HLS segment not found", http.StatusNotFound)
		return
	}
	session := globalTaterLocalHLS.get(sessionID)
	if session == nil || player == nil || session.playerID != player.ID {
		http.Error(w, "HLS session not found", http.StatusNotFound)
		return
	}
	segmentPath, err := safeLocalPath(session.root, segmentName)
	if err != nil {
		http.Error(w, "Invalid HLS segment", http.StatusBadRequest)
		return
	}
	file, err := os.Open(segmentPath)
	if err != nil {
		http.Error(w, "HLS segment not found", http.StatusNotFound)
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || stat.IsDir() {
		http.Error(w, "HLS segment not found", http.StatusNotFound)
		return
	}

	session.touch()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("Accept-Ranges", "bytes")
	var writer http.ResponseWriter = w
	if session.tracker != nil && session.stream != nil {
		writer = &trackedResponseWriter{ResponseWriter: w, stream: session.stream, streamTracker: session.tracker}
	}
	http.ServeContent(writer, r, segmentName, stat.ModTime(), file)
}

func (h *LocalStreamHandler) prepareLocalHLSSession(
	r *http.Request,
	cfg *config.Config,
	player *config.PlayerConfig,
	playerToken string,
	path string,
	info os.FileInfo,
) (*taterLocalHLSSession, error) {
	if player == nil {
		return nil, fmt.Errorf("player is unavailable")
	}
	ffmpegPath := effectiveFFmpegPath(cfg.Transcoding.FFmpegPath)
	if _, err := exec.LookPath(ffmpegPath); err != nil {
		return nil, fmt.Errorf("ffmpeg is unavailable: %w", err)
	}

	key := taterLocalHLSKey(r, player.ID, path)
	if existing := globalTaterLocalHLS.get(key); existing != nil {
		if !existing.finished() || (existing.failure() == nil && existing.playlistReady()) {
			existing.touch()
			return existing, nil
		}
		globalTaterLocalHLS.removeIfSame(key, existing)
		existing.stopAndCleanup()
	}

	root := filepath.Join(taterTVHLSRoot(cfg), fmt.Sprintf("local-%s-%d", key, time.Now().UnixNano()))
	if err := os.RemoveAll(root); err != nil {
		return nil, fmt.Errorf("clear HLS working directory: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create HLS working directory: %w", err)
	}
	playlistPath := filepath.Join(root, "index.m3u8")
	segmentPattern := filepath.Join(root, "segment-%06d.ts")
	command, err := buildTaterLocalHLSCommand(r, cfg, path, playlistPath, segmentPattern)
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	session := &taterLocalHLSSession{
		id:           key,
		playerID:     player.ID,
		playerToken:  playerToken,
		playlistURL:  "/api/tater/local/stream",
		root:         root,
		playlistPath: playlistPath,
		cancel:       cancel,
		tracker:      h.streamTracker,
		accessed:     time.Now(),
	}
	session, created := globalTaterLocalHLS.addOrGet(key, session)
	if !created {
		cancel()
		_ = os.RemoveAll(root)
		session.touch()
		return session, nil
	}

	if h.streamTracker != nil {
		session.stream = h.streamTracker.AddStream(
			path, "Local", taterPlayerDisplayName(player), r.RemoteAddr, r.UserAgent(), info.Size(),
		)
		h.streamTracker.SetPlayerID(session.stream.ID, player.ID)
		h.streamTracker.SetCancelFunc(session.stream.ID, cancel)
		h.streamTracker.SetTranscodingInfo(
			session.stream.ID,
			command.profileID,
			command.profileName,
			command.effectiveAccel,
			command.hardwareDevice,
			command.videoCodec,
			command.effectiveAccel != "" && command.effectiveAccel != "none",
		)
		h.streamTracker.SetTrackProcessingInfo(
			session.stream.ID, command.videoMode, command.audioMode, command.audioCodec, "Streaming HLS",
		)
		h.streamTracker.SetAudioChannelInfo(session.stream.ID, command.audioChannels)
		applyTaterRequestedDynamicRangeInfo(h.streamTracker, session.stream.ID, r)
		applyTaterRequestedResolutionInfo(h.streamTracker, session.stream.ID, r)
		transcoder := &StreamHandler{configGetter: h.configGetter, streamTracker: h.streamTracker}
		duration := transcoder.probeMediaDuration(r.Context(), path)
		h.streamTracker.SetMediaInfo(session.stream.ID, duration, parseTranscodeStartSeconds(r.URL.Query().Get("start")))
	}

	go session.run(ctx, ffmpegPath, command.args)
	go session.expireWhenIdle(ctx)
	return session, nil
}

func buildTaterLocalHLSCommand(
	r *http.Request,
	cfg *config.Config,
	inputPath string,
	playlistPath string,
	segmentPattern string,
) (taterLocalHLSCommand, error) {
	profileID := strings.TrimSpace(r.URL.Query().Get("profile"))
	if profileID == "" {
		profileID = strings.TrimSpace(cfg.Transcoding.Profile)
	}
	profile, ok := transcodeProfiles[profileID]
	if !ok {
		profileID = "hdmi_1080p"
		profile = transcodeProfiles[profileID]
	}
	startSeconds := parseTranscodeStartSeconds(r.URL.Query().Get("start"))
	audioTrack := requestedTaterAudioTrack(r)
	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("transcode")))
	sourceVideoCodec := cleanTaterCodecName(r.URL.Query().Get("tater_video_codec"))
	sourceVideoRange := cleanTaterVideoRange(r.URL.Query().Get("tater_source_video_range"))
	outputVideoRange := cleanTaterVideoRange(r.URL.Query().Get("tater_output_video_range"))
	stripDolbyVision := strings.TrimSpace(r.URL.Query().Get("tater_strip_dolby_vision")) == "1"
	command := taterLocalHLSCommand{
		profileID:     profileID,
		profileName:   profile.Name,
		videoMode:     "direct",
		audioMode:     "direct",
		audioCodec:    cleanTaterCodecName(r.URL.Query().Get("tater_audio_codec")),
		audioChannels: requestedTaterAudioChannels(r),
	}

	var args []string
	videoEncoded := false
	switch mode {
	case "remux":
		command.profileID = "hls_remux"
		command.profileName = "HLS remux"
		command.videoCodec = "copy"
		args = buildFFmpegRemuxArgs(inputPath, startSeconds, audioTrack)
	case "audio", "audio-only", "audio_only":
		command.profileID = audioOnlyProfileID
		command.profileName = audioOnlyProfileName
		command.videoCodec = "copy"
		command.audioMode = "transcode"
		command.audioCodec = "aac"
		args = buildFFmpegAudioOnlyVideoArgsWithTrackContainerAndChannels(
			profile.AudioBitrate, inputPath, startSeconds, audioTrack, "mpegts", command.audioChannels,
		)
	case "video", "video-only", "video_only":
		command.videoMode = "transcode"
		videoEncoded = true
		fallthrough
	default:
		if mode != "video" && mode != "video-only" && mode != "video_only" && mode != "1" && mode != "full" && mode != "true" {
			return taterLocalHLSCommand{}, fmt.Errorf("unsupported HLS playback mode %q", mode)
		}
		requestedCodec := requestedTranscodeCodec(r)
		requestedAccel := strings.TrimSpace(r.URL.Query().Get("hwaccel"))
		if requestedAccel == "" {
			requestedAccel = strings.TrimSpace(cfg.Transcoding.HardwareAcceleration)
		}
		if requestedAccel == "" {
			requestedAccel = "none"
		}
		transcoder := &StreamHandler{}
		accel, selectedDevice, preferredCodec := transcoder.selectTranscodeAccelerationAndCodec(
			r.Context(), effectiveFFmpegPath(cfg.Transcoding.FFmpegPath), cfg.Transcoding, profile, requestedAccel, requestedCodec,
		)
		if requestedCodec == transcodeCodecHEVC && preferredCodec != transcodeCodecHEVC {
			if fallbackID, fallbackProfile, found := requestedFallbackTranscodeProfile(r); found {
				profileID, profile = fallbackID, fallbackProfile
				accel, selectedDevice = transcoder.selectTranscodeAcceleration(
					r.Context(), effectiveFFmpegPath(cfg.Transcoding.FFmpegPath), cfg.Transcoding, profile, requestedAccel,
				)
			}
		}
		transcodeCfg := cfg.Transcoding
		if selectedDevice != "" {
			transcodeCfg.HardwareDevice = selectedDevice
		}
		if requestedCodec == transcodeCodecHEVC && preferredCodec != transcodeCodecHEVC &&
			taterVideoRangeIsHDR(outputVideoRange) {
			// Never emit HDR metadata on an H.264 fallback. If this server cannot
			// provide the requested HEVC encoder, retain playable video by using the
			// same guarded HDR-to-SDR path as an SDR display.
			outputVideoRange = "sdr"
		}
		videoCodec, _ := transcodeVideoSettingsForCodec(accel, transcodeCfg.HardwareDevice, profile, preferredCodec)
		command.profileID = profileID
		command.profileName = profile.Name
		command.videoCodec = videoCodec
		command.effectiveAccel = effectiveTranscodeHardwareAccel(videoCodec)
		command.hardwareDevice = effectiveTranscodeHardwareDevice(command.effectiveAccel, transcodeCfg.HardwareDevice)
		videoEncoded = true
		toneMapSource, toneMapTarget := requestedTaterToneMap(r)
		if sourceVideoRange != "" && sourceVideoRange != "sdr" && outputVideoRange == "sdr" {
			toneMapSource, toneMapTarget = sourceVideoRange, "sdr"
		}
		toneMapFilter := taterToneMapFilterForFFmpeg(r.Context(), effectiveFFmpegPath(cfg.Transcoding.FFmpegPath), toneMapSource)
		if toneMapSource != "" && toneMapFilter == "" {
			return taterLocalHLSCommand{}, fmt.Errorf("tone mapping is unavailable in the configured ffmpeg build")
		}
		if mode == "video" || mode == "video-only" || mode == "video_only" {
			command.audioMode = requestedTaterTrackMode(r, "tater_audio_mode", "direct")
			if command.audioCodec == "" {
				command.audioCodec = "copy"
			}
			args = buildFFmpegVideoOnlyArgsWithToneMapFilterAudioTrackContainerAndRange(
				transcodeCfg, profile, accel, preferredCodec, inputPath, startSeconds,
				toneMapSource, toneMapTarget, toneMapFilter, audioTrack, "mpegts", outputVideoRange,
			)
		} else {
			command.videoMode = "transcode"
			command.audioMode = "transcode"
			command.audioCodec = "aac"
			args = buildFFmpegTranscodeArgsWithOptions(
				transcodeCfg, profile, accel, preferredCodec, transcodeOutputOptions{
					InputPath: inputPath, StartSeconds: startSeconds, AudioTrack: audioTrack,
					AudioChannels: command.audioChannels,
					ToneMapSource: toneMapSource, ToneMapTarget: toneMapTarget, ToneMapFilter: toneMapFilter,
					SourceVideoRange: sourceVideoRange, OutputVideoRange: outputVideoRange,
				},
			)
		}
	}
	if stripDolbyVision && (mode == "remux" || mode == "audio" || mode == "audio-only" || mode == "audio_only") {
		args = insertTaterFFmpegOutputArgs(args, "-bsf:v", "dovi_rpu=strip=1")
	}

	if command.audioMode == "transcode" {
		_, command.audioChannels = taterAACTranscodeSettings(profile.AudioBitrate, command.audioChannels)
	}
	command.args = convertTaterFFmpegArgsToHLS(
		args, playlistPath, segmentPattern, videoEncoded, command.videoCodec,
		sourceVideoCodec, outputVideoRange,
	)
	return command, nil
}

func convertTaterFFmpegArgsToHLS(
	args []string,
	playlistPath string,
	segmentPattern string,
	videoEncoded bool,
	videoCodec string,
	sourceVideoCodec string,
	outputVideoRange string,
) []string {
	if len(args) >= 3 && args[len(args)-3] == "-f" && args[len(args)-1] == "pipe:1" {
		args = append([]string(nil), args[:len(args)-3]...)
	} else {
		args = append([]string(nil), args...)
	}
	for i, arg := range args {
		if arg == "-i" {
			pace := []string{"-readrate", "1", "-readrate_initial_burst", strconv.Itoa(taterLocalHLSSegmentTime * 2)}
			paced := make([]string, 0, len(args)+len(pace))
			paced = append(paced, args[:i]...)
			paced = append(paced, pace...)
			paced = append(paced, args[i:]...)
			args = paced
			break
		}
	}
	if videoEncoded {
		args = append(args,
			"-flags:v", "+cgop",
			"-g", "120",
			"-bf", "0",
			"-force_key_frames", "expr:gte(t,n_forced*"+strconv.Itoa(taterLocalHLSSegmentTime)+")",
		)
		if videoCodec == "h264_qsv" || videoCodec == "hevc_qsv" {
			args = append(args, "-forced_idr", "1")
		}
	}
	effectiveVideoCodec := cleanTaterCodecName(videoCodec)
	if effectiveVideoCodec == "copy" || effectiveVideoCodec == "" {
		effectiveVideoCodec = cleanTaterCodecName(sourceVideoCodec)
	}
	if effectiveVideoCodec == "hevc" || strings.Contains(effectiveVideoCodec, "hevc") ||
		strings.Contains(effectiveVideoCodec, "x265") {
		segmentPattern = strings.TrimSuffix(segmentPattern, filepath.Ext(segmentPattern)) + ".m4s"
		videoTag := "hvc1"
		if cleanTaterVideoRange(outputVideoRange) == "dolby_vision" {
			videoTag = "dvh1"
		}
		args = append(args, "-tag:v", videoTag)
		return append(args,
			"-f", "hls",
			"-hls_time", strconv.Itoa(taterLocalHLSSegmentTime),
			"-hls_segment_type", "fmp4",
			"-hls_fmp4_init_filename", "init.mp4",
			"-hls_flags", "independent_segments+temp_file",
			"-hls_list_size", "0",
			"-hls_playlist_type", "event",
			"-hls_segment_filename", segmentPattern,
			playlistPath,
		)
	}
	return append(args,
		"-f", "hls",
		"-hls_time", strconv.Itoa(taterLocalHLSSegmentTime),
		"-hls_segment_type", "mpegts",
		"-hls_segment_options", "mpegts_flags=+resend_headers+initial_discontinuity",
		"-hls_flags", "independent_segments+temp_file",
		"-hls_list_size", "0",
		"-hls_playlist_type", "event",
		"-hls_segment_filename", segmentPattern,
		playlistPath,
	)
}

func insertTaterFFmpegOutputArgs(args []string, values ...string) []string {
	if len(args) >= 3 && args[len(args)-3] == "-f" && args[len(args)-1] == "pipe:1" {
		inserted := make([]string, 0, len(args)+len(values))
		inserted = append(inserted, args[:len(args)-3]...)
		inserted = append(inserted, values...)
		inserted = append(inserted, args[len(args)-3:]...)
		return inserted
	}
	return append(args, values...)
}

func taterHLSSegmentContentType(name string) (string, bool) {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(name))) {
	case ".ts":
		return "video/mp2t", true
	case ".m4s", ".mp4":
		return "video/mp4", true
	default:
		return "", false
	}
}

func taterLocalHLSKey(r *http.Request, playerID, path string) string {
	query := url.Values{}
	if r != nil && r.URL != nil {
		query = r.URL.Query()
		query.Del("player_token")
		query.Del(taterLocalHLSSessionQuery)
		query.Del(taterLocalHLSSegmentQuery)
	}
	raw := strings.Join([]string{strings.TrimSpace(playerID), strings.TrimSpace(path), query.Encode()}, "|")
	sum := sha1.Sum([]byte(raw))
	return hex.EncodeToString(sum[:])[:20]
}

func (m *taterLocalHLSManager) get(id string) *taterLocalHLSSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

func (m *taterLocalHLSManager) addOrGet(id string, session *taterLocalHLSSession) (*taterLocalHLSSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.sessions[id]; existing != nil {
		return existing, false
	}
	m.sessions[id] = session
	return session, true
}

func (m *taterLocalHLSManager) removeIfSame(id string, session *taterLocalHLSSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[id] == session {
		delete(m.sessions, id)
	}
}

func (s *taterLocalHLSSession) run(ctx context.Context, ffmpegPath string, args []string) {
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	s.mu.Lock()
	s.done = true
	if err != nil && ctx.Err() == nil {
		s.err = fmt.Errorf("ffmpeg HLS process: %w: %s", err, stderr.String())
	}
	s.mu.Unlock()
	if err != nil && ctx.Err() == nil {
		slog.Error("Local HLS FFmpeg failed", "session", s.id, "error", err, "stderr", stderr.String())
	}
}

func (s *taterLocalHLSSession) expireWhenIdle(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if time.Since(s.lastAccessed()) <= taterLocalHLSIdleTimeout {
				continue
			}
			globalTaterLocalHLS.removeIfSame(s.id, s)
			s.stopAndCleanup()
			return
		}
	}
}

func (s *taterLocalHLSSession) playlistReady() bool {
	data, err := os.ReadFile(s.playlistPath)
	if err != nil {
		return false
	}
	playlist := string(data)
	return strings.Contains(playlist, "#EXTINF:") &&
		(strings.Contains(playlist, ".ts") || strings.Contains(playlist, ".m4s"))
}

func (s *taterLocalHLSSession) playlist() ([]byte, error) {
	raw, err := os.ReadFile(s.playlistPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")
	for index, line := range lines {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "#EXT-X-MAP:") {
			lines[index] = rewriteTaterHLSMapURI(line, s.authenticatedSegmentURL)
			continue
		}
		if strings.HasPrefix(name, "#") {
			continue
		}
		lines[index] = s.authenticatedSegmentURL(name)
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func (s *taterLocalHLSSession) authenticatedSegmentURL(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == "" {
		return ""
	}
	query := url.Values{}
	query.Set("player_token", s.playerToken)
	query.Set(taterLocalHLSSessionQuery, s.id)
	query.Set(taterLocalHLSSegmentQuery, name)
	playlistURL := strings.TrimSpace(s.playlistURL)
	if playlistURL == "" {
		playlistURL = "/api/tater/local/stream"
	}
	return playlistURL + "?" + query.Encode()
}

func rewriteTaterHLSMapURI(line string, rewrite func(string) string) string {
	const marker = `URI="`
	start := strings.Index(line, marker)
	if start < 0 {
		return line
	}
	start += len(marker)
	end := strings.Index(line[start:], `"`)
	if end < 0 {
		return line
	}
	end += start
	replacement := rewrite(line[start:end])
	if replacement == "" {
		return line
	}
	return line[:start] + replacement + line[end:]
}

func (s *taterLocalHLSSession) touch() {
	s.mu.Lock()
	s.accessed = time.Now()
	s.mu.Unlock()
	if s.tracker != nil && s.stream != nil {
		s.tracker.Touch(s.stream.ID)
	}
}

func (s *taterLocalHLSSession) lastAccessed() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accessed
}

func (s *taterLocalHLSSession) finished() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

func (s *taterLocalHLSSession) failure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *taterLocalHLSSession) stopAndCleanup() {
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		if s.tracker != nil && s.stream != nil {
			s.tracker.Remove(s.stream.ID)
		}
		_ = os.RemoveAll(s.root)
	})
}
