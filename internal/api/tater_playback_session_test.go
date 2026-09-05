package api

import (
	"net/url"
	"testing"

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

func playbackPlanQuery(t *testing.T, rawURL string) url.Values {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	return u.Query()
}
