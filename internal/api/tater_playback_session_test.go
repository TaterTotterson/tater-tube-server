package api

import (
	"net/url"
	"testing"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/stretchr/testify/require"
)

func TestBuildTaterPlaybackPlanDirect(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mkv",
		Profile:   "hdmi_1080p",
		Capabilities: taterPlaybackCapabilities{
			VideoCodecs:      []string{"h264", "hevc"},
			AudioCodecs:      []string{"aac", "eac3"},
			MaxWidth:         3840,
			MaxHeight:        2160,
			MaxAudioChannels: 8,
		},
	}, taterPlaybackMediaInfo{
		VideoCodec: "hevc", Width: 3840, Height: 2160,
		AudioCodec: "eac3", AudioChannels: 6,
	})

	require.Equal(t, "direct", plan.Mode)
	require.Equal(t, "direct", plan.VideoMode)
	require.Equal(t, "direct", plan.AudioMode)
	require.NotContains(t, plan.StreamURL, "transcode=")
}

func TestBuildTaterPlaybackPlanRemuxesUnsupportedContainerForAppleTV(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mkv",
		Profile:   "hdmi_4k",
		Capabilities: taterPlaybackCapabilities{
			Platform:                 "tvos",
			Engine:                   "avkit",
			PreferredStreamContainer: "mpegts",
			Containers:               []string{"mp4", "mpegts", "hls"},
			VideoCodecs:              []string{"h264", "hevc"},
			AudioCodecs:              []string{"aac", "ac3", "eac3"},
			MaxWidth:                 3840,
			MaxHeight:                2160,
			MaxAudioChannels:         8,
		},
	}, taterPlaybackMediaInfo{
		Container: "mkv", VideoCodec: "hevc", Width: 3840, Height: 2160,
		AudioCodec: "eac3", AudioChannels: 6,
	})

	require.Equal(t, "remux", plan.Mode)
	require.Equal(t, "direct", plan.VideoMode)
	require.Equal(t, "direct", plan.AudioMode)
	require.Equal(t, "mpegts", plan.OutputContainer)
	require.Contains(t, plan.Reason, "repackaging")
	query := playbackPlanQuery(t, plan.StreamURL)
	require.Equal(t, "remux", query.Get("transcode"))
	require.Equal(t, "mpegts", query.Get("tater_output_container"))
}

func TestBuildTaterPlaybackPlanUsesHLSForNativeAppleTVTranscode(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mkv",
		Profile:   "hdmi_1080p",
		Capabilities: taterPlaybackCapabilities{
			Platform:                 "tvos",
			Engine:                   "avkit",
			PreferredStreamContainer: "hls",
			Containers:               []string{"mp4", "mpegts", "hls"},
			VideoCodecs:              []string{"h264", "hevc"},
			AudioCodecs:              []string{"aac", "ac3", "eac3"},
			MaxWidth:                 1920,
			MaxHeight:                1080,
			MaxAudioChannels:         8,
			AudioDownmix:             true,
		},
	}, taterPlaybackMediaInfo{
		Container: "mkv", VideoCodec: "av1", Width: 3840, Height: 2160,
		AudioCodec: "dts", AudioChannels: 6,
	})

	require.Equal(t, "full_transcode", plan.Mode)
	require.Equal(t, "hls", plan.OutputContainer)
	query := playbackPlanQuery(t, plan.StreamURL)
	require.Equal(t, "1", query.Get("transcode"))
	require.Equal(t, "hls", query.Get("tater_output_container"))
}

func TestBuildTaterPlaybackPlanKeepsSupportedAppleTVContainerDirect(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mp4",
		Capabilities: taterPlaybackCapabilities{
			PreferredStreamContainer: "mpegts",
			Containers:               []string{"mp4", "mpegts", "hls"},
			VideoCodecs:              []string{"h264"},
			AudioCodecs:              []string{"aac"},
		},
	}, taterPlaybackMediaInfo{
		Container: "mp4", VideoCodec: "h264", AudioCodec: "aac",
	})

	require.Equal(t, "direct", plan.Mode)
	require.Equal(t, "mp4", plan.OutputContainer)
	require.NotContains(t, plan.StreamURL, "transcode=")
}

