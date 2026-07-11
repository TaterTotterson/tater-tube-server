package api

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
)

type transcodeHardwareOption struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
	Device    string `json:"device,omitempty"`
	Status    string `json:"status"`
	Details   string `json:"details,omitempty"`
}

type transcodeHardwareDetection struct {
	FFmpegPath        string                    `json:"ffmpeg_path"`
	FFmpegAvailable   bool                      `json:"ffmpeg_available"`
	Recommended       string                    `json:"recommended"`
	RecommendedDevice string                    `json:"recommended_device,omitempty"`
	Current           string                    `json:"current"`
	CurrentDevice     string                    `json:"current_device,omitempty"`
	Options           []transcodeHardwareOption `json:"options"`
	Notes             []string                  `json:"notes,omitempty"`
}

func (s *Server) handleDetectTranscodingHardware(c *fiber.Ctx) error {
	if s.configManager == nil {
		return RespondServiceUnavailable(c, "Configuration management not available", "CONFIG_UNAVAILABLE")
	}
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondInternalError(c, "Configuration not available", "CONFIG_NOT_FOUND")
	}
	return RespondSuccess(c, detectTranscodingHardware(cfg.Transcoding))
}

func detectTranscodingHardware(cfg config.TranscodingConfig) transcodeHardwareDetection {
	ffmpegPath := strings.TrimSpace(cfg.FFmpegPath)
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	current := strings.TrimSpace(cfg.HardwareAcceleration)
	if current == "" {
		current = "none"
	}

	result := transcodeHardwareDetection{
		FFmpegPath:      ffmpegPath,
		Current:         current,
		CurrentDevice:   strings.TrimSpace(cfg.HardwareDevice),
		Recommended:     "none",
		FFmpegAvailable: false,
	}

	if _, err := exec.LookPath(ffmpegPath); err != nil {
		result.Options = []transcodeHardwareOption{
			{ID: "none", Label: "Software x264", Available: false, Status: "FFmpeg not found"},
		}
		result.Notes = append(result.Notes, "Install FFmpeg or set the FFmpeg path before enabling transcoding.")
		return result
	}
	result.FFmpegAvailable = true

	encoders := ffmpegOutput(ffmpegPath, "-hide_banner", "-encoders")
	hwaccels := ffmpegOutput(ffmpegPath, "-hide_banner", "-hwaccels")
	gpus := detectDRMGPUVendors()
	hasDRI := len(gpus) > 0 || pathExists("/dev/dri/renderD128")
	profile := transcodeProfiles[cfg.Profile]
	if profile.Name == "" {
		profile = transcodeProfiles["crt_480p"]
	}

	options := []transcodeHardwareOption{
		softwareOption(encoders),
		nvencOption(ffmpegPath, cfg, profile, encoders),
		vaapiOption(ffmpegPath, cfg, profile, encoders, hwaccels, gpus, hasDRI),
		qsvOption(ffmpegPath, cfg, profile, encoders, hwaccels, gpus, hasDRI),
		videotoolboxOption(ffmpegPath, cfg, profile, encoders),
		v4l2m2mOption(ffmpegPath, cfg, profile, encoders),
	}
	result.Options = options

	if opt := firstAvailable(options, "videotoolbox"); runtime.GOOS == "darwin" && opt != nil {
		result.Recommended = opt.ID
		result.RecommendedDevice = opt.Device
		return result
	}
	for _, id := range []string{"nvenc", "vaapi", "qsv", "v4l2m2m", "none"} {
		if opt := firstAvailable(options, id); opt != nil {
			result.Recommended = opt.ID
			result.RecommendedDevice = opt.Device
			return result
		}
	}
	result.Notes = append(result.Notes, "No usable H.264 encoder was detected.")
	return result
}

