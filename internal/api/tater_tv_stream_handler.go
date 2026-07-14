package api

import (
	"context"
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
	"sync/atomic"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/TaterTotterson/tater-tube-server/internal/nzbfilesystem"
)

// TaterTVStreamHandler serves a continuous server-side Tube TV channel stream.
type TaterTVStreamHandler struct {
	configGetter  config.ConfigGetter
	streamTracker *StreamTracker
}

const (
	taterTVConcatRunWindow = 6 * time.Hour
	taterTVMaxConcatItems  = 256
)

func NewTaterTVStreamHandler(configGetter config.ConfigGetter, streamTracker *StreamTracker) *TaterTVStreamHandler {
	return &TaterTVStreamHandler{configGetter: configGetter, streamTracker: streamTracker}
}

func (h *TaterTVStreamHandler) GetHTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.serveChannel(w, r)
	})
}

func (h *TaterTVStreamHandler) serveChannel(w http.ResponseWriter, r *http.Request) {
	if h.configGetter == nil {
		http.Error(w, "Configuration unavailable", http.StatusServiceUnavailable)
		return
	}
	cfg := h.configGetter()
	if cfg == nil {
		http.Error(w, "Configuration unavailable", http.StatusServiceUnavailable)
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("player_token"))
	if token == "" {
		token = bearerToken(r.Header.Get("Authorization"))
	}
	if token == "" {
		token = strings.TrimSpace(r.Header.Get("X-Tater-Player-Token"))
	}
	player, ok := findTaterPlayerByToken(cfg, token)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="Tater TV Channel"`)
		http.Error(w, "Unauthorized: valid player_token required", http.StatusUnauthorized)
		return
	}
	if !taterLocalMediaEnabled(cfg) {
		http.Error(w, "Local media is not configured", http.StatusServiceUnavailable)
		return
	}

	number := taterTVStreamChannelNumber(r.URL.Path)
	if number == "" {
		http.Error(w, "Channel number required", http.StatusBadRequest)
		return
	}

	baseURL := taterHTTPRequestBaseURL(r)
	guide, err := taterTVEnsureGuide(cfg, baseURL, time.Now())
	if err != nil {
		http.Error(w, "Failed to build TV lineup", http.StatusServiceUnavailable)
		return
	}
	startedAt := guide.StartedAt

	channel, ok := taterTVFindChannel(guide.Channels, number)
	if !ok || channel.TotalDuration <= 0 || len(channel.Schedule) == 0 {
		http.Error(w, "Channel not found", http.StatusNotFound)
		return
	}

	ffmpegPath := effectiveFFmpegPath(cfg.Transcoding.FFmpegPath)
	if _, err := exec.LookPath(ffmpegPath); err != nil {
		slog.ErrorContext(r.Context(), "FFmpeg not available for Tube TV channel stream", "path", ffmpegPath, "error", err)
		http.Error(w, "Transcoding unavailable: ffmpeg not found", http.StatusServiceUnavailable)
		return
	}

	profileID, profile := taterTVRequestedTranscodeProfile(cfg, r)
	requestedAccel := strings.TrimSpace(r.URL.Query().Get("hwaccel"))
	if requestedAccel == "" {
		requestedAccel = cfg.Transcoding.HardwareAcceleration
	}
	if requestedAccel == "" {
		requestedAccel = "none"
	}
	transcoder := &StreamHandler{configGetter: h.configGetter, streamTracker: h.streamTracker}
	accel, selectedHardwareDevice := transcoder.selectTranscodeAcceleration(r.Context(), ffmpegPath, cfg.Transcoding, profile, requestedAccel)
	transcodeCfg := cfg.Transcoding
	if selectedHardwareDevice != "" {
		transcodeCfg.HardwareDevice = selectedHardwareDevice
	}
	videoCodec, _ := transcodeVideoSettings(accel, transcodeCfg.HardwareDevice, profile)
	effectiveAccel := effectiveTranscodeHardwareAccel(videoCodec)
	hardwareDevice := effectiveTranscodeHardwareDevice(effectiveAccel, transcodeCfg.HardwareDevice)

	streamReq := r
	var stream *nzbfilesystem.ActiveStream
	if h.streamTracker != nil {
		streamCtx, cancel := context.WithCancel(r.Context())
		streamReq = r.WithContext(streamCtx)
		defer cancel()

		playerName := taterPlayerDisplayName(player)
		stream = h.streamTracker.AddStream("Tube TV CH "+channel.Number+" - "+channel.Title, "Tube TV", playerName, r.RemoteAddr, r.UserAgent(), 0)
		h.streamTracker.SetPlayerID(stream.ID, player.ID)
		h.streamTracker.SetCancelFunc(stream.ID, cancel)
		h.streamTracker.SetTranscodingInfo(stream.ID, profileID, profile.Name, effectiveAccel, hardwareDevice, videoCodec, effectiveAccel != "" && effectiveAccel != "none")
		defer h.streamTracker.Remove(stream.ID)
	}

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `inline; filename="TaterTube-CH`+channel.Number+`.ts"`)
	w.Header().Del("Accept-Ranges")
	w.WriteHeader(http.StatusOK)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	writer := taterTVStreamWriter{
		w:             w,
		stream:        stream,
		streamTracker: h.streamTracker,
	}

	consecutiveFailures := 0
	for streamReq.Context().Err() == nil {
		guide, err := taterTVEnsureGuide(cfg, baseURL, time.Now())
		if err != nil {
			consecutiveFailures++
			slog.WarnContext(streamReq.Context(), "Failed to refresh Tube TV guide for stream", "channel", channel.Number, "error", err)
			time.Sleep(time.Duration(consecutiveFailures) * 500 * time.Millisecond)
			continue
		}
		if nextChannel, ok := taterTVFindChannel(guide.Channels, number); ok {
			channel = nextChannel
			startedAt = guide.StartedAt
		}
		if len(channel.Schedule) == 0 || channel.TotalDuration <= 0 {
			return
		}

		items, err := taterTVResolveConcatItems(cfg, channel, startedAt, time.Now(), taterTVConcatRunWindow.Seconds())
		if err != nil {
			consecutiveFailures++
			slog.WarnContext(streamReq.Context(), "Failed to prepare Tube TV channel run", "channel", channel.Number, "error", err)
			time.Sleep(time.Duration(consecutiveFailures) * 500 * time.Millisecond)
			continue
		}
		concatPath, cleanup, err := taterTVWriteConcatFile(items)
		if err != nil {
			consecutiveFailures++
			slog.WarnContext(streamReq.Context(), "Failed to write Tube TV concat plan", "channel", channel.Number, "error", err)
			time.Sleep(time.Duration(consecutiveFailures) * 500 * time.Millisecond)
			continue
		}

		if h.streamTracker != nil && stream != nil && len(items) > 0 {
			h.streamTracker.SetMediaInfo(stream.ID, items[0].FullDuration, items[0].StartSeconds)
		}
		args := buildTaterTVChannelConcatTranscodeArgs(transcodeCfg, profile, accel, concatPath)
		var stderr limitedBuffer
		cmd := exec.CommandContext(streamReq.Context(), ffmpegPath, args...)
		cmd.Stdout = writer
		cmd.Stderr = &stderr
		firstTitle := ""
		firstKind := ""
		if len(items) > 0 {
			firstTitle = items[0].Title
			firstKind = items[0].Kind
		}
		slog.InfoContext(streamReq.Context(), "Starting Tube TV channel run",
			"channel", channel.Number,
			"items", len(items),
			"first_title", firstTitle,
			"first_kind", firstKind,
			"profile", profileID,
			"hardware_acceleration", effectiveAccel,
			"video_codec", videoCodec)
		err = cmd.Run()
		cleanup()
		if err != nil && streamReq.Context().Err() == nil {
			consecutiveFailures++
			slog.WarnContext(streamReq.Context(), "Tube TV channel run failed",
				"channel", channel.Number,
				"error", err,
				"stderr", stderr.String())
			if consecutiveFailures >= 3 {
				time.Sleep(time.Duration(consecutiveFailures) * 500 * time.Millisecond)
			}
			continue
		}
		consecutiveFailures = 0
	}
}

type taterTVStreamItem struct {
	Title           string
	Kind            string
	Path            string
	StartSeconds    float64
	DurationSeconds float64
	FullDuration    float64
}

type taterTVStreamWriter struct {
	w             http.ResponseWriter
	stream        *nzbfilesystem.ActiveStream
	streamTracker *StreamTracker
}

func (w taterTVStreamWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if n > 0 && w.stream != nil {
		atomic.AddInt64(&w.stream.BytesSent, int64(n))
		atomic.AddInt64(&w.stream.CurrentOffset, int64(n))
		if w.streamTracker != nil {
			w.streamTracker.Touch(w.stream.ID)
		}
	}
	if flusher, ok := w.w.(http.Flusher); ok {
		flusher.Flush()
	}
	return n, err
}

func taterTVStreamChannelNumber(path string) string {
	rest := strings.TrimPrefix(path, "/api/tater/tv/channel/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[1] != "stream" {
		return ""
	}
	number, err := url.PathUnescape(parts[0])
	if err != nil {
		return ""
	}
	return strings.TrimSpace(number)
}

func taterHTTPRequestBaseURL(r *http.Request) string {
	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	return strings.TrimRight(proto+"://"+host, "/")
}

func taterTVFindChannel(channels []taterTVChannel, number string) (taterTVChannel, bool) {
	for _, channel := range channels {
		if strings.EqualFold(strings.TrimSpace(channel.Number), strings.TrimSpace(number)) {
			return channel, true
		}
	}
	return taterTVChannel{}, false
}

func taterTVRequestedTranscodeProfile(cfg *config.Config, r *http.Request) (string, transcodeProfile) {
	profileID := strings.TrimSpace(r.URL.Query().Get("profile"))
	if profileID == "" && cfg != nil {
		profileID = cfg.Transcoding.Profile
	}
	profile, ok := transcodeProfiles[profileID]
	if !ok {
		profileID = "crt_480p"
		profile = transcodeProfiles[profileID]
	}
	return profileID, profile
}

func taterTVCurrentSchedulePosition(channel taterTVChannel, startedAt, now time.Time) (int, float64, float64) {
	if len(channel.Schedule) == 0 || channel.TotalDuration <= 0 {
		return -1, 0, 0
	}
	if startedAt.IsZero() {
		startedAt = now
	}
	elapsed := now.Sub(startedAt).Seconds()
	if elapsed < 0 || math.IsNaN(elapsed) || math.IsInf(elapsed, 0) {
		elapsed = 0
	}
	position := math.Mod(elapsed, channel.TotalDuration)
	for i, row := range channel.Schedule {
		start := rowFloat(row, "start")
		end := rowFloat(row, "end")
		if position >= start && position < end {
			segmentOffset := math.Max(0, position-start)
			mediaOffset := math.Max(0, rowFloat(row, "mediaOffset"))
			return i, mediaOffset + segmentOffset, math.Max(0, rowFloat(row, "duration")-segmentOffset)
		}
	}
	row := channel.Schedule[0]
	return 0, math.Max(0, rowFloat(row, "mediaOffset")), math.Max(0, rowFloat(row, "duration"))
}

func taterTVResolveConcatItems(cfg *config.Config, channel taterTVChannel, startedAt, now time.Time, maxDurationSeconds float64) ([]taterTVStreamItem, error) {
	index, startOffset, segmentRemaining := taterTVCurrentSchedulePosition(channel, startedAt, now)
	if index < 0 || index >= len(channel.Schedule) {
		return nil, fmt.Errorf("schedule position unavailable")
	}
	if maxDurationSeconds <= 0 {
		maxDurationSeconds = taterTVConcatRunWindow.Seconds()
	}

	items := make([]taterTVStreamItem, 0, 24)
	total := 0.0
	failures := 0
	for i := index; i < len(channel.Schedule) && total < maxDurationSeconds && len(items) < taterTVMaxConcatItems; i++ {
		row := channel.Schedule[i]
		offset := rowFloat(row, "mediaOffset")
		remaining := rowFloat(row, "duration")
		if i == index {
			offset = startOffset
			remaining = segmentRemaining
		}
		item, err := taterTVResolveStreamItem(cfg, row, offset, remaining)
		if err != nil {
			failures++
			continue
		}
		if item.DurationSeconds <= 0 {
			item.DurationSeconds = math.Max(0, rowFloat(row, "duration"))
		}
		if item.DurationSeconds <= 0 && item.FullDuration > item.StartSeconds {
			item.DurationSeconds = item.FullDuration - item.StartSeconds
		}
		items = append(items, item)
		if item.DurationSeconds > 0 {
			total += item.DurationSeconds
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no playable schedule items found; skipped %d entries", failures)
	}
	return items, nil
}

func taterTVResolveStreamItem(cfg *config.Config, row map[string]any, startOffset, durationOverride float64) (taterTVStreamItem, error) {
	kind := strings.ToLower(strings.TrimSpace(rowString(row, "kind")))
	if kind == "" {
		kind = "video"
	}
	title := rowString(row, "title")
	if title == "" {
		title = "Tater Tube"
	}
	path, err := taterTVResolveSchedulePath(cfg, row)
	if err != nil {
		return taterTVStreamItem{}, err
	}
	duration := durationOverride
	if duration <= 0 {
		duration = rowFloat(row, "duration")
	}
	fullDuration := rowFloat(row, "fullDuration")
	if fullDuration <= 0 {
		fullDuration = duration
	}
	if duration <= 0 {
		duration = fullDuration
	}
	return taterTVStreamItem{
		Title:           title,
		Kind:            kind,
		Path:            path,
		StartSeconds:    math.Max(0, startOffset),
		DurationSeconds: math.Max(0, duration),
		FullDuration:    math.Max(0, fullDuration),
	}, nil
}

func taterTVResolveSchedulePath(cfg *config.Config, row map[string]any) (string, error) {
	if strings.EqualFold(rowString(row, "kind"), "commercial") {
		category := taterTVCategoryID(rowString(row, "categoryId"), "")
		name := taterTVSafeFileName(rowString(row, "name"))
		if category == "" || name == "" {
			if u, err := url.Parse(rowString(row, "url")); err == nil {
				category = taterTVCategoryID(u.Query().Get("category"), "")
				name = taterTVSafeFileName(u.Query().Get("name"))
			}
		}
		if category == "" || name == "" {
			return "", fmt.Errorf("commercial category/name missing")
		}
		path := filepath.Join(taterTVCommercialRoot(cfg), category, name)
		if !isMediaExtension(filepath.Ext(path)) {
			return "", fmt.Errorf("unsupported commercial file type")
		}
		if stat, err := os.Stat(path); err != nil || stat.IsDir() {
			return "", fmt.Errorf("commercial file not found")
		}
		return path, nil
	}

	categoryID := strings.TrimPrefix(rowString(row, "categoryId"), "local:")
	sourceIndex := rowInt(row, "sourceIndex", 0)
	relPath := cleanLocalRelativePath(rowString(row, "path"))
	if categoryID == "" || relPath == "" {
		return "", fmt.Errorf("local media category/path missing")
	}
	cat, ok := taterLocalMediaCategory(cfg, categoryID)
	if !ok {
		return "", fmt.Errorf("local media category not found")
	}
	paths := taterLocalMediaCategoryPaths(cat)
	if sourceIndex < 0 || sourceIndex >= len(paths) {
		return "", fmt.Errorf("local media source not found")
	}
	path, err := safeLocalPath(paths[sourceIndex], relPath)
	if err != nil {
		return "", err
	}
	if !isLocalStreamExtension(filepath.Ext(path)) {
		return "", fmt.Errorf("unsupported media file type")
	}
	if stat, err := os.Stat(path); err != nil || stat.IsDir() {
		return "", fmt.Errorf("local media file not found")
	}
	return path, nil
}

func taterTVWriteConcatFile(items []taterTVStreamItem) (string, func(), error) {
	if len(items) == 0 {
		return "", func() {}, fmt.Errorf("no concat items")
	}
	file, err := os.CreateTemp("", "tater-tv-channel-*.ffconcat")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() {
		_ = os.Remove(file.Name())
	}
	var b strings.Builder
	b.WriteString("ffconcat version 1.0\n")
	for _, item := range items {
		if strings.ContainsAny(item.Path, "\r\n") {
			_ = file.Close()
			cleanup()
			return "", func() {}, fmt.Errorf("media path contains unsupported newline")
		}
		b.WriteString("file ")
		b.WriteString(taterTVFFConcatQuote(item.Path))
		b.WriteByte('\n')
		if item.StartSeconds > 0 {
			b.WriteString("inpoint ")
			b.WriteString(strconv.FormatFloat(item.StartSeconds, 'f', 3, 64))
			b.WriteByte('\n')
		}
		if item.DurationSeconds > 0 {
			outpoint := item.StartSeconds + item.DurationSeconds
			if outpoint > item.StartSeconds {
				b.WriteString("outpoint ")
				b.WriteString(strconv.FormatFloat(outpoint, 'f', 3, 64))
				b.WriteByte('\n')
			}
		}
	}
	if _, err := file.WriteString(b.String()); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return file.Name(), cleanup, nil
}

func taterTVFFConcatQuote(path string) string {
	var b strings.Builder
	b.Grow(len(path) + 2)
	b.WriteByte('\'')
	for _, r := range path {
		switch r {
		case '\'':
			b.WriteString("'\\''")
		case '\\':
			b.WriteString("\\\\")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

func buildTaterTVChannelTranscodeArgs(cfg config.TranscodingConfig, profile transcodeProfile, accel string, inputPath string, startSeconds, durationSeconds float64) []string {
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
	if durationSeconds > 0 {
		args = append(args, "-t", strconv.FormatFloat(durationSeconds, 'f', 3, 64))
	}
	args = append(args,
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-sn",
	)

	videoCodec, filters := transcodeVideoSettings(accel, cfg.HardwareDevice, profile)
	if filters != "" {
		args = append(args, "-vf", filters)
	}
	args = append(args,
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
		"-muxdelay", "0",
		"-muxpreload", "0",
		"-f", "mpegts",
		"pipe:1",
	)
	return args
}

func buildTaterTVChannelConcatTranscodeArgs(cfg config.TranscodingConfig, profile transcodeProfile, accel string, concatPath string) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-nostdin",
	}
	args = append(args, transcodeHardwareInitArgs(cfg, accel)...)
	args = append(args,
		"-readrate", "1.0",
		"-f", "concat",
		"-safe", "0",
		"-protocol_whitelist", "file,http,tcp,https,tls,pipe",
		"-i", concatPath,
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-sn",
	)

	videoCodec, filters := transcodeVideoSettings(accel, cfg.HardwareDevice, profile)
	if filters != "" {
		args = append(args, "-vf", filters)
	}
	args = append(args,
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
		"-muxdelay", "0",
		"-muxpreload", "0",
		"-mpegts_flags", "+resend_headers",
		"-f", "mpegts",
		"pipe:1",
	)
	return args
}

func rowString(row map[string]any, key string) string {
	if row == nil {
		return ""
	}
	value, ok := row[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func rowFloat(row map[string]any, key string) float64 {
	if row == nil {
		return 0
	}
	value, ok := row[key]
	if !ok || value == nil {
		return 0
	}
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case uint64:
		return float64(v)
	default:
		out, _ := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(v)), 64)
		return out
	}
}

func rowInt(row map[string]any, key string, fallback int) int {
	if row == nil {
		return fallback
	}
	value, ok := row[key]
	if !ok || value == nil {
		return fallback
	}
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		out, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(v)))
		if err != nil {
			return fallback
		}
		return out
	}
}