func TestBuildTaterPlaybackPlanPrefersBestEnglishAudioTrack(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mkv",
		Capabilities: taterPlaybackCapabilities{
			VideoCodecs:  []string{"h264"},
			AudioCodecs:  []string{"aac", "truehd"},
			AudioDownmix: true,
		},
	}, taterPlaybackMediaInfo{
		VideoCodec: "h264",
		AudioTracks: []taterPlaybackAudioTrack{
			{Index: 0, Codec: "truehd", Channels: 8, Language: "jpn", Default: true},
			{Index: 1, Codec: "aac", Channels: 2, Language: "eng"},
			{Index: 2, Codec: "truehd", Channels: 8, Language: "en-US"},
			{Index: 3, Codec: "truehd", Channels: 8, Language: "eng", Commentary: true},
		},
	})

	require.Equal(t, 2, plan.SelectedAudioTrack)
	require.Equal(t, "truehd", plan.Source.AudioCodec)
	require.Equal(t, 8, plan.Source.AudioChannels)
	require.Equal(t, "2", playbackPlanQuery(t, plan.StreamURL).Get("tater_audio_track"))
}

func TestBuildTaterPlaybackPlanPrefersCompatibleEnglishSurroundTrack(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mkv",
		Capabilities: taterPlaybackCapabilities{
			VideoCodecs:      []string{"h264"},
			AudioCodecs:      []string{"aac", "ac3", "eac3"},
			MaxAudioChannels: 6,
		},
	}, taterPlaybackMediaInfo{
		VideoCodec: "h264",
		AudioTracks: []taterPlaybackAudioTrack{
			{Index: 0, Codec: "truehd", Channels: 8, Language: "eng", Default: true},
			{Index: 1, Codec: "ac3", Channels: 6, Language: "eng"},
			{Index: 2, Codec: "aac", Channels: 2, Language: "eng"},
		},
	})

	require.Equal(t, 1, plan.SelectedAudioTrack)
	require.Equal(t, "ac3", plan.Source.AudioCodec)
	require.Equal(t, 6, plan.Source.AudioChannels)
	require.Equal(t, "direct", plan.AudioMode)
	require.NotContains(t, plan.StreamURL, "transcode=")
}

func TestBuildTaterPlaybackPlanHonorsRequestedAudioTrack(t *testing.T) {
	requested := 0
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL:  "http://tube.local/api/tater/local/stream?path=movie.mkv",
		AudioTrack: &requested,
		Capabilities: taterPlaybackCapabilities{
			VideoCodecs: []string{"h264"},
			AudioCodecs: []string{"aac"},
		},
	}, taterPlaybackMediaInfo{
		VideoCodec: "h264",
		AudioTracks: []taterPlaybackAudioTrack{
			{Index: 0, Codec: "aac", Channels: 2, Language: "spa"},
			{Index: 1, Codec: "aac", Channels: 2, Language: "eng"},
		},
	})

	require.Equal(t, 0, plan.SelectedAudioTrack)
	require.Equal(t, "spa", plan.Source.AudioTracks[0].Language)
	require.Equal(t, "0", playbackPlanQuery(t, plan.StreamURL).Get("tater_audio_track"))
}

func TestBuildTaterPlaybackPlanAudioOnlyTranscode(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mkv",
		Capabilities: taterPlaybackCapabilities{
			VideoCodecs:      []string{"h264"},
			AudioCodecs:      []string{"aac"},
			MaxAudioChannels: 2,
		},
	}, taterPlaybackMediaInfo{
		VideoCodec: "h264", AudioCodec: "truehd", AudioChannels: 8,
	})

	require.Equal(t, "audio_transcode", plan.Mode)
	require.Equal(t, "direct", plan.VideoMode)
	require.Equal(t, "transcode", plan.AudioMode)
	require.Equal(t, "aac", plan.AudioCodec)
	require.Equal(t, "audio", playbackPlanQuery(t, plan.StreamURL).Get("transcode"))
}

