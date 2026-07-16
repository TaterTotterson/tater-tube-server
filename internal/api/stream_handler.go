package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"math"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/auth"
	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/TaterTotterson/tater-tube-server/internal/database"
	"github.com/TaterTotterson/tater-tube-server/internal/nzbfilesystem"
	"github.com/TaterTotterson/tater-tube-server/internal/utils"
	"github.com/spf13/afero"
)

// StreamHandler handles HTTP streaming requests for files in NzbFilesystem
// Uses http.ServeContent for automatic Range request handling, ETag support,
// and proper HTTP caching semantics
type StreamHandler struct {
	nzbFilesystem *nzbfilesystem.NzbFilesystem
	userRepo      *database.UserRepository
	streamTracker *StreamTracker
	configGetter  config.ConfigGetter
}

// MonitoredFile wraps an afero.File to track read progress and support cancellation
type MonitoredFile struct {
	file          afero.File
	stream        *nzbfilesystem.ActiveStream
	ctx           context.Context
	streamTracker *StreamTracker
}

func (m *MonitoredFile) Read(p []byte) (n int, err error) {
	if err := m.ctx.Err(); err != nil {
		return 0, err
	}
	n, err = m.file.Read(p)
	if n > 0 {
		atomic.AddInt64(&m.stream.BytesSent, int64(n))
		atomic.AddInt64(&m.stream.CurrentOffset, int64(n))
		if m.streamTracker != nil {
			m.streamTracker.Touch(m.stream.ID)
		}
	}
	return n, err
}

func (m *MonitoredFile) Seek(offset int64, whence int) (int64, error) {
	if err := m.ctx.Err(); err != nil {
		return 0, err
	}
	newOffset, err := m.file.Seek(offset, whence)
	if err == nil {
		atomic.StoreInt64(&m.stream.CurrentOffset, newOffset)
	}
	return newOffset, err
}

func (m *MonitoredFile) Close() error {
	return m.file.Close()
}

// NewStreamHandler creates a new stream handler with the provided filesystem and user repository
func NewStreamHandler(fs *nzbfilesystem.NzbFilesystem, userRepo *database.UserRepository, streamTracker *StreamTracker, configGetter config.ConfigGetter) *StreamHandler {
	return &StreamHandler{
		nzbFilesystem: fs,
		userRepo:      userRepo,
		streamTracker: streamTracker,
		configGetter:  configGetter,
	}
}

// authenticate validates either a paired Tater Tube player token or a legacy
// download_key parameter against user API keys.
// When login is not required, authentication is skipped and an anonymous user is returned.
// Returns the user and true if the download_key matches a hashed API key from any user.
func (h *StreamHandler) authenticate(r *http.Request) (*database.User, bool) {
	ctx := r.Context()

	playerToken := strings.TrimSpace(r.URL.Query().Get("player_token"))
	if playerToken == "" {
		playerToken = bearerToken(r.Header.Get("Authorization"))
	}
	if playerToken == "" {
		playerToken = strings.TrimSpace(r.Header.Get("X-Tater-Player-Token"))
	}
	if playerToken != "" {
		if h.configGetter != nil {
			if player, ok := findTaterPlayerByToken(h.configGetter(), playerToken); ok {
				slog.DebugContext(ctx, "Stream authenticated by Tater Tube player token",
					"player_id", player.ID,
					"path", r.URL.Query().Get("path"))
				playerName := taterPlayerDisplayName(player)
				return &database.User{
					UserID:   player.ID,
					Name:     &playerName,
					Provider: "tater",
				}, true
			}
		}
		slog.WarnContext(ctx, "Stream authentication failed - invalid player token",
			"path", r.URL.Query().Get("path"),
			"remote_addr", r.RemoteAddr)
		return nil, false
	}

	// Extract download_key from query parameter
	downloadKey := r.URL.Query().Get("download_key")
	if downloadKey == "" {
		slog.WarnContext(ctx, "Stream access attempt without player_token or download_key",
			"path", r.URL.Query().Get("path"),
			"remote_addr", r.RemoteAddr)
		return nil, false
	}

	// Get all users with API keys
	if h.userRepo == nil {
		return nil, false
	}
	users, err := h.userRepo.GetAllUsers(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to get users for authentication",
			"error", err)
		return nil, false
	}

	// Check download_key against hashed API keys
	for _, user := range users {
		if user.APIKey == nil || *user.APIKey == "" {
			continue
		}

		// Hash the user's API key with SHA256
		hashedKey := auth.HashAPIKey(*user.APIKey)

		// Compare with provided download_key (constant-time comparison for security)
		if subtle.ConstantTimeCompare([]byte(hashedKey), []byte(downloadKey)) == 1 {
			return user, true
		}
	}

	slog.WarnContext(ctx, "Stream authentication failed - invalid download_key",
		"path", r.URL.Query().Get("path"),
		"remote_addr", r.RemoteAddr)
	return nil, false
}

