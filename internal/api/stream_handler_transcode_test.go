package api

import (
	"net/http/httptest"
	"net/url"
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

func TestTaterSeekableVirtualInputURLUsesLoopbackRangeStream(t *testing.T) {
	req := httptest.NewRequest(
		"GET",
		"http://media.example/api/files/stream?path=queue%2FMovie.mkv&player_token=paired-token&transcode=video&profile=hdmi_1080p&start=182.5",
		nil,
	)
	inputURL := taterSeekableVirtualInputURL(req, &config.Config{
		Server: config.ServerConfig{Port: 8080},
	})

	parsed, err := url.Parse(inputURL)
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:8080/api/files/stream", parsed.Scheme+"://"+parsed.Host+parsed.Path)
	require.Equal(t, "queue/Movie.mkv", parsed.Query().Get("path"))
	require.Equal(t, "paired-token", parsed.Query().Get("player_token"))
	require.Equal(t, "1", parsed.Query().Get(taterInternalTranscodeInputQuery))
	require.Empty(t, parsed.Query().Get("transcode"))
	require.Empty(t, parsed.Query().Get("start"))
	require.Empty(t, parsed.Query().Get("profile"))
}

func TestTaterSeekableVirtualInputURLCarriesBearerToken(t *testing.T) {
	req := httptest.NewRequest(
		"GET", "http://media.example/api/files/stream?path=queue%2FMovie.mkv", nil,
	)
	req.Header.Set("Authorization", "Bearer paired-token")

	inputURL := taterSeekableVirtualInputURL(req, &config.Config{
		Server: config.ServerConfig{Port: 8080},
	})
	parsed, err := url.Parse(inputURL)
	require.NoError(t, err)
	require.Equal(t, "paired-token", parsed.Query().Get("player_token"))
	require.Equal(t, "1", parsed.Query().Get(taterInternalTranscodeInputQuery))
}

func TestTaterSeekableTranscodeInputKeepsLocalFilesystemPath(t *testing.T) {
	req := httptest.NewRequest(
		"GET",
		"http://media.example/api/tater/local/stream?category_id=movies&source=0&path=Movie.mkv&player_token=paired-token&transcode=audio&start=182.5",
		nil,
	)

	require.Equal(t, "/media/Movies/Movie.mkv",
		taterSeekableTranscodeInput(req, &config.Config{
			Server: config.ServerConfig{Port: 8080},
		}, "/media/Movies/Movie.mkv"),
	)
}

func TestTaterSeekableTranscodeInputUsesLoopbackForNZB(t *testing.T) {
	req := httptest.NewRequest(
		"GET",
		"http://media.example/api/files/stream?path=queue%2FMovie.mkv&player_token=paired-token&transcode=video&start=182.5",
		nil,
	)

	inputURL := taterSeekableTranscodeInput(req, &config.Config{
		Server: config.ServerConfig{Port: 8080},
	}, "queue/Movie.mkv")
	parsed, err := url.Parse(inputURL)
	require.NoError(t, err)
	require.Equal(t, "queue/Movie.mkv", parsed.Query().Get("path"))
	require.Equal(t, "paired-token", parsed.Query().Get("player_token"))
	require.Equal(t, "1", parsed.Query().Get(taterInternalTranscodeInputQuery))
}

func TestInternalTranscodeInputRequiresLoopbackRequest(t *testing.T) {
	loopback := httptest.NewRequest("GET", "http://127.0.0.1/api/files/stream?"+taterInternalTranscodeInputQuery+"=1", nil)
	loopback.RemoteAddr = "127.0.0.1:42000"
	require.True(t, isTaterInternalTranscodeInputRequest(loopback))

	external := httptest.NewRequest("GET", "http://media.example/api/files/stream?"+taterInternalTranscodeInputQuery+"=1", nil)
	external.RemoteAddr = "10.4.20.59:42000"
	require.False(t, isTaterInternalTranscodeInputRequest(external))

	unmarked := httptest.NewRequest("GET", "http://127.0.0.1/api/files/stream", nil)
	unmarked.RemoteAddr = "127.0.0.1:42000"
	require.False(t, isTaterInternalTranscodeInputRequest(unmarked))
}

func TestBuildFFmpegAudioSyncArgs(t *testing.T) {
	args := buildFFmpegAudioSyncArgs("", 0)
	joined := strings.Join(args, " ")

	require.Contains(t, joined, "-i pipe:0")
	require.Contains(t, joined, "-map 0:a:0 -vn -sn -dn")
	require.Contains(t, joined, "-map_metadata -1")
	require.Contains(t, joined, "-af aresample=48000:async=0:first_pts=0")
	require.Contains(t, joined, "-c:a pcm_s16le -ac 2 -ar 48000")
	require.Contains(t, joined, "-f wav pipe:1")
	require.NotContains(t, joined, "-c:v")
}

func TestBuildFFmpegAudioSyncArgsFileSeek(t *testing.T) {
	args := buildFFmpegAudioSyncArgs("/media/song.flac", 12.5)
	joined := strings.Join(args, " ")

	require.Contains(t, joined, "-ss 12.500 -i /media/song.flac")
	require.NotContains(t, joined, "-i pipe:0")
}

func TestBuildFFmpegAudioOnlyVideoArgsCopiesVideoAndTranscodesAudio(t *testing.T) {
	args := buildFFmpegAudioOnlyVideoArgs("192k", "", 0)
	joined := strings.Join(args, " ")

	require.Contains(t, joined, "-i pipe:0")
	require.Contains(t, joined, "-map 0:v:0 -map 0:a:0?")
	require.Contains(t, joined, "-c:v copy")
	require.Contains(t, joined, "-c:a aac -b:a 192k -ac 2 -ar 48000")
	require.Contains(t, joined, "-f matroska pipe:1")
	require.NotContains(t, joined, "libx264")
}

func TestBuildFFmpegAudioOnlyVideoArgsFileSeek(t *testing.T) {
	args := buildFFmpegAudioOnlyVideoArgs("", "/media/movie.mkv", 12.5)
	joined := strings.Join(args, " ")

	require.Contains(t, joined, "-noaccurate_seek -ss 12.500 -i /media/movie.mkv")
	require.Contains(t, joined, "-b:a 192k")
	require.NotContains(t, joined, "-i pipe:0")
}

func TestBuildFFmpegAudioOnlyVideoArgsSelectsRequestedTrack(t *testing.T) {
	args := buildFFmpegAudioOnlyVideoArgsWithTrack("192k", "", 0, 2)
	require.Contains(t, strings.Join(args, " "), "-map 0:a:2?")
}

func TestBuildFFmpegVideoOnlyArgsCopiesAudio(t *testing.T) {
	args := buildFFmpegVideoOnlyArgs(
		config.TranscodingConfig{}, transcodeProfiles["hdmi_1080p"],
		"none", "h264", "", 0,
	)
	joined := strings.Join(args, " ")

	require.Contains(t, joined, "-i pipe:0")
	require.Contains(t, joined, "-c:v libx264")
	require.Contains(t, joined, "-c:a copy")
	require.Contains(t, joined, "-f matroska pipe:1")
	require.NotContains(t, joined, "-c:a aac")
}

func TestBuildFFmpegVideoOnlyArgsFileSeek(t *testing.T) {
	args := buildFFmpegVideoOnlyArgs(
		config.TranscodingConfig{}, transcodeProfiles["hdmi_1080p"],
		"none", "h264", "/media/movie.mkv", 12.5,
	)
	joined := strings.Join(args, " ")

	require.Contains(t, joined, "-ss 12.500 -i /media/movie.mkv")
	require.NotContains(t, joined, "-i pipe:0")
}

func TestBuildFFmpegVideoOnlyArgsSelectsRequestedTrack(t *testing.T) {
	args := buildFFmpegVideoOnlyArgsWithToneMapFilterAndAudioTrack(
		config.TranscodingConfig{}, transcodeProfiles["hdmi_1080p"],
		"none", "h264", "", 0, "", "", "", 3,
	)
	require.Contains(t, strings.Join(args, " "), "-map 0:a:3?")
}

func TestBuildFFmpegVideoOnlyArgsToneMapsHDRAndCopiesAudio(t *testing.T) {
	args := buildFFmpegVideoOnlyArgsWithToneMap(
		config.TranscodingConfig{}, transcodeProfiles["hdmi_1080p"],
		"qsv", "h264", "/media/movie.mkv", 0, "hdr10", "sdr",
	)
	joined := strings.Join(args, " ")

	require.Contains(t, joined, "-vf tonemapx=tonemap=bt2390")
	require.Contains(t, joined, "transfer=bt709:matrix=bt709:primaries=bt709:range=tv")
	require.Contains(t, joined, ",scale=w=1920:h=1080")
	require.Contains(t, joined, "-color_primaries bt709 -color_trc bt709 -colorspace bt709 -color_range tv")
	require.Contains(t, joined, "-c:a copy")
}

func TestBuildFFmpegVideoOnlyArgsUsesPortableToneMapFallback(t *testing.T) {
	args := buildFFmpegVideoOnlyArgsWithToneMapFilter(
		config.TranscodingConfig{}, transcodeProfiles["hdmi_1080p"],
		"none", "h264", "/media/movie.mkv", 0, "hlg", "sdr", "zscale",
	)
	joined := strings.Join(args, " ")

	require.Contains(t, joined, "zscale=t=linear:npl=100")
	require.Contains(t, joined, "tonemap=tonemap=mobius")
	require.Contains(t, joined, "zscale=p=bt709:t=bt709:m=bt709:r=tv")
	require.Contains(t, joined, "-c:a copy")
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

func TestShouldTranscodeRequestAcceptsAudioOnlyMode(t *testing.T) {
	handler := &StreamHandler{
		configGetter: func() *config.Config {
			return &config.Config{}
		},
	}
	req := httptest.NewRequest("GET", "/api/files/stream/movie.mkv?transcode=audio", nil)

	require.True(t, isAudioOnlyTranscodeRequest(req))
	require.True(t, handler.shouldTranscode(req, "/media/movie.mkv"))
}

func TestShouldTranscodeRequestAcceptsVideoOnlyMode(t *testing.T) {
	handler := &StreamHandler{
		configGetter: func() *config.Config {
			return &config.Config{}
		},
	}
	req := httptest.NewRequest("GET", "/api/files/stream/movie.mkv?transcode=video", nil)

	require.True(t, isVideoOnlyTranscodeRequest(req))
	require.True(t, handler.shouldTranscode(req, "/media/movie.mkv"))
}

func TestRequestedTaterAudioTrack(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/files/stream/movie.mkv?tater_audio_track=4", nil)
	require.Equal(t, 4, requestedTaterAudioTrack(req))
	require.Equal(t, "0:a:4?", taterAudioMap(requestedTaterAudioTrack(req), true))

	req = httptest.NewRequest("GET", "/api/files/stream/movie.mkv?tater_audio_track=-1", nil)
	require.Zero(t, requestedTaterAudioTrack(req))
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

func TestShouldTranscodeAudioSyncProfileAcceptsMusic(t *testing.T) {
	handler := &StreamHandler{
		configGetter: func() *config.Config {
			return &config.Config{}
		},
	}
	for _, path := range []string{
		"/media/song.aiff",
		"/media/song.flac",
		"/media/song.mp3",
		"/media/song.m4a",
		"/media/song.wav",
	} {
		req := httptest.NewRequest(
			"GET",
			"/api/tater/local/stream?transcode=1&profile=audio_sync",
			nil,
		)
		require.True(t, handler.shouldTranscode(req, path), path)
	}
}

func TestShouldTranscodeMusicRequiresAudioSyncProfile(t *testing.T) {
	handler := &StreamHandler{
		configGetter: func() *config.Config {
			return &config.Config{}
		},
	}
	req := httptest.NewRequest("GET", "/api/tater/local/stream?transcode=1", nil)

	require.False(t, handler.shouldTranscode(req, "/media/song.flac"))
}