func TestBuildTaterPlaybackPlanPreservesAppleTVSurroundWhenTranscodingAudio(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mkv",
		Profile:   "hdmi_4k",
		Capabilities: taterPlaybackCapabilities{
			Platform:         "tvos",
			VideoCodecs:      []string{"h264"},
			AudioCodecs:      []string{"aac", "ac3", "eac3"},
			MaxAudioChannels: 8,
			AudioDownmix:     true,
		},
	}, taterPlaybackMediaInfo{
		VideoCodec: "h264", AudioCodec: "dts_hd", AudioChannels: 8,
	})

	require.Equal(t, "audio_transcode", plan.Mode)
	require.Equal(t, "aac", plan.AudioCodec)
	require.Equal(t, 6, plan.OutputAudioChannels)
	require.Equal(t, "6", playbackPlanQuery(t, plan.StreamURL).Get("tater_audio_channels"))
	require.Contains(t, plan.QualityLabel, "AAC 5.1")
}

func TestBuildTaterPlaybackPlanDoesNotChangeOtherPlayersAudioContract(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mkv",
		Capabilities: taterPlaybackCapabilities{
			Platform:         "linux",
			VideoCodecs:      []string{"h264"},
			AudioCodecs:      []string{"aac"},
			MaxAudioChannels: 8,
		},
	}, taterPlaybackMediaInfo{
		VideoCodec: "h264", AudioCodec: "dts_hd", AudioChannels: 8,
	})

	require.Equal(t, "audio_transcode", plan.Mode)
	require.Zero(t, plan.OutputAudioChannels)
	require.Empty(t, playbackPlanQuery(t, plan.StreamURL).Get("tater_audio_channels"))
}

func TestBuildTaterPlaybackPlanUsesAppleTVContainerForAudioOnlyTranscode(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mkv",
		Capabilities: taterPlaybackCapabilities{
			PreferredStreamContainer: "mpegts",
			Containers:               []string{"mp4", "mpegts", "hls"},
			VideoCodecs:              []string{"h264"},
			AudioCodecs:              []string{"aac"},
			MaxAudioChannels:         2,
		},
	}, taterPlaybackMediaInfo{
		Container: "mkv", VideoCodec: "h264", AudioCodec: "truehd", AudioChannels: 8,
	})

	require.Equal(t, "audio_transcode", plan.Mode)
	require.Equal(t, "mpegts", plan.OutputContainer)
	require.Equal(t, "mpegts", playbackPlanQuery(t, plan.StreamURL).Get("tater_output_container"))
}

func TestBuildTaterPlaybackPlanLetsNativePlayerDownmix(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mkv",
		Capabilities: taterPlaybackCapabilities{
			AudioCodecs:      []string{"dts_hd"},
			VideoCodecs:      []string{"vc1"},
			MaxAudioChannels: 2,
			AudioDownmix:     true,
		},
	}, taterPlaybackMediaInfo{
		VideoCodec: "vc1", AudioCodec: "dts_hd", AudioChannels: 6,
	})

	require.Equal(t, "direct", plan.Mode)
	require.Equal(t, "direct", plan.VideoMode)
	require.Equal(t, "direct", plan.AudioMode)
}

func TestCleanTaterCodecNameNormalizesDTSHD(t *testing.T) {
	require.Equal(t, "dts_hd", cleanTaterCodecName("DTS-HD MA"))
	require.Equal(t, "dts_hd", cleanTaterCodecName("dts_hd"))
}

func TestBuildTaterPlaybackPlanVideoOnlyPreservesBitstreamAudio(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mkv",
		Capabilities: taterPlaybackCapabilities{
			VideoCodecs:          []string{"h264"},
			AudioCodecs:          []string{"aac"},
			AudioPassthrough:     []string{"eac3", "truehd"},
			PassthroughAvailable: true,
		},
	}, taterPlaybackMediaInfo{
		VideoCodec: "hevc", AudioCodec: "truehd", AudioChannels: 8,
	})

	require.Equal(t, "video_transcode", plan.Mode)
	require.Equal(t, "transcode", plan.VideoMode)
	require.Equal(t, "bitstream", plan.AudioMode)
	require.Equal(t, "video", playbackPlanQuery(t, plan.StreamURL).Get("transcode"))
	require.Equal(t, "truehd", playbackPlanQuery(t, plan.StreamURL).Get("audio_codec"))
	require.Contains(t, plan.QualityLabel, "Audio Bitstream")
}

