package api

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"mime"
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
	taterTVSegmentRunWindow = 6 * time.Hour
	taterTVMaxSegmentItems  = 256
)

func NewTaterTVStreamHandler(configGetter config.ConfigGetter, streamTracker *StreamTracker) *TaterTVStreamHandler {
	return &TaterTVStreamHandler{configGetter: configGetter, streamTracker: streamTracker}
}

func (h *TaterTVStreamHandler) GetHTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/item/") {
			h.serveItem(w, r)
			return
		}
		if strings.Contains(r.URL.Path, "/playlist.m3u8") {
			h.serveHLSPlaylist(w, r)
			return
		}
		if strings.Contains(r.URL.Path, "/hls/") {
			h.serveHLSSegment(w, r)
			return
		}
		h.serveChannel(w, r)
	})
}

func (h *TaterTVStreamHandler) serveItem(w http.ResponseWriter, r *http.Request) {
	cfg, _, player, ok := h.authorizeTaterTVRequest(w, r)
	if !ok {
		return
	}
	if !taterTubeTVEnabled(cfg) {
		http.Error(w, "Tube TV is not enabled", http.StatusServiceUnavailable)
		return
	}

	number, itemIndex, ok := taterTVChannelItemFromPath(r.URL.Path)
	if !ok {
		http.Error(w, "Channel item required", http.StatusBadRequest)
		return
	}
	guide, err := taterTVEnsureGuide(cfg, taterHTTPRequestBaseURL(r), time.Now())
	if err != nil {
		http.Error(w, "Failed to build TV lineup", http.StatusServiceUnavailable)
		return
	}
	channel, ok := taterTVFindChannel(guide.Channels, number)
	if !ok || itemIndex < 0 || itemIndex >= len(channel.Schedule) {
		http.Error(w, "Channel item not found", http.StatusNotFound)
		return
	}

	row := channel.Schedule[itemIndex]
	startSeconds := parseTranscodeStartSeconds(r.URL.Query().Get("start"))
	mediaOffset := math.Max(0, rowFloat(row, "mediaOffset"))
	if startSeconds <= 0 {
		startSeconds = mediaOffset
	}
	segmentOffset := math.Max(0, startSeconds-mediaOffset)
	remaining := math.Max(0, rowFloat(row, "duration")-segmentOffset)
	item, err := taterTVResolveStreamItem(cfg, row, startSeconds, remaining)
	if err != nil {
		http.Error(w, "Channel item unavailable", http.StatusNotFound)
		return
	}

	file, err := os.Open(item.Path)
	if err != nil {
		http.Error(w, "Channel item unavailable", http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.Error(w, "Channel item unavailable", http.StatusNotFound)
		return
	}

	streamReq := r
	var stream *nzbfilesystem.ActiveStream
	if h.streamTracker != nil {
		streamCtx, cancel := context.WithCancel(r.Context())
		streamReq = r.WithContext(streamCtx)
		defer cancel()
		stream = h.streamTracker.AddStream(
			"Tube TV CH "+channel.Number+" - "+item.Title,
			"Tube TV",
			taterPlayerDisplayName(player),
			r.RemoteAddr,
			r.UserAgent(),
			info.Size(),
		)
		h.streamTracker.SetPlayerID(stream.ID, player.ID)
		h.streamTracker.SetCancelFunc(stream.ID, cancel)
		h.streamTracker.SetMediaInfo(stream.ID, item.FullDuration, item.StartSeconds)
		defer h.streamTracker.Remove(stream.ID)
	}

	transcoder := &StreamHandler{configGetter: h.configGetter, streamTracker: h.streamTracker}
	if !transcoder.shouldTranscode(r, item.Path) {
		if mimeType := mime.TypeByExtension(filepath.Ext(item.Path)); mimeType != "" {
			w.Header().Set("Content-Type", mimeType)
		} else {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Disposition", `inline; filename="`+filepath.Base(item.Path)+`"`)
		var streamWriter http.ResponseWriter = w
		if stream != nil {
			streamWriter = &trackedResponseWriter{
				ResponseWriter: w,
				stream:         stream,
				streamTracker:  h.streamTracker,
			}
		}
		http.ServeContent(streamWriter, streamReq, filepath.Base(item.Path), info.ModTime(), file)
		return
	}

	ffmpegPath := effectiveFFmpegPath(cfg.Transcoding.FFmpegPath)
	if _, err := exec.LookPath(ffmpegPath); err != nil {
		http.Error(w, "Transcoding unavailable: ffmpeg not found", http.StatusServiceUnavailable)
		return
	}
	profileID, profile := taterTVRequestedTranscodeProfile(cfg, r)
	requestedCodec := requestedTranscodeCodec(r)
	requestedAccel := strings.TrimSpace(r.URL.Query().Get("hwaccel"))
	if requestedAccel == "" {
		requestedAccel = cfg.Transcoding.HardwareAcceleration
	}
	if requestedAccel == "" {
		requestedAccel = "none"
	}
	accel, selectedHardwareDevice, videoCodecPreference := transcoder.selectTranscodeAccelerationAndCodec(
		streamReq.Context(), ffmpegPath, cfg.Transcoding, profile, requestedAccel, requestedCodec)
	if requestedCodec == transcodeCodecHEVC && videoCodecPreference != transcodeCodecHEVC {
		if fallbackID, fallbackProfile, available := requestedFallbackTranscodeProfile(r); available {
			profileID = fallbackID
			profile = fallbackProfile
			accel, selectedHardwareDevice = transcoder.selectTranscodeAcceleration(
				streamReq.Context(), ffmpegPath, cfg.Transcoding, profile, requestedAccel)
		}
	}
	transcodeCfg := cfg.Transcoding
	if selectedHardwareDevice != "" {
		transcodeCfg.HardwareDevice = selectedHardwareDevice
	}
	videoCodec, _ := transcodeVideoSettingsForCodec(accel, transcodeCfg.HardwareDevice, profile, videoCodecPreference)
	effectiveAccel := effectiveTranscodeHardwareAccel(videoCodec)
	hardwareDevice := effectiveTranscodeHardwareDevice(effectiveAccel, transcodeCfg.HardwareDevice)
	if stream != nil {
		h.streamTracker.SetTranscodingInfo(stream.ID, profileID, profile.Name, effectiveAccel, hardwareDevice, videoCodec, effectiveAccel != "" && effectiveAccel != "none")
	}

	logoFile := ""
	if taterTVChannelLogosEnabled(cfg) && channel.LogoPath != "" {
		if resolvedLogo, logoErr := taterTVResolveLogoFile(streamReq.Context(), cfg, channel.LogoPath); logoErr == nil {
			logoFile = resolvedLogo
		} else {
			slog.WarnContext(streamReq.Context(), "Tube TV channel logo unavailable",
				"channel", channel.Number, "logo_path", channel.LogoPath, "error", logoErr)
		}
	}

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `inline; filename="TaterTube-CH`+channel.Number+`-item.ts"`)
	w.Header().Del("Accept-Ranges")
	w.WriteHeader(http.StatusOK)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	err = runTaterTVChannelSegments(
		streamReq.Context(),
		ffmpegPath,
		transcodeCfg,
		profile,
		accel,
		videoCodecPreference,
		taterTVStreamWriter{w: w, stream: stream, streamTracker: h.streamTracker},
		h.streamTracker,
		stream,
		channel,
		[]taterTVStreamItem{item},
		logoFile,
	)
	if err != nil && streamReq.Context().Err() == nil {
		slog.WarnContext(streamReq.Context(), "Tube TV item stream failed",
			"channel", channel.Number, "item", itemIndex, "title", item.Title, "error", err)
	}
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
	if !taterTubeTVEnabled(cfg) {
		http.Error(w, "Tube TV is not enabled", http.StatusServiceUnavailable)
		return
	}

	number := taterTVChannelNumberFromPath(r.URL.Path)
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
	requestedCodec := requestedTranscodeCodec(r)
	requestedAccel := strings.TrimSpace(r.URL.Query().Get("hwaccel"))
	if requestedAccel == "" {
		requestedAccel = cfg.Transcoding.HardwareAcceleration
	}
	if requestedAccel == "" {
		requestedAccel = "none"
	}
	transcoder := &StreamHandler{configGetter: h.configGetter, streamTracker: h.streamTracker}
	accel, selectedHardwareDevice, videoCodecPreference := transcoder.selectTranscodeAccelerationAndCodec(r.Context(), ffmpegPath, cfg.Transcoding, profile, requestedAccel, requestedCodec)
	if requestedCodec == transcodeCodecHEVC && videoCodecPreference != transcodeCodecHEVC {
		if fallbackID, fallbackProfile, ok := requestedFallbackTranscodeProfile(r); ok {
			profileID = fallbackID
			profile = fallbackProfile
			accel, selectedHardwareDevice = transcoder.selectTranscodeAcceleration(r.Context(), ffmpegPath, cfg.Transcoding, profile, requestedAccel)
		}
	}
	transcodeCfg := cfg.Transcoding
	if selectedHardwareDevice != "" {
		transcodeCfg.HardwareDevice = selectedHardwareDevice
	}
	videoCodec, _ := transcodeVideoSettingsForCodec(accel, transcodeCfg.HardwareDevice, profile, videoCodecPreference)
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

		items, err := taterTVResolveStreamItems(cfg, channel, startedAt, time.Now(), taterTVSegmentRunWindow.Seconds())
		if err != nil {
			consecutiveFailures++
			slog.WarnContext(streamReq.Context(), "Failed to prepare Tube TV channel run", "channel", channel.Number, "error", err)
			time.Sleep(time.Duration(consecutiveFailures) * 500 * time.Millisecond)
			continue
		}
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
		logoFile := ""
		if taterTVChannelLogosEnabled(cfg) && channel.LogoPath != "" {
			resolvedLogo, logoErr := taterTVResolveLogoFile(streamReq.Context(), cfg, channel.LogoPath)
			if logoErr != nil {
				slog.WarnContext(streamReq.Context(), "Tube TV channel logo unavailable",
					"channel", channel.Number,
					"logo_path", channel.LogoPath,
					"error", logoErr)
			} else {
				logoFile = resolvedLogo
			}
		}
		err = runTaterTVChannelSegments(streamReq.Context(), ffmpegPath, transcodeCfg, profile, accel, videoCodecPreference, writer, h.streamTracker, stream, channel, items, logoFile)
		if err != nil && streamReq.Context().Err() == nil {
			consecutiveFailures++
			slog.WarnContext(streamReq.Context(), "Tube TV channel run failed",
				"channel", channel.Number,
				"error", err,
				"first_title", firstTitle,
				"first_kind", firstKind)
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

func taterTVLogoForItem(item taterTVStreamItem, logoFile string) string {
	if taterTVIsInterstitial(item.Kind) {
		return ""
	}
	return logoFile
}

func runTaterTVChannelSegments(ctx context.Context, ffmpegPath string, cfg config.TranscodingConfig, profile transcodeProfile, accel, preferredCodec string, writer taterTVStreamWriter, tracker *StreamTracker, stream *nzbfilesystem.ActiveStream, channel taterTVChannel, items []taterTVStreamItem, logoFile string) error {
	if len(items) == 0 {
		return fmt.Errorf("no channel segments")
	}
	played := 0
	failed := 0
	for index, item := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if tracker != nil && stream != nil {
			tracker.SetMediaInfo(stream.ID, item.FullDuration, item.StartSeconds)
		}
		args := buildTaterTVChannelTranscodeArgsWithCodec(cfg, profile, accel, preferredCodec, item.Path, item.StartSeconds, item.DurationSeconds, taterTVLogoForItem(item, logoFile), channel.LogoPosition)
		videoCodec, _ := transcodeVideoSettingsForCodec(accel, cfg.HardwareDevice, profile, preferredCodec)
		var stderr limitedBuffer
		cmd := exec.CommandContext(ctx, ffmpegPath, args...)
		cmd.Stdout = writer
		cmd.Stderr = &stderr
		slog.InfoContext(ctx, "Starting Tube TV channel segment",
			"channel", channel.Number,
			"index", index,
			"items", len(items),
			"title", item.Title,
			"kind", item.Kind,
			"path", item.Path,
			"start_seconds", item.StartSeconds,
			"duration_seconds", item.DurationSeconds,
			"profile", profile.Name,
			"video_codec", videoCodec)
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			failed++
			slog.WarnContext(ctx, "Tube TV channel segment failed; skipping",
				"channel", channel.Number,
				"index", index,
				"title", item.Title,
				"kind", item.Kind,
				"error", err,
				"stderr", stderr.String())
			continue
		}
		played++
	}
	if played == 0 {
		return fmt.Errorf("all channel segments failed; failures=%d", failed)
	}
	return nil
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
	return taterTVChannelNumberFromPath(path)
}

func taterTVChannelNumberFromPath(path string) string {
	rest := strings.TrimPrefix(path, "/api/tater/tv/channel/")
	parts := strings.Split(rest, "/")
	if len(parts) < 1 {
		return ""
	}
	number, err := url.PathUnescape(parts[0])
	if err != nil {
		return ""
	}
	return strings.TrimSpace(number)
}

func taterTVChannelItemFromPath(path string) (string, int, bool) {
	rest := strings.TrimPrefix(path, "/api/tater/tv/channel/")
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[1] != "item" {
		return "", -1, false
	}
	number, err := url.PathUnescape(parts[0])
	if err != nil || strings.TrimSpace(number) == "" {
		return "", -1, false
	}
	index, err := strconv.Atoi(parts[2])
	if err != nil || index < 0 {
		return "", -1, false
	}
	return strings.TrimSpace(number), index, true
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

func taterTVResolveStreamItems(cfg *config.Config, channel taterTVChannel, startedAt, now time.Time, maxDurationSeconds float64) ([]taterTVStreamItem, error) {
	index, startOffset, segmentRemaining := taterTVCurrentSchedulePosition(channel, startedAt, now)
	if index < 0 || index >= len(channel.Schedule) {
		return nil, fmt.Errorf("schedule position unavailable")
	}
	if maxDurationSeconds <= 0 {
		maxDurationSeconds = taterTVSegmentRunWindow.Seconds()
	}

	items := make([]taterTVStreamItem, 0, 24)
	total := 0.0
	failures := 0
	for i := index; i < len(channel.Schedule) && total < maxDurationSeconds && len(items) < taterTVMaxSegmentItems; i++ {
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
	if strings.EqualFold(rowString(row, "kind"), "bumper") {
		placement := taterTVNormalizeBumperPlacement(rowString(row, "placement"))
		groupID := taterTVCategoryID(rowString(row, "groupId"), "")
		rawName := strings.TrimSpace(rowString(row, "name"))
		name := taterTVSafeFileName(rawName)
		if placement == "" || groupID == "" || rawName == "" {
			if u, err := url.Parse(rowString(row, "url")); err == nil {
				placement = taterTVNormalizeBumperPlacement(u.Query().Get("placement"))
				groupID = taterTVCategoryID(u.Query().Get("group"), "")
				rawName = strings.TrimSpace(u.Query().Get("name"))
				name = taterTVSafeFileName(rawName)
			}
		}
		if placement == "" || groupID == "" || rawName == "" {
			return "", fmt.Errorf("bumper placement/group/name missing")
		}
		path := filepath.Join(taterTVBumperGroupPath(cfg, placement, groupID), name)
		if !isMediaExtension(filepath.Ext(path)) {
			return "", fmt.Errorf("unsupported bumper file type")
		}
		if stat, err := os.Stat(path); err != nil || stat.IsDir() {
			return "", fmt.Errorf("bumper file not found")
		}
		return path, nil
	}
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

func buildTaterTVChannelTranscodeArgs(cfg config.TranscodingConfig, profile transcodeProfile, accel string, inputPath string, startSeconds, durationSeconds float64, logoFile, logoPosition string) []string {
	return buildTaterTVChannelTranscodeArgsWithCodec(cfg, profile, accel, transcodeCodecH264, inputPath, startSeconds, durationSeconds, logoFile, logoPosition)
}

func buildTaterTVChannelTranscodeArgsWithCodec(cfg config.TranscodingConfig, profile transcodeProfile, accel, preferredCodec string, inputPath string, startSeconds, durationSeconds float64, logoFile, logoPosition string) []string {
	return buildFFmpegTranscodeArgsWithOptions(cfg, profile, accel, preferredCodec, transcodeOutputOptions{
		InputPath:       inputPath,
		StartSeconds:    startSeconds,
		DurationSeconds: durationSeconds,
		LogoFile:        logoFile,
		LogoPosition:    logoPosition,
	})
}

func taterTVChannelLogoFilter(baseFilters string, profile transcodeProfile, logoPosition string) string {
	preFilters, postFilters := splitTaterTVOverlayFilters(baseFilters)
	if strings.TrimSpace(preFilters) == "" {
		preFilters = "null"
	}
	logoWidth := clampInt(profile.MaxWidth/12, 96, 220)
	marginX := clampInt(profile.MaxWidth/64, 12, 48)
	marginY := clampInt(profile.MaxHeight/36, 10, 40)
	xExpr := fmt.Sprintf("W-w-%d", marginX)
	yExpr := fmt.Sprintf("H-h-%d", marginY)
	switch config.NormalizeTubeTVLogoPosition(logoPosition) {
	case "top_left":
		xExpr = fmt.Sprintf("%d", marginX)
		yExpr = fmt.Sprintf("%d", marginY)
	case "top_right":
		yExpr = fmt.Sprintf("%d", marginY)
	case "bottom_left":
		xExpr = fmt.Sprintf("%d", marginX)
	}
	logoFilters := fmt.Sprintf(
		"scale=w=%d:h=-1:force_original_aspect_ratio=decrease,format=rgba,colorchannelmixer=aa=0.82",
		logoWidth,
	)
	return fmt.Sprintf(
		"[0:v]%s[base];[1:v]%s[logo];[base][logo]overlay=x=%s:y=%s:format=auto%s[vout]",
		preFilters,
		logoFilters,
		xExpr,
		yExpr,
		postFilters,
	)
}

func splitTaterTVOverlayFilters(filters string) (preFilters, postFilters string) {
	filters = strings.TrimSpace(filters)
	for _, suffix := range []string{",format=nv12,hwupload", ",format=nv12"} {
		if strings.HasSuffix(filters, suffix) {
			return strings.TrimSuffix(filters, suffix), suffix
		}
	}
	return filters, ""
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
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
