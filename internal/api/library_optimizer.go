package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
)

const (
	libraryOptimizerStateSchema = 1
	libraryOptimizerAutoDelay   = 5 * time.Minute
	libraryOptimizerPollDelay   = 2 * time.Second
)

type libraryOptimizerEncoderCapability struct {
	Codec     string `json:"codec"`
	Label     string `json:"label"`
	Encoder   string `json:"encoder"`
	Hardware  string `json:"hardware"`
	Available bool   `json:"available"`
	Status    string `json:"status"`
	Details   string `json:"details,omitempty"`
}

type libraryOptimizerCapabilities struct {
	FFmpegPath          string                              `json:"ffmpeg_path"`
	FFmpegAvailable     bool                                `json:"ffmpeg_available"`
	FFprobeAvailable    bool                                `json:"ffprobe_available"`
	RecommendedHardware string                              `json:"recommended_hardware"`
	RecommendedHEVC     string                              `json:"recommended_hevc,omitempty"`
	RecommendedAV1      string                              `json:"recommended_av1,omitempty"`
	HEVC                []libraryOptimizerEncoderCapability `json:"hevc"`
	AV1                 []libraryOptimizerEncoderCapability `json:"av1"`
	AudioCopy           bool                                `json:"audio_copy"`
	EAC3                bool                                `json:"eac3"`
	AAC                 bool                                `json:"aac"`
	LibraryWritable     bool                                `json:"library_writable"`
	WritableLibraries   int                                 `json:"writable_libraries"`
	TotalLibraries      int                                 `json:"total_libraries"`
	HDRPreservation     bool                                `json:"hdr_preservation"`
	DolbyVisionPolicy   string                              `json:"dolby_vision_policy"`
	Notes               []string                            `json:"notes,omitempty"`
}

type libraryOptimizerAnalysis struct {
	GeneratedAt        time.Time      `json:"generated_at"`
	TotalFiles         int            `json:"total_files"`
	EligibleFiles      int            `json:"eligible_files"`
	VideoConversions   int            `json:"video_conversions"`
	TrackCleanupOnly   int            `json:"track_cleanup_only"`
	AlreadyEfficient   int            `json:"already_efficient"`
	PreviouslyHandled  int            `json:"previously_handled"`
	DolbyVisionSkipped int            `json:"dolby_vision_skipped"`
	UnavailableFiles   int            `json:"unavailable_files"`
	SourceBytes        int64          `json:"source_bytes"`
	EligibleBytes      int64          `json:"eligible_bytes"`
	Codecs             map[string]int `json:"codecs"`
}

type libraryOptimizerStatus struct {
	State           string                    `json:"state"`
	Message         string                    `json:"message,omitempty"`
	Running         bool                      `json:"running"`
	Paused          bool                      `json:"paused"`
	WaitingPlayback bool                      `json:"waiting_for_playback"`
	StartedAt       time.Time                 `json:"started_at,omitempty"`
	FinishedAt      time.Time                 `json:"finished_at,omitempty"`
	CurrentFile     string                    `json:"current_file,omitempty"`
	Current         int                       `json:"current"`
	Total           int                       `json:"total"`
	Completed       int                       `json:"completed"`
	Skipped         int                       `json:"skipped"`
	Failed          int                       `json:"failed"`
	OriginalBytes   int64                     `json:"original_bytes"`
	OutputBytes     int64                     `json:"output_bytes"`
	SavedBytes      int64                     `json:"saved_bytes"`
	LastError       string                    `json:"last_error,omitempty"`
	Analysis        *libraryOptimizerAnalysis `json:"analysis,omitempty"`
}