func TestBuildTaterPlaybackPlanReports4KTo1080p(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/files/stream?path=queue%2FMovie.mkv",
		Profile:   "hdmi_1080p",
		Capabilities: taterPlaybackCapabilities{
			VideoCodecs:      []string{"h264"},
			AudioCodecs:      []string{"eac3"},
			MaxWidth:         1920,
			MaxHeight:        1080,
			MaxAudioChannels: 6,
		},
	}, taterPlaybackMediaInfo{
		VideoCodec: "hevc", Width: 3840, Height: 2160,
		AudioCodec: "eac3", AudioChannels: 6,
	})

	require.Equal(t, "video_transcode", plan.Mode)
	require.Equal(t, 1920, plan.OutputWidth)
	require.Equal(t, 1080, plan.OutputHeight)
	require.Equal(t, "4K → 1080p", plan.ResolutionLabel)
	query := playbackPlanQuery(t, plan.StreamURL)
	require.Equal(t, "3840", query.Get("tater_source_width"))
	require.Equal(t, "2160", query.Get("tater_source_height"))
	require.Equal(t, "1920", query.Get("tater_output_width"))
	require.Equal(t, "1080", query.Get("tater_output_height"))
}

func TestBuildTaterPlaybackPlanFullTranscode(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mkv",
		Capabilities: taterPlaybackCapabilities{
			VideoCodecs: []string{"h264"},
			AudioCodecs: []string{"aac"},
		},
	}, taterPlaybackMediaInfo{VideoCodec: "av1", AudioCodec: "dts"})

	require.Equal(t, "full_transcode", plan.Mode)
	require.Equal(t, "transcode", plan.VideoMode)
	require.Equal(t, "transcode", plan.AudioMode)
	require.Equal(t, "1", playbackPlanQuery(t, plan.StreamURL).Get("transcode"))
}

func TestBuildTaterPlaybackPlanUnknownAudioHonorsCompatibilityMode(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/files/stream?path=remote.mkv",
		Capabilities: taterPlaybackCapabilities{
			CompatibilityMode: true,
		},
	}, taterPlaybackMediaInfo{})

	require.Equal(t, "audio_transcode", plan.Mode)
	require.Equal(t, "audio", playbackPlanQuery(t, plan.StreamURL).Get("transcode"))
}

func TestBuildTaterPlaybackPlanHDR10DirectWhenDecoderAndDisplaySupportIt(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mkv",
		Capabilities: taterPlaybackCapabilities{
			VideoCodecs:       []string{"hevc"},
			AudioCodecs:       []string{"aac"},
			VideoHDRFormats:   []string{"hdr10", "hlg"},
			DisplayHDRFormats: []string{"hdr10"},
			DisplayHDREnabled: true,
			MaxVideoBitDepth:  10,
		},
	}, taterPlaybackMediaInfo{
		VideoCodec: "hevc", VideoRange: "hdr10", VideoBitDepth: 10,
		AudioCodec: "aac", AudioChannels: 2,
	})

	require.Equal(t, "direct", plan.Mode)
	require.Equal(t, "hdr10", plan.OutputVideoRange)
	require.False(t, plan.ToneMapped)
	require.Contains(t, plan.QualityLabel, "HDR10 Direct")
	require.Equal(t, "hdr10", playbackPlanQuery(t, plan.StreamURL).Get("tater_source_video_range"))
	require.Empty(t, playbackPlanQuery(t, plan.StreamURL).Get("tater_tone_map"))
}