// GetHTTPHandler returns an http.Handler that serves files from NzbFilesystem
// This handler:
// - Requires authentication via player_token or legacy download_key parameter
// - Preserves context for logging and health tracking
// - Uses http.ServeContent for automatic Range request handling
// - Supports ETag and Last-Modified for caching
// - Provides proper Content-Type detection
func (h *StreamHandler) GetHTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Authenticate using download_key
		_, ok := h.authenticate(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="Stream API"`)
			http.Error(w, "Unauthorized: valid player_token required", http.StatusUnauthorized)
			return
		}

		// Serve the file
		h.serveFile(w, r)
	})
}

// serveFile handles the actual file streaming after authentication
func (h *StreamHandler) serveFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Enrich context with request metadata (similar to Server adapter)
	ctx = context.WithValue(ctx, utils.ContentLengthKey, r.Header.Get("Content-Length"))
	ctx = context.WithValue(ctx, utils.RangeKey, r.Header.Get("Range"))
	ctx = context.WithValue(ctx, utils.Origin, r.RequestURI)
	ctx = context.WithValue(ctx, utils.ShowCorrupted, r.Header.Get("X-Show-Corrupted") == "true")

	// Authenticate again to get user details
	user, ok := h.authenticate(r)
	if !ok {
		// Should have been caught by GetHTTPHandler
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var userName string
	var playerID string
	if user != nil {
		if user.Name != nil && *user.Name != "" {
			userName = *user.Name
		} else {
			userName = user.UserID
		}
		if user.Provider == "tater" {
			playerID = user.UserID
		}
	}

	// Set stream source and username for tracking
	ctx = context.WithValue(ctx, utils.StreamSourceKey, "API")
	ctx = context.WithValue(ctx, utils.StreamPlayerIDKey, playerID)
	ctx = context.WithValue(ctx, utils.StreamUserNameKey, userName)
	ctx = context.WithValue(ctx, utils.ClientIPKey, r.RemoteAddr)
	ctx = context.WithValue(ctx, utils.UserAgentKey, r.UserAgent())

	// Get path from query parameter
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "Path parameter required", http.StatusBadRequest)
		return
	}

	// Open file via NzbFilesystem (handles encryption, health tracking, etc.)
	file, err := h.nzbFilesystem.OpenFile(ctx, path, os.O_RDONLY, 0)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to open file", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// Get file info
	stat, err := file.Stat()
	if err != nil {
		http.Error(w, "Failed to get file information", http.StatusInternalServerError)
		return
	}

	// Check if it's a directory
	if stat.IsDir() {
		http.Error(w, "Cannot stream directory", http.StatusBadRequest)
		return
	}

	if h.shouldTranscode(r, path) {
		h.serveTranscoded(w, r, ctx, path, file)
		return
	}

	// Track stream if tracker is available
	if h.streamTracker != nil {
		// Create a cancellable context for the stream
		streamCtx, cancel := context.WithCancel(ctx)
		defer cancel() // Ensure cleanup

		var streamID string
		// Try to get stream ID from the file itself (created during OpenFile)
		if mvf, ok := file.(*nzbfilesystem.MetadataVirtualFile); ok {
			streamID = mvf.GetStreamID()
		}

		if streamID != "" {
			// Add stream ID to context for low-level tracking
			streamCtx = context.WithValue(streamCtx, utils.StreamIDKey, streamID)

			// Register cancel function in tracker
			h.streamTracker.SetCancelFunc(streamID, cancel)

			streamObj := h.streamTracker.GetStream(streamID)
			if streamObj != nil {
				h.setStreamMediaInfoFromPath(ctx, streamID, path, 0)
				// Wrap the file with monitoring
				monitoredFile := &MonitoredFile{
					file:          file,
					stream:        streamObj,
					ctx:           streamCtx,
					streamTracker: h.streamTracker,
				}

				// Set MIME type based on file extension (prevents internal seeks)
				ext := filepath.Ext(path)
				if ext != "" {
					mimeType := mime.TypeByExtension(ext)
					if mimeType != "" {
						w.Header().Set("Content-Type", mimeType)
					} else {
						w.Header().Set("Content-Type", "application/octet-stream")
					}
				}

				// Indicate support for range requests
				w.Header().Set("Accept-Ranges", "bytes")

				// Set Content-Disposition to inline for browser viewing
				filename := filepath.Base(path)
				w.Header().Set("Content-Disposition", `inline; filename="`+filename+`"`)

				http.ServeContent(w, r, filename, stat.ModTime(), monitoredFile)
				return
			}
		}
	}

	// Fallback if tracker is nil (should not happen in prod)
	ext := filepath.Ext(path)
	if ext != "" {
		mimeType := mime.TypeByExtension(ext)
		if mimeType != "" {
			w.Header().Set("Content-Type", mimeType)
		} else {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
	}
	w.Header().Set("Accept-Ranges", "bytes")
	filename := filepath.Base(path)
	w.Header().Set("Content-Disposition", `inline; filename="`+filename+`"`)
	http.ServeContent(w, r, filename, stat.ModTime(), file)
}

type transcodeProfile struct {
	Name         string
	MaxWidth     int
	MaxHeight    int
	VideoBitrate string
	MaxRate      string
	BufferSize   string
	AudioBitrate string
	Level        string
}

const (
	transcodeCodecH264 = "h264"
	transcodeCodecHEVC = "hevc"
)

var transcodeProfiles = map[string]transcodeProfile{
	"crt_480p": {
		Name:         "CRT 480p",
		MaxWidth:     640,
		MaxHeight:    480,
		VideoBitrate: "1400k",
		MaxRate:      "1800k",
		BufferSize:   "3600k",
		AudioBitrate: "128k",
		Level:        "3.0",
	},
	"hdmi_720p": {
		Name:         "HDMI 720p",
		MaxWidth:     1280,
		MaxHeight:    720,
		VideoBitrate: "4000k",
		MaxRate:      "6000k",
		BufferSize:   "12000k",
		AudioBitrate: "160k",
		Level:        "3.1",
	},
	"hdmi_1080p": {
		Name:         "HDMI 1080p",
		MaxWidth:     1920,
		MaxHeight:    1080,
		VideoBitrate: "8000k",
		MaxRate:      "12000k",
		BufferSize:   "24000k",
		AudioBitrate: "192k",
		Level:        "4.1",
	},
	"hdmi_4k": {
		Name:         "HDMI 4K",
		MaxWidth:     3840,
		MaxHeight:    2160,
		VideoBitrate: "25000k",
		MaxRate:      "35000k",
		BufferSize:   "70000k",
		AudioBitrate: "256k",
		Level:        "5.1",
	},
}

func (h *StreamHandler) shouldTranscode(r *http.Request, path string) bool {
	if h.configGetter == nil {
		return false
	}
	cfg := h.configGetter()
	if cfg == nil {
		return false
	}

	transcodeValue := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("transcode")))
	if r.URL.Query().Get("direct") == "1" ||
		transcodeValue == "0" ||
		transcodeValue == "false" ||
		transcodeValue == "off" ||
		transcodeValue == "no" {
		return false
	}

	forceTranscode := transcodeValue == "1" ||
		transcodeValue == "true" ||
		transcodeValue == "on" ||
		transcodeValue == "yes"
	if !forceTranscode {
		return false
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv", ".mp4", ".m4v", ".mov", ".avi", ".ts", ".m2ts", ".mpg", ".mpeg", ".wmv", ".webm":
		return true
	default:
		return false
	}
}

func (h *StreamHandler) serveTranscoded(w http.ResponseWriter, r *http.Request, ctx context.Context, path string, file afero.File) {
	cfg := h.configGetter()
	if cfg == nil {
		http.Error(w, "Transcoding configuration unavailable", http.StatusServiceUnavailable)
		return
	}

	ffmpegPath := effectiveFFmpegPath(cfg.Transcoding.FFmpegPath)
	if _, err := exec.LookPath(ffmpegPath); err != nil {
		slog.ErrorContext(ctx, "FFmpeg not available for transcoding", "path", ffmpegPath, "error", err)
		http.Error(w, "Transcoding unavailable: ffmpeg not found", http.StatusServiceUnavailable)
		return
	}

	profileID := r.URL.Query().Get("profile")
	if profileID == "" {
		profileID = cfg.Transcoding.Profile
	}
	profile, ok := transcodeProfiles[profileID]
	if !ok {
		profileID = "crt_480p"
		profile = transcodeProfiles["crt_480p"]
	}
	requestedCodec := requestedTranscodeCodec(r)

	accel := r.URL.Query().Get("hwaccel")
	if accel == "" {
		accel = cfg.Transcoding.HardwareAcceleration
	}
	if accel == "" {
		accel = "none"
	}

	accel, selectedHardwareDevice, videoCodecPreference := h.selectTranscodeAccelerationAndCodec(r.Context(), ffmpegPath, cfg.Transcoding, profile, accel, requestedCodec)
	if requestedCodec == transcodeCodecHEVC && videoCodecPreference != transcodeCodecHEVC {
		if fallbackID, fallbackProfile, ok := requestedFallbackTranscodeProfile(r); ok {
			profileID = fallbackID
			profile = fallbackProfile
			accel, selectedHardwareDevice = h.selectTranscodeAcceleration(r.Context(), ffmpegPath, cfg.Transcoding, profile, accel)
		}
	}
	transcodeCfg := cfg.Transcoding
	if selectedHardwareDevice != "" {
		transcodeCfg.HardwareDevice = selectedHardwareDevice
	}
	startSeconds := parseTranscodeStartSeconds(r.URL.Query().Get("start"))
	inputPath := ""
	if startSeconds > 0 {
		inputPath = path
	}
	args := buildFFmpegTranscodeArgsWithCodec(transcodeCfg, profile, accel, videoCodecPreference, inputPath, startSeconds)
	videoCodec, _ := transcodeVideoSettingsForCodec(accel, transcodeCfg.HardwareDevice, profile, videoCodecPreference)
	effectiveAccel := effectiveTranscodeHardwareAccel(videoCodec)
	hardwareDevice := effectiveTranscodeHardwareDevice(effectiveAccel, transcodeCfg.HardwareDevice)
	durationSeconds := h.probeMediaDuration(ctx, path)
	h.markTranscodedStream(w, file, profileID, profile.Name, effectiveAccel, hardwareDevice, videoCodec, startSeconds, durationSeconds)

	cmd := exec.CommandContext(r.Context(), ffmpegPath, args...)
	if inputPath == "" {
		cmd.Stdin = file
	}

	var stderr limitedBuffer
	cmd.Stderr = &stderr
	cmd.Stdout = flushWriter{w: w}

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `inline; filename="`+filepath.Base(path)+`.ts"`)
	w.Header().Del("Accept-Ranges")
	w.WriteHeader(http.StatusOK)

	slog.InfoContext(ctx, "Starting FFmpeg transcode stream",
		"path", path,
		"profile", profileID,
		"profile_name", profile.Name,
		"hardware_acceleration", effectiveAccel,
		"video_codec", videoCodec,
		"start_seconds", startSeconds)

	if err := cmd.Run(); err != nil && r.Context().Err() == nil {
		slog.ErrorContext(ctx, "FFmpeg transcode failed",
			"path", path,
			"profile", profileID,
			"hardware_acceleration", effectiveAccel,
			"video_codec", videoCodec,
			"start_seconds", startSeconds,
			"error", err,
			"stderr", stderr.String())
	}
}

func parseTranscodeStartSeconds(value string) float64 {
	start, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || start <= 0 {
		return 0
	}
	return start
}

func (h *StreamHandler) probeMediaDuration(ctx context.Context, path string) float64 {
	if strings.TrimSpace(path) == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return 0
	}
	ffmpegPath := "ffmpeg"
	if h.configGetter != nil {
		if cfg := h.configGetter(); cfg != nil {
			ffmpegPath = effectiveFFmpegPath(cfg.Transcoding.FFmpegPath)
		}
	}
	return probeMediaDurationSeconds(ctx, ffmpegPath, path)
}

func (h *StreamHandler) setStreamMediaInfoFromPath(ctx context.Context, streamID, path string, playbackStart float64) {
	if h.streamTracker == nil || streamID == "" {
		return
	}
	durationSeconds := h.probeMediaDuration(ctx, path)
	if durationSeconds <= 0 && playbackStart <= 0 {
		return
	}
	h.streamTracker.SetMediaInfo(streamID, durationSeconds, playbackStart)
}

func probeMediaDurationSeconds(parent context.Context, ffmpegPath, path string) float64 {
	duration, err := probeMediaDurationSecondsWithError(parent, ffmpegPath, path)
	if err != nil {
		return 0
	}
	return duration
}

func probeMediaDurationSecondsWithError(parent context.Context, ffmpegPath, path string) (float64, error) {
	ffprobePath := effectiveFFprobePath(ffmpegPath)
	if ffprobePath == "" {
		return 0, fmt.Errorf("ffprobe not found")
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "error",
		"-show_entries", "format=duration:stream=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if err != nil {
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = err.Error()
		}
		return 0, fmt.Errorf("%s: %s", filepath.Base(ffprobePath), reason)
	}
	duration := 0.0
	for _, field := range strings.Fields(string(out)) {
		candidate, err := strconv.ParseFloat(strings.TrimSpace(field), 64)
		if err == nil && candidate > duration && !math.IsNaN(candidate) && !math.IsInf(candidate, 0) {
			duration = candidate
		}
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s returned no duration", filepath.Base(ffprobePath))
	}
	return duration, nil
}

func effectiveFFprobePath(ffmpegPath string) string {
	ffmpegPath = strings.TrimSpace(ffmpegPath)
	if ffmpegPath != "" && ffmpegPath != "ffmpeg" {
		dir := filepath.Dir(ffmpegPath)
		base := filepath.Base(ffmpegPath)
		candidateBase := strings.Replace(base, "ffmpeg", "ffprobe", 1)
		if candidateBase != base {
			candidate := filepath.Join(dir, candidateBase)
			if _, err := exec.LookPath(candidate); err == nil {
				return candidate
			}
		}
	}
	if path, err := exec.LookPath("ffprobe"); err == nil {
		return path
	}
	for _, pattern := range []string{
		"/usr/local/bin/tater-ffprobe",
		"/usr/lib/*-ffmpeg/ffprobe",
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, candidate := range matches {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	return ""
}

func (h *StreamHandler) markTranscodedStream(w http.ResponseWriter, file afero.File, profileID, profileName, hardwareAccel, hardwareDevice, videoCodec string, playbackStartSeconds, durationSeconds float64) {
	if h.streamTracker == nil {
		return
	}

	streamID := ""
	if tracked, ok := w.(*trackedResponseWriter); ok && tracked.stream != nil {
		streamID = tracked.stream.ID
	} else if mvf, ok := file.(*nzbfilesystem.MetadataVirtualFile); ok {
		streamID = mvf.GetStreamID()
	}
	if streamID == "" {
		return
	}

	h.streamTracker.SetTranscodingInfo(
		streamID,
		profileID,
		profileName,
		hardwareAccel,
		hardwareDevice,
		videoCodec,
		hardwareAccel != "" && hardwareAccel != "none",
	)
	if durationSeconds > 0 || playbackStartSeconds > 0 {
		h.streamTracker.SetMediaInfo(streamID, durationSeconds, playbackStartSeconds)
	}
}

func buildFFmpegTranscodeArgs(cfg config.TranscodingConfig, profile transcodeProfile, accel string, inputPath string, startSeconds float64) []string {
	return buildFFmpegTranscodeArgsWithCodec(cfg, profile, accel, transcodeCodecH264, inputPath, startSeconds)
}

func buildFFmpegTranscodeArgsWithCodec(cfg config.TranscodingConfig, profile transcodeProfile, accel, preferredCodec string, inputPath string, startSeconds float64) []string {
	return buildFFmpegTranscodeArgsWithOptions(cfg, profile, accel, preferredCodec, transcodeOutputOptions{
		InputPath:    inputPath,
		StartSeconds: startSeconds,
	})
}

type transcodeOutputOptions struct {
	InputPath       string
	StartSeconds    float64
	DurationSeconds float64
	LogoFile        string
	LogoPosition    string
}

func buildFFmpegTranscodeArgsWithOptions(cfg config.TranscodingConfig, profile transcodeProfile, accel, preferredCodec string, options transcodeOutputOptions) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-nostdin",
	}

	args = append(args, transcodeHardwareInitArgs(cfg, accel)...)
	if strings.TrimSpace(options.InputPath) != "" {
		if options.StartSeconds > 0 {
			args = append(args, "-ss", strconv.FormatFloat(options.StartSeconds, 'f', 3, 64))
		}
		args = append(args, "-i", options.InputPath)
	} else {
		args = append(args, "-i", "pipe:0")
	}

	logoFile := strings.TrimSpace(options.LogoFile)
	if logoFile != "" {
		args = append(args, "-loop", "1", "-framerate", "30", "-i", logoFile)
	}
	if options.DurationSeconds > 0 {
		args = append(args, "-t", strconv.FormatFloat(options.DurationSeconds, 'f', 3, 64))
	}

	videoCodec, filters := transcodeVideoSettingsForCodec(accel, cfg.HardwareDevice, profile, preferredCodec)
	if logoFile != "" {
		args = append(args,
			"-filter_complex", taterTVChannelLogoFilter(filters, profile, options.LogoPosition),
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
	}
	if filters != "" && logoFile == "" {
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

func (h *StreamHandler) selectTranscodeAccelerationAndCodec(ctx context.Context, ffmpegPath string, cfg config.TranscodingConfig, profile transcodeProfile, requestedAccel, requestedCodec string) (string, string, string) {
	codec := normalizeTranscodeCodec(requestedCodec)
	if codec != transcodeCodecHEVC {
		accel, device := h.selectTranscodeAcceleration(ctx, ffmpegPath, cfg, profile, requestedAccel)
		return accel, device, transcodeCodecH264
	}

	accel, device, ok := h.selectTranscodeAccelerationForCodec(ctx, ffmpegPath, cfg, profile, requestedAccel, transcodeCodecHEVC)
	if ok {
		return accel, device, transcodeCodecHEVC
	}

	slog.WarnContext(ctx, "Requested HEVC hardware transcoding is not usable; falling back to H.264",
		"requested", requestedAccel)
	accel, device = h.selectTranscodeAcceleration(ctx, ffmpegPath, cfg, profile, requestedAccel)
	return accel, device, transcodeCodecH264
}

func (h *StreamHandler) selectTranscodeAcceleration(ctx context.Context, ffmpegPath string, cfg config.TranscodingConfig, profile transcodeProfile, requested string) (string, string) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		requested = "none"
	}
	if requested == "none" {
		return requested, ""
	}
	if requested == "auto" {
		detected := detectTranscodingHardware(cfg)
		if detected.Recommended != "" && detected.Recommended != "auto" {
			slog.InfoContext(ctx, "Selected FFmpeg hardware acceleration",
				"requested", requested,
				"selected", detected.Recommended,
				"device", detected.RecommendedDevice)
			return detected.Recommended, detected.RecommendedDevice
		}
		return "none", ""
	}

	if strings.TrimSpace(cfg.HardwareDevice) == "" && (requested == "vaapi" || requested == "qsv") {
		vendors := []string{"intel", "amd"}
		if requested == "qsv" {
			vendors = []string{"intel"}
		}
		device, reason, ok := probeTranscodeEncoderDevices(
			ffmpegPath, cfg, profile, requested,
			candidateDRIRenderDevices(detectDRMGPUVendors(), vendors, ""),
		)
		if ok {
			return requested, device
		}
		slog.WarnContext(ctx, "Configured FFmpeg hardware acceleration is not usable",
			"requested", requested,
			"reason", reason)
		return "none", ""
	}

	ok, reason := probeTranscodeEncoder(ctx, ffmpegPath, cfg, profile, requested)
	if ok {
		return requested, strings.TrimSpace(cfg.HardwareDevice)
	}
	slog.WarnContext(ctx, "Configured FFmpeg hardware acceleration is not usable",
		"requested", requested,
		"reason", reason)
	return "none", ""
}

func (h *StreamHandler) selectTranscodeAccelerationForCodec(ctx context.Context, ffmpegPath string, cfg config.TranscodingConfig, profile transcodeProfile, requested, codec string) (string, string, bool) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		requested = "none"
	}
	if requested == "none" {
		return "", "", false
	}
	if requested == "auto" {
		for _, candidate := range []string{"nvenc", "qsv", "vaapi", "videotoolbox", "v4l2m2m"} {
			if accel, device, ok := h.selectTranscodeAccelerationForCodec(ctx, ffmpegPath, cfg, profile, candidate, codec); ok {
				slog.InfoContext(ctx, "Selected FFmpeg hardware acceleration",
					"requested", requested,
					"selected", accel,
					"device", device,
					"codec", codec)
				return accel, device, true
			}
		}
		return "", "", false
	}

	if strings.TrimSpace(cfg.HardwareDevice) == "" && (requested == "vaapi" || requested == "qsv") {
		vendors := []string{"intel", "amd"}
		if requested == "qsv" {
			vendors = []string{"intel"}
		}
		device, reason, ok := probeTranscodeEncoderDevicesCodec(
			ffmpegPath, cfg, profile, requested, codec,
			candidateDRIRenderDevices(detectDRMGPUVendors(), vendors, ""),
		)
		if ok {
			return requested, device, true
		}
		slog.WarnContext(ctx, "Configured FFmpeg hardware acceleration is not usable",
			"requested", requested,
			"codec", codec,
			"reason", reason)
		return "", "", false
	}

	ok, reason := probeTranscodeEncoderCodec(ctx, ffmpegPath, cfg, profile, requested, codec)
	if ok {
		return requested, strings.TrimSpace(cfg.HardwareDevice), true
	}
	slog.WarnContext(ctx, "Configured FFmpeg hardware acceleration is not usable",
		"requested", requested,
		"codec", codec,
		"reason", reason)
	return "", "", false
}

func transcodeHardwareInitArgs(cfg config.TranscodingConfig, accel string) []string {
	device := strings.TrimSpace(cfg.HardwareDevice)
	if device == "" {
		device = firstDRIRenderDevice()
	}

	switch accel {
	case "vaapi":
		return []string{"-vaapi_device", device}
	case "qsv":
		return []string{
			"-init_hw_device", "vaapi=va:" + device + ",driver=iHD",
			"-init_hw_device", "qsv=qs@va",
			"-filter_hw_device", "qs",
		}
	default:
		return nil
	}
}

func appendVideoEncoderOptions(args []string, videoCodec string, profile transcodeProfile) []string {
	switch videoCodec {
	case "libx264":
		return append(args,
			"-preset", "veryfast",
			"-profile:v", "main",
			"-level:v", profile.Level,
			"-pix_fmt", "yuv420p",
		)
	case "h264_nvenc":
		return append(args, "-preset", "p4", "-profile:v", "main")
	case "hevc_nvenc":
		return append(args, "-preset", "p4")
	case "h264_videotoolbox":
		return append(args, "-profile:v", "main", "-allow_sw", "1")
	case "hevc_videotoolbox":
		return append(args, "-allow_sw", "0")
	case "libx265":
		return append(args,
			"-preset", "veryfast",
			"-pix_fmt", "yuv420p",
		)
	default:
		return args
	}
}

func transcodeVideoSettings(accel, device string, profile transcodeProfile) (codec string, filters string) {
	return transcodeVideoSettingsForCodec(accel, device, profile, transcodeCodecH264)
}

func transcodeVideoSettingsForCodec(accel, device string, profile transcodeProfile, preferredCodec string) (codec string, filters string) {
	scaleFilter := "scale=w=" + strconv.Itoa(profile.MaxWidth) + ":h=" + strconv.Itoa(profile.MaxHeight) + ":force_original_aspect_ratio=decrease:force_divisible_by=2"

	if normalizeTranscodeCodec(preferredCodec) == transcodeCodecHEVC {
		switch accel {
		case "auto":
			if hasDefaultVAAPIDevice() {
				return "hevc_vaapi", scaleFilter + ",format=nv12,hwupload"
			}
			return "libx265", scaleFilter
		case "vaapi":
			return "hevc_vaapi", scaleFilter + ",format=nv12,hwupload"
		case "qsv":
			return "hevc_qsv", scaleFilter + ",format=nv12"
		case "nvenc":
			return "hevc_nvenc", scaleFilter
		case "videotoolbox":
			return "hevc_videotoolbox", scaleFilter
		case "v4l2m2m":
			return "hevc_v4l2m2m", scaleFilter
		default:
			return "libx265", scaleFilter
		}
	}

	switch accel {
	case "auto":
		if hasDefaultVAAPIDevice() {
			return "h264_vaapi", scaleFilter + ",format=nv12,hwupload"
		}
		return "libx264", scaleFilter
	case "vaapi":
		return "h264_vaapi", scaleFilter + ",format=nv12,hwupload"
	case "qsv":
		return "h264_qsv", scaleFilter + ",format=nv12"
	case "nvenc":
		return "h264_nvenc", scaleFilter
	case "videotoolbox":
		return "h264_videotoolbox", scaleFilter
	case "v4l2m2m":
		return "h264_v4l2m2m", scaleFilter
	default:
		return "libx264", scaleFilter
	}
}

func effectiveTranscodeHardwareAccel(videoCodec string) string {
	switch videoCodec {
	case "h264_vaapi", "hevc_vaapi":
		return "vaapi"
	case "h264_qsv", "hevc_qsv":
		return "qsv"
	case "h264_nvenc", "hevc_nvenc":
		return "nvenc"
	case "h264_videotoolbox", "hevc_videotoolbox":
		return "videotoolbox"
	case "h264_v4l2m2m", "hevc_v4l2m2m":
		return "v4l2m2m"
	default:
		return "none"
	}
}

func effectiveTranscodeHardwareDevice(hardwareAccel, configuredDevice string) string {
	if configuredDevice != "" {
		return configuredDevice
	}
	switch hardwareAccel {
	case "vaapi", "qsv":
		return firstDRIRenderDevice()
	}
	return ""
}

func effectiveFFmpegPath(configuredPath string) string {
	configuredPath = strings.TrimSpace(configuredPath)
	if configuredPath == "" || configuredPath == "ffmpeg" {
		const bundledFFmpegPath = "/usr/local/bin/tater-ffmpeg"
		if pathExists(bundledFFmpegPath) {
			return bundledFFmpegPath
		}
		return "ffmpeg"
	}
	return configuredPath
}

func probeTranscodeEncoder(parent context.Context, ffmpegPath string, cfg config.TranscodingConfig, profile transcodeProfile, accel string) (bool, string) {
	return probeTranscodeEncoderCodec(parent, ffmpegPath, cfg, profile, accel, transcodeCodecH264)
}

func probeTranscodeEncoderCodec(parent context.Context, ffmpegPath string, cfg config.TranscodingConfig, profile transcodeProfile, accel, preferredCodec string) (bool, string) {
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()

	args := buildFFmpegTranscodeProbeArgsWithCodec(cfg, profile, accel, preferredCodec)
	out, err := exec.CommandContext(ctx, ffmpegPath, args...).CombinedOutput()
	if ctx.Err() != nil {
		return false, "probe timed out"
	}
	if err != nil {
		reason := strings.TrimSpace(string(out))
		if reason == "" {
			reason = err.Error()
		}
		return false, truncateProbeReason(reason)
	}
	return true, ""
}

func buildFFmpegTranscodeProbeArgs(cfg config.TranscodingConfig, profile transcodeProfile, accel string) []string {
	return buildFFmpegTranscodeProbeArgsWithCodec(cfg, profile, accel, transcodeCodecH264)
}

func buildFFmpegTranscodeProbeArgsWithCodec(cfg config.TranscodingConfig, profile transcodeProfile, accel, preferredCodec string) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
	}
	args = append(args, transcodeHardwareInitArgs(cfg, accel)...)
	args = append(args,
		"-f", "lavfi",
		"-i", "testsrc2=size=640x360:rate=30",
		"-frames:v", "8",
	)

	videoCodec, filters := transcodeVideoSettingsForCodec(accel, cfg.HardwareDevice, profile, preferredCodec)
	if filters != "" {
		args = append(args, "-vf", filters)
	}
	args = append(args,
		"-an",
		"-c:v", videoCodec,
		"-b:v", profile.VideoBitrate,
		"-maxrate", profile.MaxRate,
		"-bufsize", profile.BufferSize,
	)
	args = appendVideoEncoderOptions(args, videoCodec, profile)
	args = append(args, "-f", "null", "-")
	return args
}

func normalizeTranscodeCodec(codec string) string {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "hevc", "h265", "h.265", "x265":
		return transcodeCodecHEVC
	default:
		return transcodeCodecH264
	}
}

func requestedTranscodeCodec(r *http.Request) string {
	if r == nil {
		return transcodeCodecH264
	}
	codec := r.URL.Query().Get("codec")
	if codec == "" {
		codec = r.URL.Query().Get("video_codec")
	}
	return normalizeTranscodeCodec(codec)
}

func requestedFallbackTranscodeProfile(r *http.Request) (string, transcodeProfile, bool) {
	if r == nil {
		return "", transcodeProfile{}, false
	}
	profileID := strings.TrimSpace(r.URL.Query().Get("fallback_profile"))
	profile, ok := transcodeProfiles[profileID]
	if !ok {
		return "", transcodeProfile{}, false
	}
	return profileID, profile, true
}

func truncateProbeReason(reason string) string {
	const maxLen = 600
	reason = strings.Join(strings.Fields(reason), " ")
	if len(reason) <= maxLen {
		return reason
	}
	return reason[:maxLen] + "..."
}

func hasDefaultVAAPIDevice() bool {
	_, err := os.Stat("/dev/dri/renderD128")
	return err == nil
}

type flushWriter struct {
	w http.ResponseWriter
}

func (f flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if flusher, ok := f.w.(http.Flusher); ok {
		flusher.Flush()
	}
	return n, err
}

type trackedResponseWriter struct {
	http.ResponseWriter
	stream        *nzbfilesystem.ActiveStream
	streamTracker *StreamTracker
}

func (w *trackedResponseWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if n > 0 && w.stream != nil {
		atomic.AddInt64(&w.stream.BytesSent, int64(n))
		atomic.AddInt64(&w.stream.CurrentOffset, int64(n))
		if w.streamTracker != nil {
			w.streamTracker.Touch(w.stream.ID)
		}
	}
	return n, err
}

func (w *trackedResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

type limitedBuffer struct {
	buf bytes.Buffer
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	const maxBytes = 32 * 1024
	remaining := maxBytes - b.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = b.buf.Write(p[:remaining])
		} else {
			_, _ = b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return b.buf.String()
}
