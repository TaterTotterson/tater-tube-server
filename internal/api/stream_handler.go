package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"log/slog"
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
	if user != nil {
		if user.Name != nil && *user.Name != "" {
			userName = *user.Name
		} else {
			userName = user.UserID
		}
	}

	// Set stream source and username for tracking
	ctx = context.WithValue(ctx, utils.StreamSourceKey, "API")
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
	if cfg == nil || cfg.Transcoding.Enabled == nil || !*cfg.Transcoding.Enabled {
		return false
	}
	if r.URL.Query().Get("direct") == "1" || r.URL.Query().Get("transcode") == "0" {
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

	ffmpegPath := cfg.Transcoding.FFmpegPath
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
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
		profile = transcodeProfiles["crt_480p"]
	}

	accel := r.URL.Query().Get("hwaccel")
	if accel == "" {
		accel = cfg.Transcoding.HardwareAcceleration
	}
	if accel == "" {
		accel = "none"
	}

	accel, selectedHardwareDevice := h.selectTranscodeAcceleration(r.Context(), ffmpegPath, cfg.Transcoding, profile, accel)
	transcodeCfg := cfg.Transcoding
	if selectedHardwareDevice != "" {
		transcodeCfg.HardwareDevice = selectedHardwareDevice
	}
	args := buildFFmpegTranscodeArgs(transcodeCfg, profile, accel)
	videoCodec, _ := transcodeVideoSettings(accel, transcodeCfg.HardwareDevice, profile)
	effectiveAccel := effectiveTranscodeHardwareAccel(videoCodec)
	hardwareDevice := effectiveTranscodeHardwareDevice(effectiveAccel, transcodeCfg.HardwareDevice)
	h.markTranscodedStream(w, file, profileID, profile.Name, effectiveAccel, hardwareDevice, videoCodec)

	cmd := exec.CommandContext(r.Context(), ffmpegPath, args...)
	cmd.Stdin = file

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
		"video_codec", videoCodec)

	if err := cmd.Run(); err != nil && r.Context().Err() == nil {
		slog.ErrorContext(ctx, "FFmpeg transcode failed",
			"path", path,
			"profile", profileID,
			"hardware_acceleration", effectiveAccel,
			"video_codec", videoCodec,
			"error", err,
			"stderr", stderr.String())
	}
}

func (h *StreamHandler) markTranscodedStream(w http.ResponseWriter, file afero.File, profileID, profileName, hardwareAccel, hardwareDevice, videoCodec string) {
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
}

func buildFFmpegTranscodeArgs(cfg config.TranscodingConfig, profile transcodeProfile, accel string) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-nostdin",
	}

	args = append(args, transcodeHardwareInitArgs(cfg, accel)...)

	args = append(args,
		"-i", "pipe:0",
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

	if requested == "qsv" {
		probeCfg := cfg
		probeCfg.HardwareDevice = ""
		ok, reason := probeTranscodeEncoder(ctx, ffmpegPath, probeCfg, profile, requested)
		if ok {
			return requested, ""
		}
		slog.WarnContext(ctx, "Configured FFmpeg hardware acceleration is not usable",
			"requested", requested,
			"reason", reason)
		return "none", ""
	}

	if strings.TrimSpace(cfg.HardwareDevice) == "" && requested == "vaapi" {
		device, reason, ok := probeTranscodeEncoderDevices(
			ffmpegPath, cfg, profile, requested,
			candidateDRIRenderDevices(detectDRMGPUVendors(), []string{"intel", "amd"}, ""),
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

func transcodeHardwareInitArgs(cfg config.TranscodingConfig, accel string) []string {
	device := strings.TrimSpace(cfg.HardwareDevice)
	if device == "" {
		device = firstDRIRenderDevice()
	}

	switch accel {
	case "vaapi":
		return []string{"-vaapi_device", device}
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
	case "h264_videotoolbox":
		return append(args, "-profile:v", "main", "-allow_sw", "1")
	default:
		return args
	}
}

func transcodeVideoSettings(accel, device string, profile transcodeProfile) (codec string, filters string) {
	scaleFilter := "scale=w=" + strconv.Itoa(profile.MaxWidth) + ":h=" + strconv.Itoa(profile.MaxHeight) + ":force_original_aspect_ratio=decrease:force_divisible_by=2"

	switch accel {
	case "auto":
		if hasDefaultVAAPIDevice() {
			return "h264_vaapi", scaleFilter + ",format=nv12,hwupload"
		}
		return "libx264", scaleFilter
	case "vaapi":
		return "h264_vaapi", scaleFilter + ",format=nv12,hwupload"
	case "qsv":
		return "h264_qsv", scaleFilter
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
	case "h264_vaapi":
		return "vaapi"
	case "h264_qsv":
		return "qsv"
	case "h264_nvenc":
		return "nvenc"
	case "h264_videotoolbox":
		return "videotoolbox"
	case "h264_v4l2m2m":
		return "v4l2m2m"
	default:
		return "none"
	}
}

func effectiveTranscodeHardwareDevice(hardwareAccel, configuredDevice string) string {
	if hardwareAccel == "qsv" {
		return ""
	}
	if configuredDevice != "" {
		return configuredDevice
	}
	switch hardwareAccel {
	case "vaapi":
		return firstDRIRenderDevice()
	}
	return ""
}

func probeTranscodeEncoder(parent context.Context, ffmpegPath string, cfg config.TranscodingConfig, profile transcodeProfile, accel string) (bool, string) {
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()

	args := buildFFmpegTranscodeProbeArgs(cfg, profile, accel)
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

	videoCodec, filters := transcodeVideoSettings(accel, cfg.HardwareDevice, profile)
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