func TestBuildTaterPlaybackPlanHDR10ToneMapsVideoAndPreservesAudio(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mkv",
		Capabilities: taterPlaybackCapabilities{
			VideoCodecs:      []string{"hevc"},
			AudioCodecs:      []string{"eac3"},
			MaxVideoBitDepth: 8,
		},
	}, taterPlaybackMediaInfo{
		VideoCodec: "hevc", VideoRange: "hdr10", VideoBitDepth: 10,
		AudioCodec: "eac3", AudioChannels: 6,
	})

	require.Equal(t, "video_transcode", plan.Mode)
	require.Equal(t, "transcode", plan.VideoMode)
	require.Equal(t, "direct", plan.AudioMode)
	require.Equal(t, "sdr", plan.OutputVideoRange)
	require.True(t, plan.ToneMapped)
	require.Contains(t, plan.QualityLabel, "HDR10 → SDR Tone Map")
	query := playbackPlanQuery(t, plan.StreamURL)
	require.Equal(t, "1", query.Get("tater_tone_map"))
	require.Equal(t, "hdr10", query.Get("tater_source_video_range"))
	require.Equal(t, "sdr", query.Get("tater_output_video_range"))
	require.Equal(t, "eac3", query.Get("audio_codec"))
}

func TestBuildTaterPlaybackPlanProfile7DolbyVisionUsesSafeSDRToneMap(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mkv",
		Capabilities: taterPlaybackCapabilities{
			CapabilityVersion:   5,
			Platform:            "tvos",
			VideoCodecs:         []string{"hevc"},
			AudioCodecs:         []string{"aac"},
			VideoHDRFormats:     []string{"hdr10", "dolby_vision"},
			DisplayHDRFormats:   []string{"hdr10", "dolby_vision"},
			DisplayHDREnabled:   true,
			MaxVideoBitDepth:    10,
			DolbyVisionProfiles: []int{5},
		},
	}, taterPlaybackMediaInfo{
		VideoCodec: "hevc", VideoRange: "dolby_vision", VideoBitDepth: 10,
		DolbyVisionProfile: 7, AudioCodec: "aac", AudioChannels: 2,
	})

	require.Equal(t, "video_transcode", plan.Mode)
	require.Equal(t, "sdr", plan.OutputVideoRange)
	require.True(t, plan.ToneMapped)
	require.Contains(t, plan.QualityLabel, "Dolby Vision → SDR Tone Map")
}

func TestBuildTaterPlaybackPlanProfile8DolbyVisionUsesHDR10BaseLayer(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mkv",
		Profile:   "hdmi_4k",
		Capabilities: taterPlaybackCapabilities{
			CapabilityVersion:        5,
			Platform:                 "tvos",
			Containers:               []string{"mp4", "hls"},
			VideoCodecs:              []string{"hevc"},
			AudioCodecs:              []string{"aac"},
			VideoHDRFormats:          []string{"hdr10", "dolby_vision"},
			DisplayHDRFormats:        []string{"hdr10", "dolby_vision"},
			DisplayHDREnabled:        true,
			MaxVideoBitDepth:         10,
			DolbyVisionProfiles:      []int{5},
			PreferredStreamContainer: "hls",
		},
	}, taterPlaybackMediaInfo{
		Container: "mkv", VideoCodec: "hevc", VideoRange: "dolby_vision", VideoBitDepth: 10,
		DolbyVisionProfile: 8, DolbyVisionCompatibilityID: 1, AudioCodec: "aac", AudioChannels: 2,
	})

	require.Equal(t, "remux", plan.Mode)
	require.Equal(t, "hdr10", plan.OutputVideoRange)
	require.False(t, plan.ToneMapped)
	require.Contains(t, plan.QualityLabel, "Dolby Vision → HDR10")
	query := playbackPlanQuery(t, plan.StreamURL)
	require.Equal(t, "1", query.Get("tater_strip_dolby_vision"))
	require.Equal(t, "hevc", query.Get("tater_video_codec"))
	require.Equal(t, "hls", query.Get("tater_output_container"))
}