func softwareOption(encoders string) transcodeHardwareOption {
	if hasFFmpegEncoder(encoders, "libx264") {
		return transcodeHardwareOption{ID: "none", Label: "Software x264", Available: true, Status: "Available", Details: "Uses CPU encoding."}
	}
	return transcodeHardwareOption{ID: "none", Label: "Software x264", Available: false, Status: "Missing libx264 encoder"}
}

func nvencOption(ffmpegPath string, cfg config.TranscodingConfig, profile transcodeProfile, encoders string) transcodeHardwareOption {
	opt := transcodeHardwareOption{ID: "nvenc", Label: "NVIDIA NVENC"}
	if !hasFFmpegEncoder(encoders, "h264_nvenc") {
		opt.Status = "Missing FFmpeg encoder"
		return opt
	}
	if !hasNvidiaDevice() {
		opt.Status = "Encoder present, NVIDIA device not visible"
		opt.Details = "Map the NVIDIA device/runtime into the container."
		return opt
	}
	if ok, reason := probeTranscodeEncoder(context.Background(), ffmpegPath, cfg, profile, "nvenc"); !ok {
		opt.Status = "Encoder probe failed"
		opt.Details = reason
		return opt
	}
	opt.Available = true
	opt.Status = "Available"
	return opt
}

func qsvOption(ffmpegPath string, cfg config.TranscodingConfig, profile transcodeProfile, encoders, hwaccels string, gpus []drmGPUVendor, hasDRI bool) transcodeHardwareOption {
	opt := transcodeHardwareOption{ID: "qsv", Label: "Intel Quick Sync"}
	if !hasFFmpegEncoder(encoders, "h264_qsv") {
		opt.Status = "Missing FFmpeg encoder"
		return opt
	}
	if !strings.Contains(strings.ToLower(hwaccels), "qsv") {
		opt.Status = "Missing FFmpeg QSV acceleration"
		return opt
	}
	if !hasDRI {
		opt.Status = "Encoder present, /dev/dri not visible"
		return opt
	}
	if !hasGPUVendor(gpus, "intel") {
		opt.Status = "Encoder present, Intel GPU not detected"
		return opt
	}
	device := strings.TrimSpace(cfg.HardwareDevice)
	if device == "" {
		device = firstDRIRenderDevice()
	}
	probeCfg := cfg
	probeCfg.HardwareDevice = device
	if ok, reason := probeTranscodeEncoder(context.Background(), ffmpegPath, probeCfg, profile, "qsv"); !ok {
		opt.Status = "Encoder probe failed"
		opt.Details = reason
		return opt
	}
	opt.Available = true
	opt.Device = device
	opt.Status = "Available"
	return opt
}

func vaapiOption(ffmpegPath string, cfg config.TranscodingConfig, profile transcodeProfile, encoders, hwaccels string, gpus []drmGPUVendor, hasDRI bool) transcodeHardwareOption {
	opt := transcodeHardwareOption{ID: "vaapi", Label: "VAAPI"}
	if !hasFFmpegEncoder(encoders, "h264_vaapi") {
		opt.Status = "Missing FFmpeg encoder"
		return opt
	}
	if !strings.Contains(strings.ToLower(hwaccels), "vaapi") {
		opt.Status = "Missing FFmpeg VAAPI acceleration"
		return opt
	}
	if !hasDRI {
		opt.Status = "Encoder present, /dev/dri not visible"
		return opt
	}
	if len(gpus) > 0 && !hasGPUVendor(gpus, "intel") && !hasGPUVendor(gpus, "amd") {
		opt.Status = "Encoder present, supported DRM GPU not detected"
		return opt
	}
	device := strings.TrimSpace(cfg.HardwareDevice)
	if device == "" {
		device = firstDRIRenderDevice()
	}
	probeCfg := cfg
	probeCfg.HardwareDevice = device
	if ok, reason := probeTranscodeEncoder(context.Background(), ffmpegPath, probeCfg, profile, "vaapi"); !ok {
		opt.Status = "Encoder probe failed"
		opt.Details = reason
		return opt
	}
	opt.Available = true
	opt.Device = device
	opt.Status = "Available"
	return opt
}

