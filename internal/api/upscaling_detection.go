package api

import (
	"context"
	"os/exec"
	"strings"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
)

type upscalingCompatibilityOption struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
	Status    string `json:"status"`
	Details   string `json:"details,omitempty"`
	Method    string `json:"method,omitempty"`
}

type upscalingCompatibilityDetection struct {
	FFmpegPath       string                         `json:"ffmpeg_path"`
	FFmpegAvailable  bool                           `json:"ffmpeg_available"`
	CurrentMode      string                         `json:"current_mode"`
	CurrentModel     string                         `json:"current_model"`
	RecommendedMode  string                         `json:"recommended_mode"`
	RecommendedModel string                         `json:"recommended_model,omitempty"`
	Standard         upscalingCompatibilityOption   `json:"standard"`
	Models           []upscalingCompatibilityOption `json:"models"`
	Notes            []string                       `json:"notes,omitempty"`
}

type taterAICompatibilityProbe func(context.Context, string, taterAIUpscalerModel) error

func (s *Server) handleDetectUpscalingCompatibility(c *fiber.Ctx) error {
	if s.configManager == nil {
		return RespondServiceUnavailable(c, "Configuration management not available", "CONFIG_UNAVAILABLE")
	}
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondInternalError(c, "Configuration not available", "CONFIG_NOT_FOUND")
	}

	ffmpegPath := effectiveFFmpegPath(cfg.Transcoding.FFmpegPath)
	clearTaterAIUpscalerProbeCache(ffmpegPath)
	result := detectUpscalingCompatibility(
		c.Context(), cfg.Transcoding, cfg.Upscaling,
		func(ctx context.Context, path string, model taterAIUpscalerModel) error {
			_, err := prepareTaterAIUpscaler(ctx, path, model)
			return err
		},
	)
	return RespondSuccess(c, result)
}

func detectUpscalingCompatibility(
	ctx context.Context,
	transcoding config.TranscodingConfig,
	upscaling config.UpscalingConfig,
	probeAI taterAICompatibilityProbe,
) upscalingCompatibilityDetection {
	ffmpegPath := effectiveFFmpegPath(transcoding.FFmpegPath)
	currentMode := cleanTaterUpscalingMode(upscaling.Mode)
	currentModel := cleanTaterAIUpscalerModel(upscaling.Model)
	result := upscalingCompatibilityDetection{
		FFmpegPath:      ffmpegPath,
		CurrentMode:     currentMode,
		CurrentModel:    currentModel,
		RecommendedMode: "off",
		Standard: upscalingCompatibilityOption{
			ID: "standard", Label: "Standard upscaling", Status: "FFmpeg not found",
		},
		Models: make([]upscalingCompatibilityOption, 0, len(taterAIUpscalerModelOrder)),
	}

	if _, err := exec.LookPath(ffmpegPath); err != nil {
		for _, modelID := range taterAIUpscalerModelOrder {
			model := taterAIUpscalerModels[modelID]
			result.Models = append(result.Models, upscalingCompatibilityOption{
				ID: model.id, Label: model.name, Status: "FFmpeg not found",
			})
		}
		result.Notes = append(result.Notes, "Install FFmpeg or set its path before enabling server-side upscaling.")
		return result
	}
	result.FFmpegAvailable = true
	result.Standard = detectStandardUpscalingCompatibility(ctx, ffmpegPath)

	if probeAI == nil {
		probeAI = func(ctx context.Context, path string, model taterAIUpscalerModel) error {
			_, err := prepareTaterAIUpscaler(ctx, path, model)
			return err
		}
	}
	for _, modelID := range taterAIUpscalerModelOrder {
		model := taterAIUpscalerModels[modelID]
		option := upscalingCompatibilityOption{ID: model.id, Label: model.name, Method: "ai"}
		if err := probeAI(ctx, ffmpegPath, model); err != nil {
			option.Status = "Unavailable"
			option.Details = conciseUpscalingProbeError(err)
		} else {
			option.Available = true
			option.Status = "Compatible"
			option.Details = "The shader and Vulkan/libplacebo compatibility probe passed."
		}
		result.Models = append(result.Models, option)
	}

	if model := firstAvailableUpscalingModel(result.Models, defaultTaterAIUpscalerModel); model != nil {
		result.RecommendedMode = "auto"
		result.RecommendedModel = model.ID
	} else if model := firstAvailableUpscalingModel(result.Models, ""); model != nil {
		result.RecommendedMode = "auto"
		result.RecommendedModel = model.ID
	} else if result.Standard.Available {
		result.RecommendedMode = "standard"
	}
	result.Notes = append(result.Notes,
		"Compatibility confirms that a model can initialize; playback speed still depends on resolution and current GPU load.",
		"AI remains limited to compatible SDR upscales between 1.3x and 2x and falls back safely when media is not eligible.",
	)
	return result
}

func detectStandardUpscalingCompatibility(ctx context.Context, ffmpegPath string) upscalingCompatibilityOption {
	option := upscalingCompatibilityOption{ID: "standard", Label: "Standard upscaling"}
	var zscaleErr error
	if taterTVFFmpegHasFilter(ctx, ffmpegPath, "zscale") {
		zscaleErr = probeTaterUpscalingFilter(ctx, ffmpegPath, "zscale=w=128:h=72:filter=spline36")
		if zscaleErr == nil {
			option.Available = true
			option.Status = "Ready"
			option.Method = "spline36"
			option.Details = "Spline36 is available through FFmpeg zscale."
			return option
		}
	}

	if err := probeTaterUpscalingFilter(ctx, ffmpegPath, "scale=w=128:h=72:flags=spline"); err == nil {
		option.Available = true
		option.Status = "Portable fallback"
		option.Method = "spline"
		option.Details = "Portable Spline is available; zscale Spline36 is not usable."
		if zscaleErr != nil {
			option.Details += " " + conciseUpscalingProbeError(zscaleErr)
		}
		return option
	} else {
		option.Status = "Unavailable"
		option.Details = conciseUpscalingProbeError(err)
		return option
	}
}

func firstAvailableUpscalingModel(options []upscalingCompatibilityOption, id string) *upscalingCompatibilityOption {
	for i := range options {
		if options[i].Available && (id == "" || options[i].ID == id) {
			return &options[i]
		}
	}
	return nil
}

func conciseUpscalingProbeError(err error) string {
	if err == nil {
		return ""
	}
	details := strings.Join(strings.Fields(err.Error()), " ")
	const maxDetails = 500
	if len(details) > maxDetails {
		details = details[:maxDetails] + "..."
	}
	return details
}

func clearTaterAIUpscalerProbeCache(ffmpegPath string) {
	ffmpegPath = effectiveFFmpegPath(ffmpegPath)
	prefix := ffmpegPath + "\x00"
	taterAIUpscalerProbes.Lock()
	defer taterAIUpscalerProbes.Unlock()
	for key := range taterAIUpscalerProbes.byFFmpeg {
		if strings.HasPrefix(key, prefix) {
			delete(taterAIUpscalerProbes.byFFmpeg, key)
		}
	}
}