func TestBuildTaterPlaybackPlanUnknownDolbyVisionProfileIsNotDirectOnTVOS(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mp4",
		Capabilities: taterPlaybackCapabilities{
			CapabilityVersion:   5,
			Platform:            "tvos",
			Containers:          []string{"mp4", "hls"},
			VideoCodecs:         []string{"hevc"},
			AudioCodecs:         []string{"aac"},
			VideoHDRFormats:     []string{"dolby_vision"},
			DisplayHDRFormats:   []string{"dolby_vision"},
			DisplayHDREnabled:   true,
			MaxVideoBitDepth:    10,
			DolbyVisionProfiles: []int{5},
		},
	}, taterPlaybackMediaInfo{
		Container: "mp4", VideoCodec: "hevc", VideoRange: "dolby_vision", VideoBitDepth: 10,
		AudioCodec: "aac", AudioChannels: 2,
	})

	require.Equal(t, "video_transcode", plan.Mode)
	require.Equal(t, "sdr", plan.OutputVideoRange)
	require.True(t, plan.ToneMapped)
}

func TestBuildTaterPlaybackPlanRemuxesHDRHEVCContainerAsHLS(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mkv",
		Profile:   "hdmi_4k",
		Capabilities: taterPlaybackCapabilities{
			CapabilityVersion:        5,
			Platform:                 "tvos",
			Containers:               []string{"mp4", "hls"},
			VideoCodecs:              []string{"hevc"},
			AudioCodecs:              []string{"aac"},
			VideoHDRFormats:          []string{"hdr10"},
			DisplayHDRFormats:        []string{"hdr10"},
			DisplayHDREnabled:        true,
			MaxVideoBitDepth:         10,
			PreferredStreamContainer: "hls",
		},
	}, taterPlaybackMediaInfo{
		Container: "mkv", VideoCodec: "hevc", VideoRange: "hdr10", VideoBitDepth: 10,
		AudioCodec: "aac", AudioChannels: 2,
	})

	require.Equal(t, "remux", plan.Mode)
	require.Equal(t, "hdr10", plan.OutputVideoRange)
	require.False(t, plan.ToneMapped)
	query := playbackPlanQuery(t, plan.StreamURL)
	require.Equal(t, "hevc", query.Get("tater_video_codec"))
	require.Equal(t, "hls", query.Get("tater_output_container"))
}

func TestBuildTaterPlaybackPlanToneMapsHDRWhenTVOSMustEncodeVideo(t *testing.T) {
	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: "http://tube.local/api/tater/local/stream?path=movie.mkv",
		Profile:   "hdmi_1080p",
		Capabilities: taterPlaybackCapabilities{
			CapabilityVersion:        5,
			Platform:                 "tvos",
			Containers:               []string{"mp4", "hls"},
			VideoCodecs:              []string{"hevc"},
			AudioCodecs:              []string{"aac"},
			VideoHDRFormats:          []string{"hdr10"},
			DisplayHDRFormats:        []string{"hdr10"},
			DisplayHDREnabled:        true,
			MaxVideoBitDepth:         10,
			MaxWidth:                 1920,
			MaxHeight:                1080,
			PreferredStreamContainer: "hls",
		},
	}, taterPlaybackMediaInfo{
		Container: "mkv", VideoCodec: "hevc", Width: 3840, Height: 2160,
		VideoRange: "hdr10", VideoBitDepth: 10, AudioCodec: "aac", AudioChannels: 2,
	})

	require.Equal(t, "video_transcode", plan.Mode)
	require.Equal(t, "sdr", plan.OutputVideoRange)
	require.True(t, plan.ToneMapped)
	require.Equal(t, "1", playbackPlanQuery(t, plan.StreamURL).Get("tater_tone_map"))
}

func TestTaterPlaybackVideoRangeDetectsHDRAndDolbyVision(t *testing.T) {
	videoRange, profile, compatibilityID := taterPlaybackVideoRange(
		"smpte2084", "bt2020", nil,
	)
	require.Equal(t, "hdr10", videoRange)
	require.Zero(t, profile)
	require.Zero(t, compatibilityID)

	videoRange, profile, compatibilityID = taterPlaybackVideoRange(
		"smpte2084", "bt2020", []taterFFprobePlaybackSideData{{
			SideDataType:               "DOVI configuration record",
			DolbyVisionProfile:         8,
			DolbyVisionCompatibilityID: 1,
		}},
	)
	require.Equal(t, "dolby_vision", videoRange)
	require.Equal(t, 8, profile)
	require.Equal(t, 1, compatibilityID)
}

