package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/stretchr/testify/require"
)

func TestUpscalingDetectionRecommendsSafeAIWhenCompatible(t *testing.T) {
	ffmpegPath := writeUpscalingDetectionFFmpeg(t, true)
	result := detectUpscalingCompatibility(
		t.Context(),
		config.TranscodingConfig{FFmpegPath: ffmpegPath},
		config.UpscalingConfig{Mode: "standard", Model: "artcnn-c4f32"},
		func(context.Context, string, taterAIUpscalerModel) error { return nil },
	)

	require.True(t, result.FFmpegAvailable)
	require.True(t, result.Standard.Available)
	require.Equal(t, "spline36", result.Standard.Method)
	require.Equal(t, "auto", result.RecommendedMode)
	require.Equal(t, defaultTaterAIUpscalerModel, result.RecommendedModel)
	require.Len(t, result.Models, len(taterAIUpscalerModelOrder))
	for _, model := range result.Models {
		require.True(t, model.Available)
		require.Equal(t, "Compatible", model.Status)
	}
}

func TestUpscalingDetectionRecommendsStandardWhenAIIsUnavailable(t *testing.T) {
	ffmpegPath := writeUpscalingDetectionFFmpeg(t, true)
	result := detectUpscalingCompatibility(
		t.Context(),
		config.TranscodingConfig{FFmpegPath: ffmpegPath},
		config.UpscalingConfig{Mode: "ai", Model: "fsrcnnx-16"},
		func(context.Context, string, taterAIUpscalerModel) error {
			return errors.New("Vulkan device unavailable")
		},
	)

	require.Equal(t, "standard", result.RecommendedMode)
	require.Empty(t, result.RecommendedModel)
	for _, model := range result.Models {
		require.False(t, model.Available)
		require.Equal(t, "Unavailable", model.Status)
		require.Contains(t, model.Details, "Vulkan device unavailable")
	}
}

func TestUpscalingDetectionReportsPortableStandardFallback(t *testing.T) {
	ffmpegPath := writeUpscalingDetectionFFmpeg(t, false)
	result := detectUpscalingCompatibility(
		t.Context(),
		config.TranscodingConfig{FFmpegPath: ffmpegPath},
		config.UpscalingConfig{},
		func(context.Context, string, taterAIUpscalerModel) error {
			return errors.New("libplacebo unavailable")
		},
	)

	require.True(t, result.Standard.Available)
	require.Equal(t, "Portable fallback", result.Standard.Status)
	require.Equal(t, "spline", result.Standard.Method)
	require.Equal(t, "standard", result.RecommendedMode)
	require.Empty(t, result.RecommendedModel)
}

func TestUpscalingDetectionHandlesMissingFFmpeg(t *testing.T) {
	result := detectUpscalingCompatibility(
		t.Context(),
		config.TranscodingConfig{FFmpegPath: filepath.Join(t.TempDir(), "missing-ffmpeg")},
		config.UpscalingConfig{Mode: "ai", Model: "artcnn-c4f32"},
		nil,
	)

	require.False(t, result.FFmpegAvailable)
	require.Equal(t, "off", result.RecommendedMode)
	require.False(t, result.Standard.Available)
	require.Len(t, result.Models, len(taterAIUpscalerModelOrder))
}

func writeUpscalingDetectionFFmpeg(t *testing.T, includeZscale bool) string {
	t.Helper()
	filters := " .SC scale V->V Apply resize\n"
	if includeZscale {
		filters += " .SC zscale V->V Apply resize\n"
	}
	script := "#!/bin/sh\n" +
		"case \" $* \" in\n" +
		"  *\" -filters \"*) printf '" + filters + "' ;;\n" +
		"esac\n" +
		"exit 0\n"
	path := filepath.Join(t.TempDir(), "ffmpeg")
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}
