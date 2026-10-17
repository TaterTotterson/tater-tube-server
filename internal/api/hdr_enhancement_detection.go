package api

import (
	"context"
	"os/exec"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
)

type hdrEnhancementCompatibilityDetection struct {
	FFmpegPath      string   `json:"ffmpeg_path"`
	FFmpegAvailable bool     `json:"ffmpeg_available"`
	Available       bool     `json:"available"`
	CurrentEnabled  bool     `json:"current_enabled"`
	CurrentStrength string   `json:"current_strength"`
	Status          string   `json:"status"`
	Details         string   `json:"details,omitempty"`
	Notes           []string `json:"notes,omitempty"`
}

type taterHDREnhancementCompatibilityProbe func(context.Context, string, string) error

func (s *Server) handleDetectHDREnhancementCompatibility(c *fiber.Ctx) error {
	if s.configManager == nil {
		return RespondServiceUnavailable(c, "Configuration management not available", "CONFIG_UNAVAILABLE")
	}
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondInternalError(c, "Configuration not available", "CONFIG_NOT_FOUND")
	}
	ffmpegPath := effectiveFFmpegPath(cfg.Transcoding.FFmpegPath)
	clearTaterHDREnhancementProbeCache(ffmpegPath)
	return RespondSuccess(c, detectHDREnhancementCompatibility(c.Context(), cfg.Transcoding, cfg.HDREnhancement))
}

func detectHDREnhancementCompatibility(
	ctx context.Context,
	transcoding config.TranscodingConfig,
	hdrConfig config.HDREnhancementConfig,
	probes ...taterHDREnhancementCompatibilityProbe,
) hdrEnhancementCompatibilityDetection {
	ffmpegPath := effectiveFFmpegPath(transcoding.FFmpegPath)
	currentEnabled := hdrConfig.Enabled != nil && *hdrConfig.Enabled
	strength := cleanTaterHDREnhancementStrength(hdrConfig.Strength)
	result := hdrEnhancementCompatibilityDetection{
		FFmpegPath:      ffmpegPath,
		CurrentEnabled:  currentEnabled,
		CurrentStrength: strength,
		Status:          "FFmpeg not found",
		Notes: []string{
			"Enhancement runs only for SDR on-demand video sent to an HDR10-capable Tater Tube TV player.",
			"Native HDR sources and SDR-only displays are left unchanged.",
		},
	}
	if _, err := exec.LookPath(ffmpegPath); err != nil {
		result.Details = "Install FFmpeg or configure its path before enabling HDR Enhancement."
		return result
	}
	result.FFmpegAvailable = true
	probe := probeTaterHDREnhancement
	if len(probes) > 0 && probes[0] != nil {
		probe = probes[0]
	}
	if err := probe(ctx, ffmpegPath, strength); err != nil {
		result.Status = "Unavailable"
		result.Details = conciseUpscalingProbeError(err)
		return result
	}
	result.Available = true
	result.Status = "Ready"
	result.Details = "FFmpeg libplacebo passed the SDR-to-HDR10 compatibility probe."
	return result
}
