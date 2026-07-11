package api

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/stretchr/testify/require"
)

func TestBuildFFmpegTranscodeArgsSoftwareCRT(t *testing.T) {
	args := buildFFmpegTranscodeArgs(config.TranscodingConfig{}, transcodeProfiles["crt_480p"], "none", "", 0)
	joined := strings.Join(args, " ")

	require.Contains(t, joined, "-i pipe:0")
	require.Contains(t, joined, "-vf scale=w=640:h=480:force_original_aspect_ratio=decrease:force_divisible_by=2")
	require.Contains(t, joined, "-c:v libx264")
	require.Contains(t, joined, "-c:a aac")
	require.Contains(t, joined, "-f mpegts pipe:1")
}

func TestBuildFFmpegTranscodeArgsFileSeek(t *testing.T) {
	args := buildFFmpegTranscodeArgs(config.TranscodingConfig{}, transcodeProfiles["crt_480p"], "none", "/media/movie.mkv", 182.5)
	joined := strings.Join(args, " ")

	require.Contains(t, joined, "-ss 182.500 -i /media/movie.mkv")
	require.NotContains(t, joined, "-i pipe:0")
}

func TestBuildFFmpegTranscodeArgsVAAPI(t *testing.T) {
	args := buildFFmpegTranscodeArgs(config.TranscodingConfig{}, transcodeProfiles["hdmi_1080p"], "vaapi", "", 0)
	joined := strings.Join(args, " ")

	require.Contains(t, joined, "-vaapi_device /dev/dri/renderD128")
	require.Contains(t, joined, "-vf scale=w=1920:h=1080:force_original_aspect_ratio=decrease:force_divisible_by=2,format=nv12,hwupload")
	require.Contains(t, joined, "-c:v h264_vaapi")
}

func TestBuildFFmpegTranscodeArgsQSV(t *testing.T) {
	cfg := config.TranscodingConfig{HardwareDevice: "/dev/dri/renderD129"}
	args := buildFFmpegTranscodeArgs(cfg, transcodeProfiles["crt_480p"], "qsv", "", 0)
	joined := strings.Join(args, " ")

	require.Contains(t, joined, "-init_hw_device vaapi=va:/dev/dri/renderD129,driver=iHD")
	require.Contains(t, joined, "-init_hw_device qsv=qs@va")
	require.Contains(t, joined, "-filter_hw_device qs")
	require.NotContains(t, joined, "hwupload")
	require.NotContains(t, joined, "-preset veryfast")
	require.NotContains(t, joined, "-profile:v main")
	require.Contains(t, joined, "-vf scale=w=640:h=480:force_original_aspect_ratio=decrease:force_divisible_by=2,format=nv12")
	require.Contains(t, joined, "-c:v h264_qsv")
}

func TestFirstDRIRenderDeviceForVendor(t *testing.T) {
	dir := t.TempDir()
	intelRender := filepath.Join(dir, "renderD129")
	amdRender := filepath.Join(dir, "renderD128")
	require.NoError(t, os.WriteFile(intelRender, []byte{}, 0o644))
	require.NoError(t, os.WriteFile(amdRender, []byte{}, 0o644))

	device := firstDRIRenderDeviceForVendor([]drmGPUVendor{
		{RenderDevice: amdRender, Vendor: "amd"},
		{RenderDevice: intelRender, Vendor: "intel"},
	}, "intel")

	require.Equal(t, intelRender, device)
}

func TestFirstDRIRenderDeviceForVendorSkipsUnmappedDevice(t *testing.T) {
	device := firstDRIRenderDeviceForVendor([]drmGPUVendor{
		{RenderDevice: filepath.Join(t.TempDir(), "renderD129"), Vendor: "intel"},
	}, "intel")

	require.Empty(t, device)
}

func TestCandidateDRIRenderDevicesPrefersConfiguredDeviceThenScansVisibleDevices(t *testing.T) {
	dir := t.TempDir()
	intelRender := filepath.Join(dir, "renderD129")
	require.NoError(t, os.WriteFile(intelRender, []byte{}, 0o644))

	candidates := candidateDRIRenderDevices([]drmGPUVendor{
		{RenderDevice: intelRender, Vendor: "intel"},
	}, []string{"intel"}, "/dev/dri/renderD130")

	require.GreaterOrEqual(t, len(candidates), 2)
	require.Equal(t, "/dev/dri/renderD130", candidates[0])
	require.Equal(t, intelRender, candidates[1])
}

func TestCandidateDRIRenderDevicesPrefersVendorDevice(t *testing.T) {
	dir := t.TempDir()
	intelRender := filepath.Join(dir, "renderD129")
	amdRender := filepath.Join(dir, "renderD128")
	require.NoError(t, os.WriteFile(intelRender, []byte{}, 0o644))
	require.NoError(t, os.WriteFile(amdRender, []byte{}, 0o644))

	candidates := candidateDRIRenderDevices([]drmGPUVendor{
		{RenderDevice: amdRender, Vendor: "amd"},
		{RenderDevice: intelRender, Vendor: "intel"},
	}, []string{"intel", "amd"}, "")

	require.GreaterOrEqual(t, len(candidates), 2)
	require.Equal(t, intelRender, candidates[0])
	require.Equal(t, amdRender, candidates[1])
}

func TestShouldTranscodeRequestCanForceOn(t *testing.T) {
	handler := &StreamHandler{
		configGetter: func() *config.Config {
			return &config.Config{}
		},
	}
	req := httptest.NewRequest("GET", "/api/files/stream/movie.mkv?transcode=1", nil)

	require.True(t, handler.shouldTranscode(req, "/media/movie.mkv"))
}

func TestShouldTranscodeRequestCanForceOff(t *testing.T) {
	enabled := true
	handler := &StreamHandler{
		configGetter: func() *config.Config {
			return &config.Config{Transcoding: config.TranscodingConfig{Enabled: &enabled}}
		},
	}
	req := httptest.NewRequest("GET", "/api/files/stream/movie.mkv?transcode=0", nil)

	require.False(t, handler.shouldTranscode(req, "/media/movie.mkv"))
}

func TestShouldTranscodeDirectPlaysWithoutRequestOverride(t *testing.T) {
	enabled := true
	handler := &StreamHandler{
		configGetter: func() *config.Config {
			return &config.Config{Transcoding: config.TranscodingConfig{Enabled: &enabled}}
		},
	}
	req := httptest.NewRequest("GET", "/api/files/stream/movie.mkv", nil)

	require.False(t, handler.shouldTranscode(req, "/media/movie.mkv"))
}

func TestShouldTranscodeIgnoresUnsupportedExtensions(t *testing.T) {
	handler := &StreamHandler{
		configGetter: func() *config.Config {
			return &config.Config{}
		},
	}
	req := httptest.NewRequest("GET", "/api/files/stream/subtitle.srt?transcode=1", nil)

	require.False(t, handler.shouldTranscode(req, "/media/subtitle.srt"))
}