func videotoolboxOption(ffmpegPath string, cfg config.TranscodingConfig, profile transcodeProfile, encoders string) transcodeHardwareOption {
	opt := transcodeHardwareOption{ID: "videotoolbox", Label: "Apple VideoToolbox"}
	if runtime.GOOS != "darwin" {
		opt.Status = "Not macOS"
		return opt
	}
	if !hasFFmpegEncoder(encoders, "h264_videotoolbox") {
		opt.Status = "Missing FFmpeg encoder"
		return opt
	}
	if ok, reason := probeTranscodeEncoder(context.Background(), ffmpegPath, cfg, profile, "videotoolbox"); !ok {
		opt.Status = "Encoder probe failed"
		opt.Details = reason
		return opt
	}
	opt.Available = true
	opt.Status = "Available"
	return opt
}

func v4l2m2mOption(ffmpegPath string, cfg config.TranscodingConfig, profile transcodeProfile, encoders string) transcodeHardwareOption {
	opt := transcodeHardwareOption{ID: "v4l2m2m", Label: "Linux V4L2 M2M"}
	if runtime.GOOS != "linux" {
		opt.Status = "Not Linux"
		return opt
	}
	if !hasFFmpegEncoder(encoders, "h264_v4l2m2m") {
		opt.Status = "Missing FFmpeg encoder"
		return opt
	}
	devices, _ := filepath.Glob("/dev/video*")
	if len(devices) == 0 {
		opt.Status = "Encoder present, /dev/video devices not visible"
		return opt
	}
	if ok, reason := probeTranscodeEncoder(context.Background(), ffmpegPath, cfg, profile, "v4l2m2m"); !ok {
		opt.Status = "Encoder probe failed"
		opt.Details = reason
		return opt
	}
	opt.Available = true
	opt.Device = devices[0]
	opt.Status = "Available"
	return opt
}

func ffmpegOutput(ffmpegPath string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, ffmpegPath, args...).CombinedOutput()
	return string(out)
}

func hasFFmpegEncoder(encoders, name string) bool {
	for _, line := range strings.Split(encoders, "\n") {
		fields := strings.Fields(line)
		for _, field := range fields {
			if field == name {
				return true
			}
		}
	}
	return false
}

func firstAvailable(options []transcodeHardwareOption, id string) *transcodeHardwareOption {
	for i := range options {
		if options[i].ID == id && options[i].Available {
			return &options[i]
		}
	}
	return nil
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func hasNvidiaDevice() bool {
	if devices, _ := filepath.Glob("/dev/nvidia*"); len(devices) > 0 {
		return true
	}
	_, err := exec.LookPath("nvidia-smi")
	return err == nil
}

type drmGPUVendor struct {
	RenderDevice string
	Vendor       string
}

func detectDRMGPUVendors() []drmGPUVendor {
	paths, _ := filepath.Glob("/sys/class/drm/renderD*/device/vendor")
	gpus := make([]drmGPUVendor, 0, len(paths))
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		gpus = append(gpus, drmGPUVendor{
			RenderDevice: "/dev/dri/" + filepath.Base(filepath.Dir(filepath.Dir(path))),
			Vendor:       vendorName(strings.TrimSpace(string(raw))),
		})
	}
	return gpus
}

func vendorName(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "0x8086":
		return "intel"
	case "0x1002", "0x1022":
		return "amd"
	case "0x10de":
		return "nvidia"
	default:
		return strings.ToLower(strings.TrimSpace(id))
	}
}

func hasGPUVendor(gpus []drmGPUVendor, vendor string) bool {
	for _, gpu := range gpus {
		if gpu.Vendor == vendor {
			return true
		}
	}
	return false
}

func firstDRIRenderDevice() string {
	devices, _ := filepath.Glob("/dev/dri/renderD*")
	if len(devices) == 0 {
		return "/dev/dri/renderD128"
	}
	return devices[0]
}
