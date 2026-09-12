package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

var taterToneMapFilterCache sync.Map

const taterInternalTranscodeInputQuery = "tater_internal_transcode_input"

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
		user, ok := h.authenticate(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="Stream API"`)
			http.Error(w, "Unauthorized: valid player_token required", http.StatusUnauthorized)
			return
		}
		if strings.TrimSpace(r.URL.Query().Get(taterLocalHLSSegmentQuery)) != "" {
			playerID := ""
			if user != nil && user.Provider == "tater" {
				playerID = user.UserID
			}
			h.serveStreamHLSSegment(w, r, playerID)
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
	if isTaterInternalTranscodeInputRequest(r) {
		// A resumed NZB transcode reads its seekable source back through this
		// endpoint. It belongs to the outer playback session and must not be
		// presented as a second viewer-facing stream.
		ctx = context.WithValue(ctx, utils.SuppressStreamTrackingKey, true)
	}

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
	if requestedTaterOutputContainer(r) == "hls" && h.shouldTranscode(r, path) {
		h.serveStreamHLSPlaylist(w, r, ctx, path, userName, playerID)
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
			applyTaterRequestedTrackInfo(h.streamTracker, streamID, r)

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

func applyTaterRequestedTrackInfo(tracker *StreamTracker, streamID string, r *http.Request) {
	if tracker == nil || strings.TrimSpace(streamID) == "" || r == nil {
		return
	}
	videoMode := requestedTaterTrackMode(r, "tater_video_mode", "")
	audioMode := requestedTaterTrackMode(r, "tater_audio_mode", "")
	if videoMode == "" && audioMode == "" {
		applyTaterRequestedDynamicRangeInfo(tracker, streamID, r)
		applyTaterRequestedResolutionInfo(tracker, streamID, r)
		return
	}
	if videoMode == "" {
		videoMode = "direct"
	}
	if audioMode == "" {
		audioMode = "direct"
	}
	audioCodec := cleanTaterCodecName(r.URL.Query().Get("tater_audio_codec"))
	status := "Streaming"
	if audioMode == "bitstream" {
		status = "Bitstreaming audio"
	}
	tracker.SetTrackProcessingInfo(streamID, videoMode, audioMode, audioCodec, status)
	applyTaterRequestedDynamicRangeInfo(tracker, streamID, r)
	applyTaterRequestedResolutionInfo(tracker, streamID, r)
}

func applyTaterRequestedDynamicRangeInfo(tracker *StreamTracker, streamID string, r *http.Request) {
	if tracker == nil || strings.TrimSpace(streamID) == "" || r == nil {
		return
	}
	sourceRange := cleanTaterVideoRange(r.URL.Query().Get("tater_source_video_range"))
	outputRange := cleanTaterVideoRange(r.URL.Query().Get("tater_output_video_range"))
	if sourceRange == "" && outputRange == "" {
		return
	}
	if sourceRange == "" {
		sourceRange = "sdr"
	}
	if outputRange == "" {
		outputRange = sourceRange
	}
	tracker.SetDynamicRangeInfo(
		streamID, sourceRange, outputRange,
		strings.TrimSpace(r.URL.Query().Get("tater_tone_map")) == "1",
	)
}

func applyTaterRequestedResolutionInfo(tracker *StreamTracker, streamID string, r *http.Request) {
	if tracker == nil || strings.TrimSpace(streamID) == "" || r == nil {
		return
	}
	parse := func(key string) int {
		value, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get(key)))
		if err != nil || value <= 0 {
			return 0
		}
		return value
	}
	sourceWidth := parse("tater_source_width")
	sourceHeight := parse("tater_source_height")
	outputWidth := parse("tater_output_width")
	outputHeight := parse("tater_output_height")
	if sourceWidth == 0 && sourceHeight == 0 && outputWidth == 0 && outputHeight == 0 {
		return
	}
	tracker.SetVideoResolutionInfo(
		streamID, sourceWidth, sourceHeight, outputWidth, outputHeight,
	)
}

func isTaterInternalTranscodeInputRequest(r *http.Request) bool {
	if r == nil || r.URL == nil || r.URL.Query().Get(taterInternalTranscodeInputQuery) != "1" {
		return false
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func requestedTaterTrackMode(r *http.Request, key, fallback string) string {
	if r == nil {
		return fallback
	}
	mode := cleanTaterCodecName(r.URL.Query().Get(key))
	switch mode {
	case "direct", "transcode", "bitstream", "none":
		return mode
	default:
		return fallback
	}
}

func requestedTaterAudioTrack(r *http.Request) int {
	if r == nil {
		return 0
	}
	track, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("tater_audio_track")))
	if err != nil || track < 0 || track > 255 {
		return 0
	}
	return track
}

func taterAudioMap(track int, optional bool) string {
	if track < 0 {
		track = 0
	}
	suffix := ""
	if optional {
		suffix = "?"
	}
	return fmt.Sprintf("0:a:%d%s", track, suffix)
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
	transcodeCodecH264   = "h264"
	transcodeCodecHEVC   = "hevc"
	audioSyncProfileID   = "audio_sync"
	audioSyncProfileName = "Audio Sync PCM"
	audioOnlyProfileID   = "audio_aac"
	audioOnlyProfileName = "Video Direct / Audio AAC"
	videoOnlyProfileID   = "video_only"
	videoOnlyProfileName = "Video Transcode / Audio Direct"
)

var transcodeProfiles = map[string]transcodeProfile{
	"xbox_480p": {
		Name:         "Original Xbox 480p",
		MaxWidth:     640,
		MaxHeight:    480,
		VideoBitrate: "900k",
		MaxRate:      "1200k",
		BufferSize:   "2400k",
		AudioBitrate: "96k",
		Level:        "3.0",
	},
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
		transcodeValue == "yes" ||
		isRemuxRequest(r) ||
		isAudioOnlyTranscodeRequest(r) ||
		isVideoOnlyTranscodeRequest(r)
	if !forceTranscode {
		return false
	}

	extension := strings.ToLower(filepath.Ext(path))
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("profile")), audioSyncProfileID) {
		switch extension {
		case ".aac", ".aiff", ".alac", ".flac", ".m4a", ".m4b", ".mp3", ".ogg", ".opus", ".wav", ".wma":
			return true
		default:
			return false
		}
	}

	switch extension {
	case ".mkv", ".mp4", ".m4v", ".mov", ".avi", ".ts", ".m2ts", ".mpg", ".mpeg", ".wmv", ".webm":
		return true
	default:
		return false
	}
}

func isAudioOnlyTranscodeRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("transcode"))) {
	case "audio", "audio-only", "audio_only":
		return true
	default:
		return false
	}
}

func isVideoOnlyTranscodeRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("transcode"))) {
	case "video", "video-only", "video_only":
		return true
	default:
		return false
	}
}

func isRemuxRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("transcode")), "remux")
}

func requestedTaterOutputContainer(r *http.Request) string {
	if r == nil {
		return ""
	}
	return cleanTaterPreferredStreamContainer(r.URL.Query().Get("tater_output_container"))
}

func taterPartialTranscodeOutput(container string) (format, contentType, suffix string) {
	if cleanTaterPreferredStreamContainer(container) == "mpegts" {
		return "mpegts", "video/mp2t", ".ts"
	}
	return "matroska", "video/x-matroska", ".mkv"
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
	if isRemuxRequest(r) {
		h.serveRemuxed(w, r, ctx, path, file, ffmpegPath, cfg)
		return
	}
	if isAudioOnlyTranscodeRequest(r) {
		h.serveAudioOnlyVideoTranscoded(w, r, ctx, path, file, ffmpegPath, cfg)
		return
	}
	if isVideoOnlyTranscodeRequest(r) {
		h.serveVideoOnlyTranscoded(w, r, ctx, path, file, ffmpegPath, cfg)
		return
	}

	profileID := r.URL.Query().Get("profile")
	if profileID == "" {
		profileID = cfg.Transcoding.Profile
	}
	if profileID == audioSyncProfileID {
		h.serveAudioSyncTranscoded(w, r, ctx, path, file, ffmpegPath, cfg)
		return
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
		inputPath = taterSeekableTranscodeInput(r, cfg, path)
		if inputPath == "" {
			http.Error(w, "Unable to prepare seekable transcode input", http.StatusServiceUnavailable)
			return
		}
	}
	toneMapSource, toneMapTarget := requestedTaterToneMap(r)
	toneMapFilter := taterToneMapFilterForFFmpeg(r.Context(), ffmpegPath, toneMapSource)
	if toneMapSource != "" && toneMapFilter == "" {
		http.Error(w, "Tone mapping unavailable in the configured FFmpeg build", http.StatusServiceUnavailable)
		return
	}
	args := buildFFmpegTranscodeArgsWithOptions(
		transcodeCfg, profile, accel, videoCodecPreference, transcodeOutputOptions{
			InputPath: inputPath, StartSeconds: startSeconds,
			AudioTrack:    requestedTaterAudioTrack(r),
			ToneMapSource: toneMapSource, ToneMapTarget: toneMapTarget,
			ToneMapFilter: toneMapFilter,
		},
	)
	videoCodec, _ := transcodeVideoSettingsForCodec(accel, transcodeCfg.HardwareDevice, profile, videoCodecPreference)
	effectiveAccel := effectiveTranscodeHardwareAccel(videoCodec)
	hardwareDevice := effectiveTranscodeHardwareDevice(effectiveAccel, transcodeCfg.HardwareDevice)
	durationSeconds := h.probeMediaDuration(ctx, path)
	streamID := h.markTranscodedStream(w, file, profileID, profile.Name, effectiveAccel, hardwareDevice, videoCodec, startSeconds, durationSeconds)
	applyTaterRequestedDynamicRangeInfo(h.streamTracker, streamID, r)
	applyTaterRequestedResolutionInfo(h.streamTracker, streamID, r)

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
	w.Header().Set("X-Tater-Transcode-Profile", profileID)
	w.Header().Set("X-Tater-Video-Mode", "transcode")
	w.Header().Set("X-Tater-Audio-Mode", "transcode")
	w.Header().Set("X-Tater-Audio-Codec", "aac")
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

func (h *StreamHandler) serveRemuxed(
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
	path string,
	file afero.File,
	ffmpegPath string,
	cfg *config.Config,
) {
	if requestedTaterOutputContainer(r) != "mpegts" {
		http.Error(w, "Requested stream container is unavailable", http.StatusBadRequest)
		return
	}

	startSeconds := parseTranscodeStartSeconds(r.URL.Query().Get("start"))
	inputPath := ""
	if startSeconds > 0 {
		inputPath = taterSeekableTranscodeInput(r, cfg, path)
		if inputPath == "" {
			http.Error(w, "Unable to prepare seekable stream input", http.StatusServiceUnavailable)
			return
		}
	}
	args := buildFFmpegRemuxArgs(inputPath, startSeconds, requestedTaterAudioTrack(r))
	durationSeconds := h.probeMediaDuration(ctx, path)
	streamID := streamIDForResponse(w, file)
	if h.streamTracker != nil && streamID != "" {
		h.streamTracker.SetTrackProcessingInfo(
			streamID,
			requestedTaterTrackMode(r, "tater_video_mode", "direct"),
			requestedTaterTrackMode(r, "tater_audio_mode", "direct"),
			cleanTaterCodecName(r.URL.Query().Get("tater_audio_codec")),
			"Remuxing stream",
		)
		if durationSeconds > 0 || startSeconds > 0 {
			h.streamTracker.SetMediaInfo(streamID, durationSeconds, startSeconds)
		}
		applyTaterRequestedDynamicRangeInfo(h.streamTracker, streamID, r)
		applyTaterRequestedResolutionInfo(h.streamTracker, streamID, r)
	}

	cmd := exec.CommandContext(r.Context(), ffmpegPath, args...)
	if inputPath == "" {
		cmd.Stdin = file
	}
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	cmd.Stdout = flushWriter{w: w}

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `inline; filename="`+filepath.Base(path)+`.remux.ts"`)
	w.Header().Set("X-Tater-Transcode-Profile", "mpegts_remux")
	w.Header().Set("X-Tater-Video-Mode", requestedTaterTrackMode(r, "tater_video_mode", "direct"))
	w.Header().Set("X-Tater-Audio-Mode", requestedTaterTrackMode(r, "tater_audio_mode", "direct"))
	w.Header().Set("X-Tater-Audio-Codec", cleanTaterCodecName(r.URL.Query().Get("tater_audio_codec")))
	w.Header().Del("Accept-Ranges")
	w.WriteHeader(http.StatusOK)

	slog.InfoContext(ctx, "Starting FFmpeg MPEG-TS remux stream",
		"path", path,
		"container", "mpegts",
		"start_seconds", startSeconds)

	if err := cmd.Run(); err != nil && r.Context().Err() == nil {
		slog.ErrorContext(ctx, "FFmpeg MPEG-TS remux failed",
			"path", path,
			"start_seconds", startSeconds,
			"error", err,
			"stderr", stderr.String())
	}
}

func (h *StreamHandler) serveAudioOnlyVideoTranscoded(
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
	path string,
	file afero.File,
	ffmpegPath string,
	cfg *config.Config,
) {
	profileID := strings.TrimSpace(r.URL.Query().Get("profile"))
	if profileID == "" {
		profileID = strings.TrimSpace(cfg.Transcoding.Profile)
	}
	profile, ok := transcodeProfiles[profileID]
	if !ok {
		profile = transcodeProfiles["hdmi_1080p"]
	}
	startSeconds := parseTranscodeStartSeconds(r.URL.Query().Get("start"))
	inputPath := ""
	if startSeconds > 0 {
		inputPath = taterSeekableTranscodeInput(r, cfg, path)
		if inputPath == "" {
			http.Error(w, "Unable to prepare seekable transcode input", http.StatusServiceUnavailable)
			return
		}
	}
	outputContainer := requestedTaterOutputContainer(r)
	outputFormat, contentType, suffix := taterPartialTranscodeOutput(outputContainer)
	args := buildFFmpegAudioOnlyVideoArgsWithTrackAndContainer(
		profile.AudioBitrate, inputPath, startSeconds, requestedTaterAudioTrack(r), outputContainer,
	)
	durationSeconds := h.probeMediaDuration(ctx, path)
	streamID := h.markTranscodedStream(
		w, file, audioOnlyProfileID, audioOnlyProfileName,
		"none", "", "copy", startSeconds, durationSeconds,
	)
	if h.streamTracker != nil && streamID != "" {
		h.streamTracker.SetTrackProcessingInfo(
			streamID, "direct", "transcode", "aac", "Transcoding audio",
		)
		applyTaterRequestedDynamicRangeInfo(h.streamTracker, streamID, r)
		applyTaterRequestedResolutionInfo(h.streamTracker, streamID, r)
	}

	cmd := exec.CommandContext(r.Context(), ffmpegPath, args...)
	if inputPath == "" {
		cmd.Stdin = file
	}

	var stderr limitedBuffer
	cmd.Stderr = &stderr
	cmd.Stdout = flushWriter{w: w}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `inline; filename="`+filepath.Base(path)+`.audio-aac`+suffix+`"`)
	w.Header().Set("X-Tater-Transcode-Profile", audioOnlyProfileID)
	w.Header().Set("X-Tater-Video-Mode", "direct")
	w.Header().Set("X-Tater-Audio-Mode", "transcode")
	w.Header().Set("X-Tater-Audio-Codec", "aac")
	w.Header().Del("Accept-Ranges")
	w.WriteHeader(http.StatusOK)

	slog.InfoContext(ctx, "Starting FFmpeg audio-only transcode stream",
		"path", path,
		"container", outputFormat,
		"video_mode", "direct",
		"audio_mode", "transcode",
		"audio_codec", "aac",
		"start_seconds", startSeconds)

	if err := cmd.Run(); err != nil && r.Context().Err() == nil {
		slog.ErrorContext(ctx, "FFmpeg audio-only transcode failed",
			"path", path,
			"start_seconds", startSeconds,
			"error", err,
			"stderr", stderr.String())
	}
}

func (h *StreamHandler) serveVideoOnlyTranscoded(
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
	path string,
	file afero.File,
	ffmpegPath string,
	cfg *config.Config,
) {
	profileID := strings.TrimSpace(r.URL.Query().Get("profile"))
	if profileID == "" {
		profileID = strings.TrimSpace(cfg.Transcoding.Profile)
	}
	profile, ok := transcodeProfiles[profileID]
	if !ok {
		profileID = "hdmi_1080p"
		profile = transcodeProfiles[profileID]
	}
	requestedCodec := requestedTranscodeCodec(r)
	accel := strings.TrimSpace(r.URL.Query().Get("hwaccel"))
	if accel == "" {
		accel = strings.TrimSpace(cfg.Transcoding.HardwareAcceleration)
	}
	if accel == "" {
		accel = "none"
	}

	accel, selectedHardwareDevice, videoCodecPreference := h.selectTranscodeAccelerationAndCodec(
		r.Context(), ffmpegPath, cfg.Transcoding, profile, accel, requestedCodec,
	)
	if requestedCodec == transcodeCodecHEVC && videoCodecPreference != transcodeCodecHEVC {
		if fallbackID, fallbackProfile, found := requestedFallbackTranscodeProfile(r); found {
			profileID = fallbackID
			profile = fallbackProfile
			accel, selectedHardwareDevice = h.selectTranscodeAcceleration(
				r.Context(), ffmpegPath, cfg.Transcoding, profile, accel,
			)
		}
	}
	transcodeCfg := cfg.Transcoding
	if selectedHardwareDevice != "" {
		transcodeCfg.HardwareDevice = selectedHardwareDevice
	}
	startSeconds := parseTranscodeStartSeconds(r.URL.Query().Get("start"))
	inputPath := ""
	if startSeconds > 0 {
		inputPath = taterSeekableTranscodeInput(r, cfg, path)
		if inputPath == "" {
			http.Error(w, "Unable to prepare seekable transcode input", http.StatusServiceUnavailable)
			return
		}
	}
	toneMapSource, toneMapTarget := requestedTaterToneMap(r)
	toneMapFilter := taterToneMapFilterForFFmpeg(r.Context(), ffmpegPath, toneMapSource)
	if toneMapSource != "" && toneMapFilter == "" {
		http.Error(w, "Tone mapping unavailable in the configured FFmpeg build", http.StatusServiceUnavailable)
		return
	}
	outputContainer := requestedTaterOutputContainer(r)
	outputFormat, contentType, suffix := taterPartialTranscodeOutput(outputContainer)
	args := buildFFmpegVideoOnlyArgsWithToneMapFilterAndAudioTrackAndContainer(
		transcodeCfg, profile, accel, videoCodecPreference, inputPath, startSeconds,
		toneMapSource, toneMapTarget, toneMapFilter, requestedTaterAudioTrack(r), outputContainer,
	)
	videoCodec, _ := transcodeVideoSettingsForCodec(
		accel, transcodeCfg.HardwareDevice, profile, videoCodecPreference,
	)
	effectiveAccel := effectiveTranscodeHardwareAccel(videoCodec)
	hardwareDevice := effectiveTranscodeHardwareDevice(
		effectiveAccel, transcodeCfg.HardwareDevice,
	)
	durationSeconds := h.probeMediaDuration(ctx, path)
	streamID := h.markTranscodedStream(
		w, file, videoOnlyProfileID, videoOnlyProfileName,
		effectiveAccel, hardwareDevice, videoCodec, startSeconds, durationSeconds,
	)
	audioCodec := cleanTaterCodecName(r.URL.Query().Get("audio_codec"))
	if audioCodec == "" {
		audioCodec = "copy"
	}
	audioMode := requestedTaterTrackMode(r, "tater_audio_mode", "direct")
	if h.streamTracker != nil && streamID != "" {
		h.streamTracker.SetTrackProcessingInfo(
			streamID, "transcode", audioMode, audioCodec, "Transcoding video",
		)
		applyTaterRequestedDynamicRangeInfo(h.streamTracker, streamID, r)
		applyTaterRequestedResolutionInfo(h.streamTracker, streamID, r)
	}

	cmd := exec.CommandContext(r.Context(), ffmpegPath, args...)
	if inputPath == "" {
		cmd.Stdin = file
	}

	var stderr limitedBuffer
	cmd.Stderr = &stderr
	cmd.Stdout = flushWriter{w: w}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `inline; filename="`+filepath.Base(path)+`.video`+suffix+`"`)
	w.Header().Set("X-Tater-Transcode-Profile", videoOnlyProfileID)
	w.Header().Set("X-Tater-Video-Mode", "transcode")
	w.Header().Set("X-Tater-Audio-Mode", audioMode)
	w.Header().Set("X-Tater-Audio-Codec", audioCodec)
	w.Header().Del("Accept-Ranges")
	w.WriteHeader(http.StatusOK)

	slog.InfoContext(ctx, "Starting FFmpeg video-only transcode stream",
		"path", path,
		"container", outputFormat,
		"profile", profileID,
		"profile_name", profile.Name,
		"video_mode", "transcode",
		"video_codec", videoCodec,
		"audio_mode", audioMode,
		"audio_codec", audioCodec,
		"hardware_acceleration", effectiveAccel,
		"start_seconds", startSeconds)

	if err := cmd.Run(); err != nil && r.Context().Err() == nil {
		slog.ErrorContext(ctx, "FFmpeg video-only transcode failed",
			"path", path,
			"profile", profileID,
			"video_codec", videoCodec,
			"audio_codec", audioCodec,
			"start_seconds", startSeconds,
			"error", err,
			"stderr", stderr.String())
	}
}

func (h *StreamHandler) serveAudioSyncTranscoded(
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
	path string,
	file afero.File,
	ffmpegPath string,
	cfg *config.Config,
) {
	startSeconds := parseTranscodeStartSeconds(r.URL.Query().Get("start"))
	inputPath := ""
	if startSeconds > 0 {
		inputPath = taterSeekableTranscodeInput(r, cfg, path)
		if inputPath == "" {
			http.Error(w, "Unable to prepare seekable transcode input", http.StatusServiceUnavailable)
			return
		}
	}
	args := buildFFmpegAudioSyncArgs(inputPath, startSeconds)
	durationSeconds := h.probeMediaDuration(ctx, path)
	streamID := h.markTranscodedStream(
		w,
		file,
		audioSyncProfileID,
		audioSyncProfileName,
		"none",
		"",
		"pcm_s16le",
		startSeconds,
		durationSeconds,
	)
	if h.streamTracker != nil && streamID != "" {
		h.streamTracker.SetTrackProcessingInfo(
			streamID, "none", "transcode", "pcm_s16le", "Transcoding audio",
		)
	}

	cmd := exec.CommandContext(r.Context(), ffmpegPath, args...)
	if inputPath == "" {
		cmd.Stdin = file
	}

	var stderr limitedBuffer
	cmd.Stderr = &stderr
	cmd.Stdout = flushWriter{w: w}

	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `inline; filename="`+filepath.Base(path)+`.sync.wav"`)
	w.Header().Set("X-Tater-Transcode-Profile", audioSyncProfileID)
	w.Header().Set("X-Tater-Video-Mode", "none")
	w.Header().Set("X-Tater-Audio-Mode", "transcode")
	w.Header().Set("X-Tater-Audio-Codec", "pcm_s16le")
	w.Header().Del("Accept-Ranges")
	w.WriteHeader(http.StatusOK)

	slog.InfoContext(ctx, "Starting FFmpeg audio sync transcode stream",
		"path", path,
		"profile", audioSyncProfileID,
		"sample_rate", 48000,
		"channels", 2,
		"start_seconds", startSeconds)

	if err := cmd.Run(); err != nil && r.Context().Err() == nil {
		slog.ErrorContext(ctx, "FFmpeg audio sync transcode failed",
			"path", path,
			"profile", audioSyncProfileID,
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

// taterSeekableTranscodeInput returns the seekable source FFmpeg should open
// for a resumed transcode. Local-library handlers already resolved path to an
// operating-system file and must keep using it. Only the NZB virtual stream
// needs to be reconstructed as a loopback HTTP range request.
func taterSeekableTranscodeInput(r *http.Request, cfg *config.Config, path string) string {
	if r == nil || r.URL == nil || r.URL.Path != "/api/files/stream" {
		return strings.TrimSpace(path)
	}
	return taterSeekableVirtualInputURL(r, cfg)
}

// taterSeekableVirtualInputURL gives FFmpeg a seekable view of an NZB virtual
// file. The path accepted by /api/files/stream is not an operating-system path,
// so passing it directly to FFmpeg works at start=0 (through stdin) but fails
// when a resumed transcode asks FFmpeg to seek it by filename. Looping back
// through the non-transcoding stream endpoint preserves HTTP byte ranges and
// lets FFmpeg perform an efficient input seek.
func taterSeekableVirtualInputURL(r *http.Request, cfg *config.Config) string {
	if r == nil || cfg == nil || cfg.Server.Port <= 0 {
		return ""
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		return ""
	}

	query := url.Values{}
	query.Set("path", path)
	query.Set(taterInternalTranscodeInputQuery, "1")
	playerToken := strings.TrimSpace(r.URL.Query().Get("player_token"))
	if playerToken == "" {
		playerToken = bearerToken(r.Header.Get("Authorization"))
	}
	if playerToken == "" {
		playerToken = strings.TrimSpace(r.Header.Get("X-Tater-Player-Token"))
	}
	if playerToken != "" {
		query.Set("player_token", playerToken)
	} else if downloadKey := strings.TrimSpace(r.URL.Query().Get("download_key")); downloadKey != "" {
		query.Set("download_key", downloadKey)
	}

	return (&url.URL{
		Scheme:   "http",
		Host:     "127.0.0.1:" + strconv.Itoa(cfg.Server.Port),
		Path:     "/api/files/stream",
		RawQuery: query.Encode(),
	}).String()
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

func (h *StreamHandler) markTranscodedStream(w http.ResponseWriter, file afero.File, profileID, profileName, hardwareAccel, hardwareDevice, videoCodec string, playbackStartSeconds, durationSeconds float64) string {
	if h.streamTracker == nil {
		return ""
	}

	streamID := streamIDForResponse(w, file)
	if streamID == "" {
		return ""
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
	return streamID
}

func streamIDForResponse(w http.ResponseWriter, file afero.File) string {
	if tracked, ok := w.(*trackedResponseWriter); ok && tracked.stream != nil {
		return tracked.stream.ID
	} else if mvf, ok := file.(*nzbfilesystem.MetadataVirtualFile); ok {
		return mvf.GetStreamID()
	}
	return ""
}

func buildFFmpegTranscodeArgs(cfg config.TranscodingConfig, profile transcodeProfile, accel string, inputPath string, startSeconds float64) []string {
	return buildFFmpegTranscodeArgsWithCodec(cfg, profile, accel, transcodeCodecH264, inputPath, startSeconds)
}

func buildFFmpegAudioSyncArgs(inputPath string, startSeconds float64) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-nostdin",
	}
	if strings.TrimSpace(inputPath) != "" {
		if startSeconds > 0 {
			args = append(args, "-ss", strconv.FormatFloat(startSeconds, 'f', 3, 64))
		}
		args = append(args, "-i", inputPath)
	} else {
		args = append(args, "-i", "pipe:0")
	}
	args = append(args,
		"-map", "0:a:0",
		"-vn",
		"-sn",
		"-dn",
		"-map_metadata", "-1",
		"-af", "aresample=48000:async=0:first_pts=0",
		"-c:a", "pcm_s16le",
		"-ac", "2",
		"-ar", "48000",
		"-fflags", "+genpts",
		"-f", "wav",
		"pipe:1",
	)
	return args
}

func buildFFmpegAudioOnlyVideoArgs(audioBitrate, inputPath string, startSeconds float64) []string {
	return buildFFmpegAudioOnlyVideoArgsWithTrack(audioBitrate, inputPath, startSeconds, 0)
}

func buildFFmpegAudioOnlyVideoArgsWithTrack(audioBitrate, inputPath string, startSeconds float64, audioTrack int) []string {
	return buildFFmpegAudioOnlyVideoArgsWithTrackAndContainer(
		audioBitrate, inputPath, startSeconds, audioTrack, "",
	)
}

func buildFFmpegAudioOnlyVideoArgsWithTrackAndContainer(audioBitrate, inputPath string, startSeconds float64, audioTrack int, outputContainer string) []string {
	if strings.TrimSpace(audioBitrate) == "" {
		audioBitrate = "192k"
	}
	outputFormat, _, _ := taterPartialTranscodeOutput(outputContainer)
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-nostdin",
	}
	if strings.TrimSpace(inputPath) != "" {
		if startSeconds > 0 {
			// Keep copied video and transcoded audio on the same seek point. With
			// accurate seeking FFmpeg retains the video keyframe pre-roll but
			// discards the matching transcoded audio, creating a silent lead-in.
			args = append(args,
				"-noaccurate_seek",
				"-ss", strconv.FormatFloat(startSeconds, 'f', 3, 64),
			)
		}
		args = append(args, "-i", inputPath)
	} else {
		args = append(args, "-i", "pipe:0")
	}
	args = append(args,
		"-map", "0:v:0",
		"-map", taterAudioMap(audioTrack, true),
		"-sn",
		"-dn",
		"-c:v", "copy",
		"-c:a", "aac",
		"-b:a", audioBitrate,
		"-ac", "2",
		"-ar", "48000",
		"-fflags", "+genpts",
		"-f", outputFormat,
		"pipe:1",
	)
	return args
}

func buildFFmpegRemuxArgs(inputPath string, startSeconds float64, audioTrack int) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-nostdin",
	}
	if strings.TrimSpace(inputPath) != "" {
		if startSeconds > 0 {
			args = append(args,
				"-noaccurate_seek",
				"-ss", strconv.FormatFloat(startSeconds, 'f', 3, 64),
			)
		}
		args = append(args, "-i", inputPath)
	} else {
		args = append(args, "-i", "pipe:0")
	}
	return append(args,
		"-map", "0:v:0",
		"-map", taterAudioMap(audioTrack, true),
		"-sn",
		"-dn",
		"-c:v", "copy",
		"-c:a", "copy",
		"-fflags", "+genpts",
		"-avoid_negative_ts", "make_zero",
		"-muxdelay", "0",
		"-muxpreload", "0",
		"-f", "mpegts",
		"pipe:1",
	)
}

func buildFFmpegVideoOnlyArgs(cfg config.TranscodingConfig, profile transcodeProfile, accel, preferredCodec, inputPath string, startSeconds float64) []string {
	return buildFFmpegVideoOnlyArgsWithToneMap(
		cfg, profile, accel, preferredCodec, inputPath, startSeconds, "", "",
	)
}

func buildFFmpegVideoOnlyArgsWithToneMap(cfg config.TranscodingConfig, profile transcodeProfile, accel, preferredCodec, inputPath string, startSeconds float64, toneMapSource, toneMapTarget string) []string {
	return buildFFmpegVideoOnlyArgsWithToneMapFilter(
		cfg, profile, accel, preferredCodec, inputPath, startSeconds,
		toneMapSource, toneMapTarget, "tonemapx",
	)
}

func buildFFmpegVideoOnlyArgsWithToneMapFilter(cfg config.TranscodingConfig, profile transcodeProfile, accel, preferredCodec, inputPath string, startSeconds float64, toneMapSource, toneMapTarget, toneMapFilter string) []string {
	return buildFFmpegVideoOnlyArgsWithToneMapFilterAndAudioTrack(
		cfg, profile, accel, preferredCodec, inputPath, startSeconds,
		toneMapSource, toneMapTarget, toneMapFilter, 0,
	)
}

func buildFFmpegVideoOnlyArgsWithToneMapFilterAndAudioTrack(cfg config.TranscodingConfig, profile transcodeProfile, accel, preferredCodec, inputPath string, startSeconds float64, toneMapSource, toneMapTarget, toneMapFilter string, audioTrack int) []string {
	return buildFFmpegVideoOnlyArgsWithToneMapFilterAndAudioTrackAndContainer(
		cfg, profile, accel, preferredCodec, inputPath, startSeconds,
		toneMapSource, toneMapTarget, toneMapFilter, audioTrack, "",
	)
}

func buildFFmpegVideoOnlyArgsWithToneMapFilterAndAudioTrackAndContainer(cfg config.TranscodingConfig, profile transcodeProfile, accel, preferredCodec, inputPath string, startSeconds float64, toneMapSource, toneMapTarget, toneMapFilter string, audioTrack int, outputContainer string) []string {
	outputFormat, _, _ := taterPartialTranscodeOutput(outputContainer)
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-nostdin",
	}
	args = append(args, transcodeHardwareInitArgs(cfg, accel)...)
	if strings.TrimSpace(inputPath) != "" {
		if startSeconds > 0 {
			args = append(args, "-ss", strconv.FormatFloat(startSeconds, 'f', 3, 64))
		}
		args = append(args, "-i", inputPath)
	} else {
		args = append(args, "-i", "pipe:0")
	}

	videoCodec, filters := transcodeVideoSettingsForCodec(
		accel, cfg.HardwareDevice, profile, preferredCodec,
	)
	filters = appendTaterToneMapFilter(filters, toneMapSource, toneMapTarget, toneMapFilter)
	args = append(args,
		"-map", "0:v:0",
		"-map", taterAudioMap(audioTrack, true),
		"-sn",
		"-dn",
	)
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
	args = appendTaterToneMapOutputMetadata(args, toneMapSource, toneMapTarget)
	args = append(args,
		"-c:a", "copy",
		"-fflags", "+genpts",
		"-f", outputFormat,
		"pipe:1",
	)
	return args
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
	AudioTrack      int
	LogoFile        string
	LogoPosition    string
	ToneMapSource   string
	ToneMapTarget   string
	ToneMapFilter   string
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
	filters = appendTaterToneMapFilter(
		filters, options.ToneMapSource, options.ToneMapTarget, options.ToneMapFilter,
	)
	if logoFile != "" {
		args = append(args,
			"-filter_complex", taterTVChannelLogoFilter(filters, profile, options.LogoPosition),
			"-map", "[vout]",
			"-map", taterAudioMap(options.AudioTrack, true),
			"-sn",
		)
	} else {
		args = append(args,
			"-map", "0:v:0",
			"-map", taterAudioMap(options.AudioTrack, true),
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
	args = appendTaterToneMapOutputMetadata(args, options.ToneMapSource, options.ToneMapTarget)

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

func requestedTaterToneMap(r *http.Request) (string, string) {
	if r == nil || strings.TrimSpace(r.URL.Query().Get("tater_tone_map")) != "1" {
		return "", ""
	}
	source := cleanTaterVideoRange(r.URL.Query().Get("tater_source_video_range"))
	target := cleanTaterVideoRange(r.URL.Query().Get("tater_output_video_range"))
	if source == "" || source == "sdr" || target != "sdr" {
		return "", ""
	}
	return source, target
}

func appendTaterToneMapFilter(filters, sourceRange, targetRange, implementation string) string {
	sourceRange = cleanTaterVideoRange(sourceRange)
	targetRange = cleanTaterVideoRange(targetRange)
	if sourceRange == "" || sourceRange == "sdr" || targetRange != "sdr" {
		return filters
	}
	var toneMap string
	switch strings.ToLower(strings.TrimSpace(implementation)) {
	case "tonemapx":
		// Jellyfin FFmpeg's SIMD implementation handles HDR10, HDR10+, HLG and
		// Dolby Vision metadata and removes HDR side data from the SDR result.
		toneMap = "tonemapx=tonemap=bt2390:desat=0:peak=100:transfer=bt709:matrix=bt709:primaries=bt709:range=tv:format=yuv420p"
	case "zscale":
		// Portable fallback for standard FFmpeg builds compiled with libzimg.
		toneMap = "zscale=t=linear:npl=100,format=gbrpf32le,tonemap=tonemap=mobius:desat=0,zscale=p=bt709:t=bt709:m=bt709:r=tv,format=yuv420p"
	default:
		return filters
	}
	if strings.TrimSpace(filters) == "" {
		return toneMap
	}
	return toneMap + "," + filters
}

func taterToneMapFilterForFFmpeg(parent context.Context, ffmpegPath, sourceRange string) string {
	if cleanTaterVideoRange(sourceRange) == "" || cleanTaterVideoRange(sourceRange) == "sdr" {
		return ""
	}
	key := strings.TrimSpace(ffmpegPath)
	if cached, ok := taterToneMapFilterCache.Load(key); ok {
		return cached.(string)
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffmpegPath, "-hide_banner", "-filters").CombinedOutput()
	selected := ""
	if err == nil {
		filters := string(out)
		switch {
		case strings.Contains(filters, " tonemapx "):
			selected = "tonemapx"
		case strings.Contains(filters, " zscale ") && strings.Contains(filters, " tonemap "):
			selected = "zscale"
		}
	}
	taterToneMapFilterCache.Store(key, selected)
	return selected
}

func appendTaterToneMapOutputMetadata(args []string, sourceRange, targetRange string) []string {
	if cleanTaterVideoRange(sourceRange) == "" ||
		cleanTaterVideoRange(sourceRange) == "sdr" ||
		cleanTaterVideoRange(targetRange) != "sdr" {
		return args
	}
	return append(args,
		"-color_primaries", "bt709",
		"-color_trc", "bt709",
		"-colorspace", "bt709",
		"-color_range", "tv",
	)
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