type libraryOptimizerRecord struct {
	Path          string    `json:"path"`
	Fingerprint   string    `json:"fingerprint"`
	Status        string    `json:"status"`
	Action        string    `json:"action"`
	SourceCodec   string    `json:"source_codec,omitempty"`
	TargetCodec   string    `json:"target_codec,omitempty"`
	OriginalBytes int64     `json:"original_bytes,omitempty"`
	OutputBytes   int64     `json:"output_bytes,omitempty"`
	SavedBytes    int64     `json:"saved_bytes,omitempty"`
	Message       string    `json:"message,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type libraryOptimizerStore struct {
	Schema  int                               `json:"schema"`
	Records map[string]libraryOptimizerRecord `json:"records"`
}

type libraryOptimizerCandidate struct {
	File         taterLocalLibraryFileIndex
	AbsolutePath string
	Action       string
	Fingerprint  string
}

type libraryOptimizerService struct {
	server               *Server
	mu                   sync.RWMutex
	status               libraryOptimizerStatus
	cancel               context.CancelFunc
	lastCleanup          time.Time
	automaticFingerprint string
}

type optimizerProbe struct {
	Streams []optimizerProbeStream `json:"streams"`
	Format  optimizerProbeFormat   `json:"format"`
}

type optimizerProbeStream struct {
	Index          int                  `json:"index"`
	CodecName      string               `json:"codec_name"`
	CodecType      string               `json:"codec_type"`
	Profile        string               `json:"profile"`
	PixelFormat    string               `json:"pix_fmt"`
	Width          int                  `json:"width"`
	Height         int                  `json:"height"`
	Channels       int                  `json:"channels"`
	ChannelLayout  string               `json:"channel_layout"`
	ColorSpace     string               `json:"color_space"`
	ColorTransfer  string               `json:"color_transfer"`
	ColorPrimaries string               `json:"color_primaries"`
	Tags           map[string]string    `json:"tags"`
	Disposition    optimizerDisposition `json:"disposition"`
	SideData       []map[string]any     `json:"side_data_list"`
}

type optimizerDisposition struct {
	Default         int `json:"default"`
	Forced          int `json:"forced"`
	Original        int `json:"original"`
	Comment         int `json:"comment"`
	HearingImpaired int `json:"hearing_impaired"`
	VisualImpaired  int `json:"visual_impaired"`
	AttachedPic     int `json:"attached_pic"`
}

type optimizerProbeFormat struct {
	Duration string `json:"duration"`
	Size     string `json:"size"`
}

func newLibraryOptimizerService(server *Server) *libraryOptimizerService {
	return &libraryOptimizerService{
		server: server,
		status: libraryOptimizerStatus{State: "idle", Message: "Ready to analyze the local library"},
	}
}

func libraryOptimizerStatePath(cfg *config.Config) string {
	return filepath.Join(taterLocalLibraryDataDir(cfg), "optimizer-state.json")
}

func readLibraryOptimizerStore(cfg *config.Config) libraryOptimizerStore {
	store := libraryOptimizerStore{Schema: libraryOptimizerStateSchema, Records: map[string]libraryOptimizerRecord{}}
	raw, err := os.ReadFile(libraryOptimizerStatePath(cfg))
	if err != nil {
		return store
	}
	if json.Unmarshal(raw, &store) != nil || store.Records == nil {
		return libraryOptimizerStore{Schema: libraryOptimizerStateSchema, Records: map[string]libraryOptimizerRecord{}}
	}
	store.Schema = libraryOptimizerStateSchema
	return store
}

func writeLibraryOptimizerStore(cfg *config.Config, store libraryOptimizerStore) error {
	store.Schema = libraryOptimizerStateSchema
	if store.Records == nil {
		store.Records = map[string]libraryOptimizerRecord{}
	}
	return writeTaterJSON(libraryOptimizerStatePath(cfg), store)
}

func boolValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func intValue(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func probeOptimizerEncoder(ffmpegPath string, cfg *config.Config, encoder, hardware, device string) (bool, string) {
	probeCfg := cfg.Transcoding
	if strings.TrimSpace(device) != "" {
		probeCfg.HardwareDevice = device
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	args = append(args, transcodeHardwareInitArgs(probeCfg, hardware)...)
	args = append(args, "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=1", "-frames:v", "1")
	switch hardware {
	case "vaapi":
		args = append(args, "-vf", "format=nv12,hwupload")
	case "qsv", "videotoolbox":
		args = append(args, "-vf", "format=nv12")
	case "nvenc":
		args = append(args, "-vf", "format=yuv420p")
	}
	args = append(args, "-an", "-c:v", encoder)
	switch encoder {
	case "libx265":
		args = append(args, "-preset", "ultrafast")
	case "libsvtav1":
		args = append(args, "-preset", "12")
	}
	args = append(args, "-f", "null", "-")
	output, err := exec.CommandContext(ctx, ffmpegPath, args...).CombinedOutput()
	if ctx.Err() != nil {
		return false, "Encoder probe timed out"
	}
	if err != nil {
		return false, compactOptimizerError(output, err)
	}
	return true, ""
}

func optimizerEncoderOptions(codec, encoders string, hardware transcodeHardwareDetection, cfg *config.Config) []libraryOptimizerEncoderCapability {
	type encoderDef struct {
		name, label, hw string
	}
	defs := []encoderDef{}
	if codec == "hevc" {
		defs = []encoderDef{
			{"hevc_nvenc", "NVIDIA NVENC", "nvenc"},
			{"hevc_qsv", "Intel Quick Sync", "qsv"},
			{"hevc_vaapi", "AMD / Intel VAAPI", "vaapi"},
			{"hevc_videotoolbox", "Apple VideoToolbox", "videotoolbox"},
			{"libx265", "Software x265", "software"},
		}
	} else {
		defs = []encoderDef{
			{"av1_nvenc", "NVIDIA NVENC", "nvenc"},
			{"av1_qsv", "Intel Quick Sync", "qsv"},
			{"av1_vaapi", "AMD / Intel VAAPI", "vaapi"},
			{"av1_videotoolbox", "Apple VideoToolbox", "videotoolbox"},
			{"libsvtav1", "Software SVT-AV1", "software"},
		}
	}
	hardwareReady := map[string]transcodeHardwareOption{}
	for _, option := range hardware.Options {
		hardwareReady[option.ID] = option
	}
	options := make([]libraryOptimizerEncoderCapability, 0, len(defs))
	for _, def := range defs {
		option := libraryOptimizerEncoderCapability{Codec: codec, Label: def.label, Encoder: def.name, Hardware: def.hw}
		if !hasFFmpegEncoder(encoders, def.name) {
			option.Status = "Encoder is not included in this FFmpeg build"
		} else if def.hw == "software" {
			option.Available = true
			option.Status = "Ready"
			option.Details = "CPU encoding is available but is slower than supported hardware."
		} else if detected, ok := hardwareReady[def.hw]; !ok || !detected.Available {
			option.Status = "Hardware is not ready"
			if ok {
				option.Details = strings.TrimSpace(strings.Join([]string{detected.Status, detected.Details}, ". "))
			}
		} else if ok, reason := probeOptimizerEncoder(effectiveFFmpegPath(cfg.Transcoding.FFmpegPath), cfg, def.name, def.hw, detected.Device); !ok {
			option.Status = "Encoder probe failed"
			option.Details = reason
		} else {
			option.Available = true
			option.Status = "Ready"
			option.Details = detected.Details
		}
		if option.Available && def.hw == "software" {
			if ok, reason := probeOptimizerEncoder(effectiveFFmpegPath(cfg.Transcoding.FFmpegPath), cfg, def.name, def.hw, ""); !ok {
				option.Available = false
				option.Status = "Encoder probe failed"
				option.Details = reason
			}
		}
		options = append(options, option)
	}
	return options
}

func recommendedOptimizerEncoder(options []libraryOptimizerEncoderCapability, preferred string) string {
	preferred = strings.TrimSpace(preferred)
	if preferred == "none" {
		preferred = "software"
	}
	for _, option := range options {
		if option.Available && option.Hardware == preferred {
			return option.Encoder
		}
	}
	for _, hw := range []string{"videotoolbox", "nvenc", "qsv", "vaapi", "software"} {
		for _, option := range options {
			if option.Available && option.Hardware == hw {
				return option.Encoder
			}
		}
	}
	return ""
}

func detectLibraryOptimizerCapabilities(cfg *config.Config) libraryOptimizerCapabilities {
	result := libraryOptimizerCapabilities{DolbyVisionPolicy: "Skipped by default to protect Dolby Vision metadata"}
	if cfg == nil {
		result.Notes = []string{"Configuration is unavailable."}
		return result
	}
	result.FFmpegPath = effectiveFFmpegPath(cfg.Transcoding.FFmpegPath)
	_, ffmpegErr := exec.LookPath(result.FFmpegPath)
	ffprobePath := effectiveFFprobePath(result.FFmpegPath)
	result.FFmpegAvailable = ffmpegErr == nil
	result.FFprobeAvailable = ffprobePath != ""
	if !result.FFmpegAvailable {
		result.Notes = append(result.Notes, "FFmpeg must be installed before library optimization can run.")
		return result
	}
	encoders := ffmpegOutput(result.FFmpegPath, "-hide_banner", "-encoders")
	hardware := detectTranscodingHardware(cfg.Transcoding)
	preferred := strings.TrimSpace(cfg.Transcoding.HardwareAcceleration)
	if preferred == "" || preferred == "auto" {
		preferred = hardware.Recommended
	}
	result.RecommendedHardware = preferred
	result.HEVC = optimizerEncoderOptions("hevc", encoders, hardware, cfg)
	result.AV1 = optimizerEncoderOptions("av1", encoders, hardware, cfg)
	result.RecommendedHEVC = recommendedOptimizerEncoder(result.HEVC, preferred)
	result.RecommendedAV1 = recommendedOptimizerEncoder(result.AV1, preferred)
	result.AudioCopy = true
	result.EAC3 = hasFFmpegEncoder(encoders, "eac3")
	result.AAC = hasFFmpegEncoder(encoders, "aac")
	result.HDRPreservation = result.RecommendedHEVC != "" || result.RecommendedAV1 != ""
	seenRoots := map[string]struct{}{}
	for _, category := range cfg.LocalMedia.Categories {
		if !taterLocalLibraryEnabled(category) || strings.EqualFold(category.LibraryType, "music") {
			continue
		}
		for _, root := range taterLocalMediaCategoryPaths(category) {
			root = filepath.Clean(root)
			if _, seen := seenRoots[root]; seen {
				continue
			}
			seenRoots[root] = struct{}{}
			result.TotalLibraries++
			probe, err := os.CreateTemp(root, ".tater-write-check-*")
			if err == nil {
				name := probe.Name()
				_ = probe.Close()
				_ = os.Remove(name)
				result.WritableLibraries++
			}
		}
	}
	result.LibraryWritable = result.TotalLibraries > 0 && result.WritableLibraries == result.TotalLibraries
	if result.TotalLibraries == 0 {
		result.Notes = append(result.Notes, "Add an enabled movie or series library before optimizing media.")
	} else if !result.LibraryWritable {
		result.Notes = append(result.Notes, "One or more library mounts are read-only. Docker library mounts must use rw access.")
	}
	result.Notes = append(result.Notes,
		"VC-1 and other unsupported hardware decoders use software decoding while the selected hardware encoder remains active.",
		"Working files and optional quarantine copies stay beside the original media; /config stores only compact history.",
	)
	return result
}

func (s *Server) handleDetectLibraryOptimizerCapabilities(c *fiber.Ctx) error {
	if s.configManager == nil || s.configManager.GetConfig() == nil {
		return RespondServiceUnavailable(c, "Configuration management not available", "CONFIG_UNAVAILABLE")
	}
	return RespondSuccess(c, detectLibraryOptimizerCapabilities(s.configManager.GetConfig()))
}

func optimizerCategory(cfg *config.Config, id string) (config.LocalMediaCategory, bool) {
	for _, category := range cfg.LocalMedia.Categories {
		if strings.TrimSpace(category.ID) == strings.TrimSpace(id) {
			return category, true
		}
	}
	return config.LocalMediaCategory{}, false
}

func optimizerAbsolutePath(cfg *config.Config, file taterLocalLibraryFileIndex) (string, error) {
	category, ok := optimizerCategory(cfg, file.CategoryID)
	if !ok {
		return "", fmt.Errorf("library category %q no longer exists", file.CategoryID)
	}
	roots := taterLocalMediaCategoryPaths(category)
	if file.SourceIndex < 0 || file.SourceIndex >= len(roots) {
		return "", fmt.Errorf("library source %d is unavailable", file.SourceIndex)
	}
	return safeLocalPath(roots[file.SourceIndex], file.Path)
}

func optimizerFingerprint(path string, info os.FileInfo, cfg config.LibraryOptimizerConfig) string {
	payload := strings.Join([]string{
		filepath.Clean(path), strconv.FormatInt(info.Size(), 10), strconv.FormatInt(info.ModTime().UnixNano(), 10),
		cfg.TargetCodec, cfg.Quality, cfg.AudioPolicy, cfg.AudioFormat, cfg.SubtitlePolicy,
		strconv.FormatBool(boolValue(cfg.ConvertHEVCToAV1, false)),
		strconv.FormatBool(boolValue(cfg.CleanEfficientFiles, false)),
		strconv.FormatBool(boolValue(cfg.PreserveHDR, true)),
		strconv.Itoa(intValue(cfg.MinimumSavingsPercent, 15)),
	}, "\x00")
	hash := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(hash[:16])
}

func normalizeOptimizerCodec(codec string) string {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "h265", "x265", "hevc":
		return "hevc"
	case "av01", "av1":
		return "av1"
	case "avc", "x264", "h264":
		return "h264"
	default:
		return strings.ToLower(strings.TrimSpace(codec))
	}
}

func optimizerAction(codec string, cfg config.LibraryOptimizerConfig) string {
	codec = normalizeOptimizerCodec(codec)
	target := normalizeOptimizerCodec(cfg.TargetCodec)
	if codec == "av1" || (codec == "hevc" && (target == "hevc" || !boolValue(cfg.ConvertHEVCToAV1, false))) {
		if boolValue(cfg.CleanEfficientFiles, false) {
			return "cleanup"
		}
		return "skip-efficient"
	}
	if codec == target {
		if boolValue(cfg.CleanEfficientFiles, false) {
			return "cleanup"
		}
		return "skip-efficient"
	}
	return "transcode"
}

func optimizerSupportedPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv", ".mp4", ".m4v", ".mov", ".avi", ".wmv", ".ts", ".m2ts", ".mpg", ".mpeg", ".webm":
		return true
	default:
		return false
	}
}

func buildOptimizerCandidates(cfg *config.Config, store libraryOptimizerStore) ([]libraryOptimizerCandidate, libraryOptimizerAnalysis, error) {
	analysis := libraryOptimizerAnalysis{GeneratedAt: time.Now().UTC(), Codecs: map[string]int{}}
	index, err := readTaterLocalLibraryIndex(cfg)
	if err != nil {
		return nil, analysis, fmt.Errorf("read local library index: %w", err)
	}
	candidates := []libraryOptimizerCandidate{}
	for _, file := range index.Files {
		if strings.EqualFold(file.LibraryType, "music") || !optimizerSupportedPath(file.Path) {
			continue
		}
		analysis.TotalFiles++
		analysis.SourceBytes += file.SizeBytes
		codec := normalizeOptimizerCodec(file.VideoCodec)
		if codec == "" {
			codec = "unknown"
		}
		analysis.Codecs[codec]++
		action := optimizerAction(codec, cfg.LibraryOptimizer)
		if action == "skip-efficient" {
			analysis.AlreadyEfficient++
			continue
		}
		absPath, pathErr := optimizerAbsolutePath(cfg, file)
		if pathErr != nil {
			analysis.UnavailableFiles++
			continue
		}
		info, statErr := os.Stat(absPath)
		if statErr != nil || info.IsDir() {
			analysis.UnavailableFiles++
			continue
		}
		fingerprint := optimizerFingerprint(absPath, info, cfg.LibraryOptimizer)
		if record, ok := store.Records[filepath.Clean(absPath)]; ok && record.Fingerprint == fingerprint && (record.Status == "completed" || record.Status == "skipped") {
			analysis.PreviouslyHandled++
			continue
		}
		if boolValue(cfg.LibraryOptimizer.SkipDolbyVision, true) && strings.Contains(strings.ToLower(file.VideoRange), "dolby") {
			analysis.DolbyVisionSkipped++
			continue
		}
		analysis.EligibleFiles++
		analysis.EligibleBytes += info.Size()
		if action == "cleanup" {
			analysis.TrackCleanupOnly++
		} else {
			analysis.VideoConversions++
		}
		candidates = append(candidates, libraryOptimizerCandidate{File: file, AbsolutePath: absPath, Action: action, Fingerprint: fingerprint})
	}
	return candidates, analysis, nil
}

func (service *libraryOptimizerService) analyze() (libraryOptimizerAnalysis, error) {
	if service == nil || service.server == nil || service.server.configManager == nil {
		return libraryOptimizerAnalysis{}, fmt.Errorf("configuration is unavailable")
	}
	cfg := service.server.configManager.GetConfig()
	_, analysis, err := buildOptimizerCandidates(cfg, readLibraryOptimizerStore(cfg))
	if err == nil {
		service.mu.Lock()
		service.status.Analysis = &analysis
		service.status.Message = fmt.Sprintf("%d files are ready for optimization", analysis.EligibleFiles)
		service.mu.Unlock()
	}
	return analysis, err
}

func (service *libraryOptimizerService) snapshot() libraryOptimizerStatus {
	service.mu.RLock()
	defer service.mu.RUnlock()
	status := service.status
	if status.Analysis != nil {
		analysis := *status.Analysis
		status.Analysis = &analysis
	}
	return status
}

func (service *libraryOptimizerService) setStatus(update func(*libraryOptimizerStatus)) {
	service.mu.Lock()
	update(&service.status)
	service.mu.Unlock()
}

func (service *libraryOptimizerService) start(automatic bool) error {
	if service == nil || service.server == nil || service.server.configManager == nil {
		return fmt.Errorf("library optimizer is unavailable")
	}
	cfg := service.server.configManager.GetConfig()
	if cfg == nil || !boolValue(cfg.LibraryOptimizer.Enabled, false) {
		return fmt.Errorf("enable Library Optimization before starting a job")
	}
	capabilities := detectLibraryOptimizerCapabilities(cfg)
	if !capabilities.FFmpegAvailable || !capabilities.FFprobeAvailable {
		return fmt.Errorf("FFmpeg and FFprobe are required")
	}
	if !capabilities.LibraryWritable {
		return fmt.Errorf("every enabled movie and series library must be writable")
	}
	targetEncoder := capabilities.RecommendedHEVC
	if strings.EqualFold(cfg.LibraryOptimizer.TargetCodec, "av1") {
		targetEncoder = capabilities.RecommendedAV1
	}
	if targetEncoder == "" {
		return fmt.Errorf("no usable %s encoder was detected", strings.ToUpper(cfg.LibraryOptimizer.TargetCodec))
	}
	if cfg.LibraryOptimizer.AudioFormat == "eac3" && !capabilities.EAC3 {
		return fmt.Errorf("the configured FFmpeg does not include the E-AC-3 encoder")
	}
	if cfg.LibraryOptimizer.AudioFormat == "aac" && !capabilities.AAC {
		return fmt.Errorf("the configured FFmpeg does not include the AAC encoder")
	}
	service.mu.Lock()
	if service.status.Running {
		service.mu.Unlock()
		return fmt.Errorf("library optimization is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	service.cancel = cancel
	service.status = libraryOptimizerStatus{
		State: "preparing", Message: "Reading the local media index", Running: true,
		StartedAt: time.Now().UTC(),
	}
	service.mu.Unlock()
	go service.run(ctx, cfg.DeepCopy(), targetEncoder, automatic)
	return nil
}

func (service *libraryOptimizerService) setPaused(paused bool) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	if !service.status.Running {
		return fmt.Errorf("library optimization is not running")
	}
	service.status.Paused = paused
	if paused {
		service.status.State = "pausing"
		service.status.Message = "Pause requested; the current file will finish first"
	} else {
		service.status.State = "running"
		service.status.Message = "Resuming library optimization"
	}
	return nil
}

func (service *libraryOptimizerService) stop() error {
	service.mu.Lock()
	defer service.mu.Unlock()
	if !service.status.Running || service.cancel == nil {
		return fmt.Errorf("library optimization is not running")
	}
	service.status.State = "cancelling"
	service.status.Message = "Stopping after the current FFmpeg process exits"
	service.cancel()
	return nil
}

func (service *libraryOptimizerService) waitUntilRunnable(ctx context.Context, cfg *config.Config) error {
	for {
		service.mu.RLock()
		paused := service.status.Paused
		service.mu.RUnlock()
		playback := boolValue(cfg.LibraryOptimizer.PauseDuringPlayback, true) && service.server.streamTracker != nil && len(service.server.streamTracker.GetActive()) > 0
		if !paused && !playback {
			service.setStatus(func(status *libraryOptimizerStatus) {
				status.WaitingPlayback = false
				status.State = "running"
				status.Message = "Optimizing local media"
			})
			return nil
		}
		service.setStatus(func(status *libraryOptimizerStatus) {
			status.WaitingPlayback = playback
			if playback {
				status.State = "waiting"
				status.Message = "Paused while Tater Tube playback is active"
			}
		})
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(libraryOptimizerPollDelay):
		}
	}
}

func (service *libraryOptimizerService) run(ctx context.Context, cfg *config.Config, encoder string, automatic bool) {
	store := readLibraryOptimizerStore(cfg)
	candidates, analysis, err := buildOptimizerCandidates(cfg, store)
	if err != nil {
		service.finish("failed", "Unable to prepare the optimization queue", err)
		return
	}
	service.setStatus(func(status *libraryOptimizerStatus) {
		status.Analysis = &analysis
		status.Total = len(candidates)
		status.State = "running"
		status.Message = fmt.Sprintf("Optimizing %d local media files", len(candidates))
	})
	service.cleanupQuarantine(cfg)
	if len(candidates) == 0 {
		service.finish("completed", "The library is already optimized", nil)
		return
	}
	for index, candidate := range candidates {
		if err := service.waitUntilRunnable(ctx, cfg); err != nil {
			service.finish("cancelled", "Library optimization stopped", nil)
			return
		}
		service.setStatus(func(status *libraryOptimizerStatus) {
			status.Current = index + 1
			status.CurrentFile = filepath.Base(candidate.AbsolutePath)
		})
		record := service.processCandidate(ctx, cfg, encoder, candidate)
		store.Records[filepath.Clean(candidate.AbsolutePath)] = record
		if err := writeLibraryOptimizerStore(cfg, store); err != nil {
			slog.WarnContext(ctx, "Failed to save library optimization history", "error", err)
		}
		service.setStatus(func(status *libraryOptimizerStatus) {
			status.OriginalBytes += record.OriginalBytes
			status.OutputBytes += record.OutputBytes
			status.SavedBytes += max(int64(0), record.SavedBytes)
			switch record.Status {
			case "completed":
				status.Completed++
			case "skipped":
				status.Skipped++
			default:
				status.Failed++
				status.LastError = record.Message
			}
		})
	}
	message := "Library optimization finished"
	if automatic {
		message = "Automatic library optimization finished"
		status := service.snapshot()
		if status.Failed == 0 {
			fingerprint := automaticLibraryOptimizerFingerprint(cfg)
			service.mu.Lock()
			service.automaticFingerprint = fingerprint
			service.mu.Unlock()
		}
	}
	service.finish("completed", message, nil)
	select {
	case service.server.localLibraryScanWake <- struct{}{}:
	default:
	}
}

func (service *libraryOptimizerService) finish(state, message string, err error) {
	service.setStatus(func(status *libraryOptimizerStatus) {
		status.State = state
		status.Message = message
		status.Running = false
		status.Paused = false
		status.WaitingPlayback = false
		status.CurrentFile = ""
		status.FinishedAt = time.Now().UTC()
		if err != nil {
			status.LastError = err.Error()
		}
	})
	service.mu.Lock()
	service.cancel = nil
	service.mu.Unlock()
}

func (service *libraryOptimizerService) runAutomatic(ctx context.Context) {
	timer := time.NewTimer(libraryOptimizerAutoDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			cfg := service.server.configManager.GetConfig()
			if cfg != nil && boolValue(cfg.LibraryOptimizer.Enabled, false) {
				service.cleanupQuarantine(cfg)
			}
			if cfg != nil && boolValue(cfg.LibraryOptimizer.Enabled, false) && boolValue(cfg.LibraryOptimizer.Automatic, false) {
				service.mu.RLock()
				running := service.status.Running
				previousFingerprint := service.automaticFingerprint
				service.mu.RUnlock()
				fingerprint := automaticLibraryOptimizerFingerprint(cfg)
				if !running && fingerprint != "" && fingerprint != previousFingerprint {
					analysis, err := service.analyze()
					if err != nil {
						slog.DebugContext(ctx, "Automatic library optimization analysis failed", "reason", err)
					} else if analysis.EligibleFiles == 0 {
						service.mu.Lock()
						service.automaticFingerprint = fingerprint
						service.mu.Unlock()
					} else if err := service.start(true); err != nil {
						slog.DebugContext(ctx, "Automatic library optimization did not start", "reason", err)
					}
				}
			}
			timer.Reset(libraryOptimizerAutoDelay)
		}
	}
}

func automaticLibraryOptimizerFingerprint(cfg *config.Config) string {
	index, err := readTaterLocalLibraryIndex(cfg)
	if err != nil {
		return ""
	}
	payload, _ := json.Marshal(struct {
		IndexGeneratedAt time.Time                     `json:"index_generated_at"`
		IndexConfig      string                        `json:"index_config"`
		Settings         config.LibraryOptimizerConfig `json:"settings"`
	}{IndexGeneratedAt: index.GeneratedAt, IndexConfig: index.ConfigFingerprint, Settings: cfg.LibraryOptimizer})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:16])
}

func optimizerProbeFile(ctx context.Context, cfg *config.Config, path string) (optimizerProbe, error) {
	var probe optimizerProbe
	probePath := effectiveFFprobePath(effectiveFFmpegPath(cfg.Transcoding.FFmpegPath))
	if probePath == "" {
		return probe, fmt.Errorf("ffprobe is not available")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, probePath,
		"-v", "error", "-show_streams", "-show_format", "-of", "json", path,
	).CombinedOutput()
	if err != nil {
		return probe, fmt.Errorf("ffprobe failed: %s", compactOptimizerError(out, err))
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		return probe, fmt.Errorf("read ffprobe output: %w", err)
	}
	return probe, nil
}

func optimizerHasDolbyVision(probe optimizerProbe) bool {
	for _, stream := range probe.Streams {
		if stream.CodecType != "video" {
			continue
		}
		for _, sideData := range stream.SideData {
			kind := strings.ToLower(fmt.Sprint(sideData["side_data_type"]))
			if strings.Contains(kind, "dovi") || strings.Contains(kind, "dolby vision") {
				return true
			}
		}
	}
	return false
}

func optimizerVideoStream(probe optimizerProbe) (optimizerProbeStream, bool) {
	for _, stream := range probe.Streams {
		if stream.CodecType == "video" && stream.Disposition.AttachedPic == 0 {
			return stream, true
		}
	}
	for _, stream := range probe.Streams {
		if stream.CodecType == "video" {
			return stream, true
		}
	}
	return optimizerProbeStream{}, false
}

func optimizerAudioScore(stream optimizerProbeStream) int {
	title := strings.ToLower(strings.TrimSpace(stream.Tags["title"]))
	language := strings.ToLower(strings.TrimSpace(stream.Tags["language"]))
	profile := strings.ToLower(strings.TrimSpace(stream.Profile))
	if stream.Disposition.Comment != 0 || stream.Disposition.VisualImpaired != 0 ||
		strings.Contains(title, "commentary") || strings.Contains(title, "description") ||
		strings.Contains(title, "descriptive") || strings.Contains(title, "director") {
		return -10000
	}
	score := stream.Channels * 100
	if language == "eng" || language == "en" || strings.HasPrefix(language, "en-") || language == "english" {
		score += 5000
	} else if language == "" || language == "und" {
		score += 500
	}
	if stream.Disposition.Default != 0 {
		score += 300
	}
	if stream.Disposition.Original != 0 {
		score += 200
	}
	switch codec := normalizeOptimizerCodec(stream.CodecName); {
	case codec == "dts" && strings.Contains(profile, "hd"):
		score += 1100
	case codec == "truehd" || codec == "mlp" || codec == "dts-hd" || codec == "dts_ma":
		score += 1000
	case codec == "dts":
		score += 800
	case codec == "flac":
		score += 800
	case codec == "eac3":
		score += 600
	case codec == "ac3":
		score += 400
	case codec == "aac" || codec == "opus":
		score += 300
	}
	return score
}

func optimizerEnglish(language string) bool {
	language = strings.ToLower(strings.TrimSpace(language))
	return language == "eng" || language == "en" || strings.HasPrefix(language, "en-") || language == "english"
}

func selectOptimizerAudio(probe optimizerProbe, policy string) []optimizerProbeStream {
	streams := []optimizerProbeStream{}
	for _, stream := range probe.Streams {
		if stream.CodecType != "audio" || optimizerAudioScore(stream) < 0 {
			continue
		}
		streams = append(streams, stream)
	}
	if len(streams) == 0 {
		return nil
	}
	if policy == "all" {
		return streams
	}
	english := streams[:0]
	for _, stream := range streams {
		if optimizerEnglish(stream.Tags["language"]) {
			english = append(english, stream)
		}
	}
	if len(english) > 0 {
		streams = english
	}
	if policy == "all_english" {
		return streams
	}
	sort.SliceStable(streams, func(i, j int) bool { return optimizerAudioScore(streams[i]) > optimizerAudioScore(streams[j]) })
	return streams[:1]
}

func selectOptimizerSubtitles(probe optimizerProbe, policy string) []optimizerProbeStream {
	if policy == "remove" {
		return nil
	}
	streams := []optimizerProbeStream{}
	for _, stream := range probe.Streams {
		if stream.CodecType != "subtitle" {
			continue
		}
		if policy != "all" && !optimizerEnglish(stream.Tags["language"]) {
			continue
		}
		if policy == "forced_english" && stream.Disposition.Forced == 0 {
			continue
		}
		streams = append(streams, stream)
	}
	return streams
}

func optimizerQuality(cfg config.LibraryOptimizerConfig) int {
	if strings.EqualFold(cfg.TargetCodec, "av1") {
		switch cfg.Quality {
		case "high":
			return 24
		case "small":
			return 34
		default:
			return 29
		}
	}
	switch cfg.Quality {
	case "high":
		return 21
	case "small":
		return 30
	default:
		return 26
	}
}

func optimizerVideoArgs(encoder string, cfg config.LibraryOptimizerConfig, stream optimizerProbeStream) []string {
	quality := optimizerQuality(cfg)
	args := []string{"-c:v", encoder}
	switch {
	case encoder == "libx265":
		args = append(args, "-preset", "medium", "-crf", strconv.Itoa(quality))
	case encoder == "libsvtav1":
		args = append(args, "-preset", "6", "-crf", strconv.Itoa(quality))
	case strings.HasSuffix(encoder, "_nvenc"):
		args = append(args, "-preset", "p5", "-tune", "hq", "-rc", "vbr", "-cq", strconv.Itoa(quality), "-b:v", "0")
	case strings.HasSuffix(encoder, "_qsv"):
		args = append(args, "-preset", "slow", "-global_quality", strconv.Itoa(quality))
	case strings.HasSuffix(encoder, "_vaapi"):
		args = append(args, "-qp", strconv.Itoa(quality))
	case strings.HasSuffix(encoder, "_videotoolbox"):
		videoToolboxQuality := max(35, 100-quality*2)
		args = append(args, "-q:v", strconv.Itoa(videoToolboxQuality), "-realtime", "0")
	}
	if boolValue(cfg.PreserveHDR, true) && optimizerStreamHDR(stream) {
		if strings.Contains(encoder, "vaapi") {
			args = append([]string{"-vf", "format=p010,hwupload"}, args...)
		} else {
			args = append(args, "-pix_fmt", "yuv420p10le")
		}
		if stream.ColorPrimaries != "" {
			args = append(args, "-color_primaries", stream.ColorPrimaries)
		}
		if stream.ColorTransfer != "" {
			args = append(args, "-color_trc", stream.ColorTransfer)
		}
		if stream.ColorSpace != "" {
			args = append(args, "-colorspace", stream.ColorSpace)
		}
	} else if strings.Contains(encoder, "vaapi") {
		args = append([]string{"-vf", "format=nv12,hwupload"}, args...)
	}
	return args
}

func optimizerStreamHDR(stream optimizerProbeStream) bool {
	transfer := strings.ToLower(stream.ColorTransfer)
	primaries := strings.ToLower(stream.ColorPrimaries)
	return strings.Contains(transfer, "2084") || strings.Contains(transfer, "arib") || strings.Contains(primaries, "2020")
}

func optimizerInputArgs(encoder string, cfg *config.Config) []string {
	device := strings.TrimSpace(cfg.Transcoding.HardwareDevice)
	if device == "" {
		gpus := detectDRMGPUVendors()
		if strings.HasSuffix(encoder, "_qsv") {
			device = firstDRIRenderDeviceForVendor(gpus, "intel")
		} else if strings.HasSuffix(encoder, "_vaapi") {
			device = firstDRIRenderDeviceForVendor(gpus, "amd")
			if device == "" {
				device = firstDRIRenderDeviceForVendor(gpus, "intel")
			}
		}
	}
	if device == "" {
		return nil
	}
	probeCfg := cfg.Transcoding
	probeCfg.HardwareDevice = device
	if strings.HasSuffix(encoder, "_vaapi") {
		return transcodeHardwareInitArgs(probeCfg, "vaapi")
	}
	if strings.HasSuffix(encoder, "_qsv") {
		return transcodeHardwareInitArgs(probeCfg, "qsv")
	}
	return nil
}

func optimizerFFmpegArgs(cfg *config.Config, encoder string, candidate libraryOptimizerCandidate, probe optimizerProbe, tempPath string) ([]string, error) {
	video, ok := optimizerVideoStream(probe)
	if !ok {
		return nil, fmt.Errorf("source has no video stream")
	}
	audio := selectOptimizerAudio(probe, cfg.LibraryOptimizer.AudioPolicy)
	hasSourceAudio := false
	for _, stream := range probe.Streams {
		if stream.CodecType == "audio" {
			hasSourceAudio = true
			break
		}
	}
	if hasSourceAudio && len(audio) == 0 {
		return nil, fmt.Errorf("source has audio, but no eligible main track matched the audio policy")
	}
	subtitles := selectOptimizerSubtitles(probe, cfg.LibraryOptimizer.SubtitlePolicy)
	args := []string{"-hide_banner", "-nostdin", "-y"}
	args = append(args, optimizerInputArgs(encoder, cfg)...)
	args = append(args, "-i", candidate.AbsolutePath, "-map", fmt.Sprintf("0:%d", video.Index))
	for _, stream := range audio {
		args = append(args, "-map", fmt.Sprintf("0:%d", stream.Index))
	}
	for _, stream := range subtitles {
		args = append(args, "-map", fmt.Sprintf("0:%d", stream.Index))
	}
	if len(subtitles) > 0 {
		args = append(args, "-map", "0:t?")
	}
	if candidate.Action == "cleanup" {
		args = append(args, "-c:v", "copy")
	} else {
		args = append(args, optimizerVideoArgs(encoder, cfg.LibraryOptimizer, video)...)
	}
	switch cfg.LibraryOptimizer.AudioFormat {
	case "eac3":
		args = append(args, "-c:a", "eac3", "-b:a", "640k")
	case "aac":
		args = append(args, "-c:a", "aac", "-b:a", "384k")
	default:
		args = append(args, "-c:a", "copy")
	}
	if len(subtitles) > 0 {
		args = append(args, "-c:s", "copy", "-c:t", "copy")
	}
	args = append(args, "-map_metadata", "0", "-map_chapters", "0", "-max_muxing_queue_size", "4096", tempPath)
	return args, nil
}

func compactOptimizerError(output []byte, fallback error) string {
	text := strings.TrimSpace(string(output))
	if len(text) > 3000 {
		text = text[len(text)-3000:]
	}
	if text == "" && fallback != nil {
		return fallback.Error()
	}
	return text
}

func optimizerDuration(probe optimizerProbe) float64 {
	duration, _ := strconv.ParseFloat(probe.Format.Duration, 64)
	return duration
}

func validateOptimizerOutput(source, output optimizerProbe, originalSize, outputSize int64) error {
	if _, ok := optimizerVideoStream(output); !ok {
		return fmt.Errorf("optimized file has no video stream")
	}
	sourceDuration := optimizerDuration(source)
	outputDuration := optimizerDuration(output)
	if sourceDuration > 0 && outputDuration > 0 {
		tolerance := math.Max(5, sourceDuration*0.01)
		if math.Abs(sourceDuration-outputDuration) > tolerance {
			return fmt.Errorf("duration changed from %.1fs to %.1fs", sourceDuration, outputDuration)
		}
	}
	sourceAudio := selectOptimizerAudio(source, "all")
	outputAudio := selectOptimizerAudio(output, "all")
	if len(sourceAudio) > 0 && len(outputAudio) == 0 {
		return fmt.Errorf("optimized file has no audio stream")
	}
	if outputSize <= 0 || outputSize >= originalSize*2 {
		return fmt.Errorf("optimized file size is invalid")
	}
	return nil
}

func optimizerTempPath(source string) (string, error) {
	ext := filepath.Ext(source)
	file, err := os.CreateTemp(filepath.Dir(source), ".tater-optimize-*"+ext)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	_ = os.Remove(path)
	return path, nil
}

func replaceOptimizerSource(source, output string, quarantineDays int) (string, error) {
	directory := filepath.Dir(source)
	backup := ""
	if quarantineDays > 0 {
		quarantine := filepath.Join(directory, ".tater-tube-originals", time.Now().UTC().Format("2006-01-02"))
		if err := os.MkdirAll(quarantine, 0755); err != nil {
			return "", err
		}
		backup = filepath.Join(quarantine, filepath.Base(source))
		if _, err := os.Stat(backup); err == nil {
			backup = filepath.Join(quarantine, fmt.Sprintf("%d-%s", time.Now().Unix(), filepath.Base(source)))
		}
	} else {
		file, err := os.CreateTemp(directory, ".tater-original-*")
		if err != nil {
			return "", err
		}
		backup = file.Name()
		_ = file.Close()
		_ = os.Remove(backup)
	}
	if err := os.Rename(source, backup); err != nil {
		return "", fmt.Errorf("protect original: %w", err)
	}
	if err := os.Rename(output, source); err != nil {
		_ = os.Rename(backup, source)
		return "", fmt.Errorf("install optimized file: %w", err)
	}
	if quarantineDays <= 0 {
		if err := os.Remove(backup); err != nil {
			slog.Warn("Failed to remove optimizer backup", "path", backup, "error", err)
		}
		return "", nil
	}
	return backup, nil
}

func (service *libraryOptimizerService) processCandidate(ctx context.Context, cfg *config.Config, encoder string, candidate libraryOptimizerCandidate) libraryOptimizerRecord {
	record := libraryOptimizerRecord{
		Path: candidate.AbsolutePath, Fingerprint: candidate.Fingerprint, Action: candidate.Action,
		TargetCodec: cfg.LibraryOptimizer.TargetCodec, UpdatedAt: time.Now().UTC(),
	}
	info, err := os.Stat(candidate.AbsolutePath)
	if err != nil {
		record.Status, record.Message = "failed", err.Error()
		return record
	}
	record.OriginalBytes = info.Size()
	probe, err := optimizerProbeFile(ctx, cfg, candidate.AbsolutePath)
	if err != nil {
		record.Status, record.Message = "failed", err.Error()
		return record
	}
	video, ok := optimizerVideoStream(probe)
	if !ok {
		record.Status, record.Message = "failed", "No video stream was found"
		return record
	}
	record.SourceCodec = normalizeOptimizerCodec(video.CodecName)
	if boolValue(cfg.LibraryOptimizer.SkipDolbyVision, true) && optimizerHasDolbyVision(probe) {
		record.Status, record.Message = "skipped", "Dolby Vision was protected from lossy conversion"
		return record
	}
	if freeBytes, _ := getDiskSpace(filepath.Dir(candidate.AbsolutePath)); freeBytes > 0 && freeBytes < uint64(info.Size()) {
		record.Status = "failed"
		record.Message = fmt.Sprintf("Not enough free space beside the source (need about %s)", optimizerByteLabel(info.Size()))
		return record
	}
	tempPath, err := optimizerTempPath(candidate.AbsolutePath)
	if err != nil {
		record.Status, record.Message = "failed", fmt.Sprintf("create working file: %v", err)
		return record
	}
	defer os.Remove(tempPath)
	args, err := optimizerFFmpegArgs(cfg, encoder, candidate, probe, tempPath)
	if err != nil {
		record.Status, record.Message = "failed", err.Error()
		return record
	}
	output, err := exec.CommandContext(ctx, effectiveFFmpegPath(cfg.Transcoding.FFmpegPath), args...).CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			record.Status, record.Message = "failed", "Cancelled"
		} else {
			record.Status, record.Message = "failed", compactOptimizerError(output, err)
		}
		return record
	}
	outputInfo, err := os.Stat(tempPath)
	if err != nil {
		record.Status, record.Message = "failed", "FFmpeg did not create an output file"
		return record
	}
	record.OutputBytes = outputInfo.Size()
	outputProbe, err := optimizerProbeFile(ctx, cfg, tempPath)
	if err != nil {
		record.Status, record.Message = "failed", fmt.Sprintf("validate output: %v", err)
		return record
	}
	if err := validateOptimizerOutput(probe, outputProbe, info.Size(), outputInfo.Size()); err != nil {
		record.Status, record.Message = "failed", err.Error()
		return record
	}
	saved := info.Size() - outputInfo.Size()
	record.SavedBytes = saved
	if saved <= 0 {
		record.Status = "skipped"
		record.Message = "The optimized copy was not smaller; the original was kept"
		return record
	}
	if candidate.Action == "transcode" && info.Size() > 0 {
		savingsPercent := int((saved * 100) / info.Size())
		if savingsPercent < intValue(cfg.LibraryOptimizer.MinimumSavingsPercent, 15) {
			record.Status = "skipped"
			record.Message = fmt.Sprintf("Only %d%% smaller; the original was kept", savingsPercent)
			return record
		}
	}
	quarantine, err := replaceOptimizerSource(candidate.AbsolutePath, tempPath, intValue(cfg.LibraryOptimizer.QuarantineDays, 7))
	if err != nil {
		record.Status, record.Message = "failed", err.Error()
		return record
	}
	record.Status = "completed"
	record.Message = "Optimized and validated"
	if installedInfo, statErr := os.Stat(candidate.AbsolutePath); statErr == nil {
		record.Fingerprint = optimizerFingerprint(candidate.AbsolutePath, installedInfo, cfg.LibraryOptimizer)
	}
	if quarantine != "" {
		record.Message = "Optimized and validated; original quarantined"
	}
	return record
}

func optimizerByteLabel(value int64) string {
	const (
		gib = int64(1024 * 1024 * 1024)
		mib = int64(1024 * 1024)
	)
	if value >= gib {
		return fmt.Sprintf("%.1f GiB", float64(value)/float64(gib))
	}
	return fmt.Sprintf("%.1f MiB", float64(value)/float64(mib))
}

func cleanupOptimizerQuarantine(cfg *config.Config) {
	days := intValue(cfg.LibraryOptimizer.QuarantineDays, 7)
	if days <= 0 {
		return
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	seen := map[string]struct{}{}
	for _, category := range cfg.LocalMedia.Categories {
		for _, root := range taterLocalMediaCategoryPaths(category) {
			root = filepath.Clean(root)
			if _, ok := seen[root]; ok {
				continue
			}
			seen[root] = struct{}{}
			_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return nil
				}
				if entry.IsDir() && entry.Name() == ".tater-tube-originals" {
					dateDirectories, _ := os.ReadDir(path)
					for _, dateDirectory := range dateDirectories {
						if !dateDirectory.IsDir() {
							continue
						}
						created, parseErr := time.Parse("2006-01-02", dateDirectory.Name())
						if parseErr == nil && created.Before(cutoff) {
							_ = os.RemoveAll(filepath.Join(path, dateDirectory.Name()))
						}
					}
					return filepath.SkipDir
				}
				if path != root && entry.IsDir() && strings.HasPrefix(entry.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			})
		}
	}
}

func (service *libraryOptimizerService) cleanupQuarantine(cfg *config.Config) {
	service.mu.Lock()
	if !service.lastCleanup.IsZero() && time.Since(service.lastCleanup) < 24*time.Hour {
		service.mu.Unlock()
		return
	}
	service.lastCleanup = time.Now()
	service.mu.Unlock()
	cleanupOptimizerQuarantine(cfg)
}

func (s *Server) handleLibraryOptimizerAnalyze(c *fiber.Ctx) error {
	if s.libraryOptimizer == nil {
		return RespondServiceUnavailable(c, "Library optimizer is unavailable", "OPTIMIZER_UNAVAILABLE")
	}
	analysis, err := s.libraryOptimizer.analyze()
	if err != nil {
		return RespondInternalError(c, "Failed to analyze the local library", err.Error())
	}
	return RespondSuccess(c, analysis)
}

func (s *Server) handleLibraryOptimizerStatus(c *fiber.Ctx) error {
	if s.libraryOptimizer == nil {
		return RespondServiceUnavailable(c, "Library optimizer is unavailable", "OPTIMIZER_UNAVAILABLE")
	}
	return RespondSuccess(c, s.libraryOptimizer.snapshot())
}

func (s *Server) handleLibraryOptimizerStart(c *fiber.Ctx) error {
	if s.libraryOptimizer == nil {
		return RespondServiceUnavailable(c, "Library optimizer is unavailable", "OPTIMIZER_UNAVAILABLE")
	}
	if err := s.libraryOptimizer.start(false); err != nil {
		return RespondValidationError(c, "Unable to start library optimization", err.Error())
	}
	return RespondSuccess(c, s.libraryOptimizer.snapshot())
}

func (s *Server) handleLibraryOptimizerPause(c *fiber.Ctx) error {
	if s.libraryOptimizer == nil {
		return RespondServiceUnavailable(c, "Library optimizer is unavailable", "OPTIMIZER_UNAVAILABLE")
	}
	request := struct {
		Paused bool `json:"paused"`
	}{}
	if err := c.BodyParser(&request); err != nil {
		return RespondValidationError(c, "Invalid pause request", err.Error())
	}
	if err := s.libraryOptimizer.setPaused(request.Paused); err != nil {
		return RespondValidationError(c, "Unable to update optimizer", err.Error())
	}
	return RespondSuccess(c, s.libraryOptimizer.snapshot())
}

func (s *Server) handleLibraryOptimizerCancel(c *fiber.Ctx) error {
	if s.libraryOptimizer == nil {
		return RespondServiceUnavailable(c, "Library optimizer is unavailable", "OPTIMIZER_UNAVAILABLE")
	}
	if err := s.libraryOptimizer.stop(); err != nil {
		return RespondValidationError(c, "Unable to stop library optimization", err.Error())
	}
	return RespondSuccess(c, s.libraryOptimizer.snapshot())
}