func TestTaterPlaybackBitDepthUsesPixelFormatFallback(t *testing.T) {
	require.Equal(t, 12, taterPlaybackBitDepth("", "yuv420p12le"))
	require.Equal(t, 10, taterPlaybackBitDepth("10", "yuv420p"))
	require.Equal(t, 8, taterPlaybackBitDepth("", "yuv420p"))
}

func TestTaterPlaybackProbeTargetUsesLoopbackForDiscoveryStream(t *testing.T) {
	cfg := &config.Config{Server: config.ServerConfig{Port: 4229}}
	target, found, err := taterPlaybackProbeTarget(cfg,
		"https://public.example/api/files/stream?path=%2Fcomplete%2Fmovie.mkv&player_token=untrusted",
		"paired-player-token")
	require.NoError(t, err)
	require.True(t, found)

	parsed, err := url.Parse(target)
	require.NoError(t, err)
	require.Equal(t, "http", parsed.Scheme)
	require.Equal(t, "127.0.0.1:4229", parsed.Host)
	require.Equal(t, "/api/files/stream", parsed.Path)
	require.Equal(t, "/complete/movie.mkv", parsed.Query().Get("path"))
	require.Equal(t, "paired-player-token", parsed.Query().Get("player_token"))
	require.Equal(t, "1", parsed.Query().Get(taterInternalTranscodeInputQuery))
}

func TestTaterPlaybackProbeTargetIgnoresUnrelatedURLs(t *testing.T) {
	cfg := &config.Config{Server: config.ServerConfig{Port: 4229}}
	target, found, err := taterPlaybackProbeTarget(cfg,
		"https://public.example/api/files/streams/history?path=%2Fcomplete%2Fmovie.mkv",
		"paired-player-token")
	require.NoError(t, err)
	require.False(t, found)
	require.Empty(t, target)
}

func TestTaterPlaybackReleaseNameFallbackProtectsDeckFromUnknown4KDV(t *testing.T) {
	streamURL := "http://tube.local/api/files/stream?path=%2Fcomplete%2FMovie.2026.2160p.WEB-DL.DV.HDR10%2B.DDP5.1.mkv"
	source := taterPlaybackMediaInfoFromReleaseName(streamURL)
	require.Equal(t, "mkv", source.Container)
	require.Equal(t, "hevc", source.VideoCodec)
	require.Equal(t, 3840, source.Width)
	require.Equal(t, 2160, source.Height)
	require.Equal(t, "dolby_vision", source.VideoRange)
	require.Equal(t, 10, source.VideoBitDepth)
	require.Equal(t, "eac3", source.AudioCodec)
	require.Equal(t, 6, source.AudioChannels)

	plan := buildTaterPlaybackPlan(taterPlaybackSessionRequest{
		StreamURL: streamURL,
		Profile:   "hdmi_720p",
		Capabilities: taterPlaybackCapabilities{
			VideoCodecs:      []string{"hevc", "h264"},
			AudioCodecs:      []string{"eac3", "aac"},
			MaxWidth:         1280,
			MaxHeight:        800,
			MaxAudioChannels: 6,
			MaxVideoBitDepth: 8,
		},
	}, source)
	require.Equal(t, "video_transcode", plan.Mode)
	require.Equal(t, "transcode", plan.VideoMode)
	require.Equal(t, "direct", plan.AudioMode)
	require.True(t, plan.ToneMapped)
	require.Contains(t, plan.QualityLabel, "Dolby Vision → SDR Tone Map")
	require.Equal(t, 1280, plan.OutputWidth)
	require.Equal(t, 720, plan.OutputHeight)
	require.Equal(t, "4K → 720p", plan.ResolutionLabel)
}

func playbackPlanQuery(t *testing.T, rawURL string) url.Values {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	return u.Query()
}
