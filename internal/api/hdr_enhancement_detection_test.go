package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/stretchr/testify/require"
)

func TestHDREnhancementDetectionReportsCompatibleFilter(t *testing.T) {
	ffmpegPath := filepath.Join(t.TempDir(), "ffmpeg")
	require.NoError(t, os.WriteFile(ffmpegPath, []byte(`#!/bin/sh
if [ "$2" = "-filters" ]; then
  printf ' .. libplacebo V->V libplacebo video filter\n'
fi
exit 0
`), 0o755))
	enabled := true
	result := detectHDREnhancementCompatibility(
		t.Context(),
		config.TranscodingConfig{FFmpegPath: ffmpegPath},
		config.HDREnhancementConfig{Enabled: &enabled, Strength: "vivid"},
		func(context.Context, string, string) error { return nil },
	)

	require.True(t, result.FFmpegAvailable)
	require.True(t, result.Available)
	require.True(t, result.CurrentEnabled)
	require.Equal(t, "vivid", result.CurrentStrength)
	require.Equal(t, "Ready", result.Status)
}

func TestHDREnhancementDetectionHandlesMissingFFmpeg(t *testing.T) {
	result := detectHDREnhancementCompatibility(
		t.Context(),
		config.TranscodingConfig{FFmpegPath: filepath.Join(t.TempDir(), "missing-ffmpeg")},
		config.HDREnhancementConfig{},
	)

	require.False(t, result.FFmpegAvailable)
	require.False(t, result.Available)
	require.Equal(t, "FFmpeg not found", result.Status)
}
