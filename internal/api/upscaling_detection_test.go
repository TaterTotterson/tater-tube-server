package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestArtCNNQualityAllowsMoreTimeForShaderCompilation(t *testing.T) {
	require.Equal(t, taterArtCNNQualityProbeTimeout, taterAIUpscalerModels["artcnn-c4f32"].probeTimeout)
	require.Greater(t, taterAIUpscalerModels["artcnn-c4f32"].probeTimeout, taterUpscalingProbeTimeout)
	require.Zero(t, taterAIUpscalerModels["artcnn-c4f16"].probeTimeout)
}

func TestUpscalingProbeReportsTimeoutInsteadOfSignalKilled(t *testing.T) {
	ffmpegPath := filepath.Join(t.TempDir(), "ffmpeg")
	require.NoError(t, os.WriteFile(ffmpegPath, []byte("#!/bin/sh\nexec sleep 5\n"), 0o755))

	err := probeTaterUpscalingFilterWithTimeout(t.Context(), ffmpegPath, "scale=128:72", 25*time.Millisecond)
	require.Error(t, err)
	require.Contains(t, err.Error(), "probe timed out before completion")
	require.NotContains(t, err.Error(), "signal: killed")
}

func TestAIUpscaleFilterAddsPersistentShaderCache(t *testing.T) {
	filter := taterAIUpscaleFilter(
		3840,
		2160,
		"/tmp/tater shaders/FSRCNNX.glsl",
		"/tmp/tater cache/",
	)

	require.Contains(t, filter, "libplacebo=w=3840:h=2160")
	require.Contains(t, filter, "shader_cache='/tmp/tater cache/'")
	require.Contains(t, filter, "custom_shader_path='/tmp/tater shaders/FSRCNNX.glsl'")
	require.NotContains(t, taterAIUpscaleFilter(128, 72, "/tmp/model.glsl", ""), "shader_cache=")
}

func TestFFmpegShaderCacheSupportIsFeatureDetected(t *testing.T) {
	supportedFFmpeg := filepath.Join(t.TempDir(), "ffmpeg-with-cache")
	require.NoError(t, os.WriteFile(
		supportedFFmpeg,
		[]byte("#!/bin/sh\nprintf '   shader_cache      <string> Set shader cache path\\n'\n"),
		0o755,
	))
	unsupportedFFmpeg := filepath.Join(t.TempDir(), "ffmpeg-without-cache")
	require.NoError(t, os.WriteFile(
		unsupportedFFmpeg,
		[]byte("#!/bin/sh\nprintf 'libplacebo filter help\\n'\n"),
		0o755,
	))

	require.True(t, taterFFmpegSupportsAIShaderCache(t.Context(), supportedFFmpeg))
	require.False(t, taterFFmpegSupportsAIShaderCache(t.Context(), unsupportedFFmpeg))
}

func TestAIShaderCacheDirectoryUsesConfiguredPersistentPath(t *testing.T) {
	configured := filepath.Join(t.TempDir(), "persistent", "libplacebo", "v1")
	t.Setenv("TATER_AI_SHADER_CACHE_DIR", configured)

	directory, err := taterAIShaderCacheDirectory()
	require.NoError(t, err)
	require.Equal(t, configured, directory)
	info, err := os.Stat(directory)
	require.NoError(t, err)
	require.True(t, info.IsDir())
}

func TestPruneAIShaderCacheRemovesExpiredAndOldestFiles(t *testing.T) {
	directory := t.TempDir()
	now := time.Now()
	writeCacheFile := func(name string, size int, age time.Duration) string {
		t.Helper()
		path := filepath.Join(directory, name)
		require.NoError(t, os.WriteFile(path, make([]byte, size), 0o644))
		modified := now.Add(-age)
		require.NoError(t, os.Chtimes(path, modified, modified))
		return path
	}

	expired := writeCacheFile("expired", 4, 48*time.Hour)
	oldest := writeCacheFile("oldest", 6, 2*time.Hour)
	newest := writeCacheFile("newest", 6, time.Hour)

	require.NoError(t, pruneTaterAIShaderCache(directory, 8, 24*time.Hour, now))
	require.NoFileExists(t, expired)
	require.NoFileExists(t, oldest)
	require.FileExists(t, newest)
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
