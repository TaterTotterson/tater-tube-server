package api

import (
	"strings"
	"testing"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/stretchr/testify/require"
)

func TestBuildFFmpegTranscodeArgsSoftwareCRT(t *testing.T) {
	args := buildFFmpegTranscodeArgs(config.TranscodingConfig{}, transcodeProfiles["crt_480p"], "none")
	joined := strings.Join(args, " ")

	require.Contains(t, joined, "-i pipe:0")
	require.Contains(t, joined, "-vf scale=w=640:h=480:force_original_aspect_ratio=decrease:force_divisible_by=2")
	require.Contains(t, joined, "-c:v libx264")
	require.Contains(t, joined, "-c:a aac")
	require.Contains(t, joined, "-f mpegts pipe:1")
}

func TestBuildFFmpegTranscodeArgsVAAPI(t *testing.T) {
	args := buildFFmpegTranscodeArgs(config.TranscodingConfig{}, transcodeProfiles["hdmi_1080p"], "vaapi")
	joined := strings.Join(args, " ")

	require.Contains(t, joined, "-vaapi_device /dev/dri/renderD128")
	require.Contains(t, joined, "-vf scale=w=1920:h=1080:force_original_aspect_ratio=decrease:force_divisible_by=2,format=nv12,hwupload")
	require.Contains(t, joined, "-c:v h264_vaapi")
}
