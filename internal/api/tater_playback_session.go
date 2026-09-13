package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
)

const taterPlaybackProbeTimeout = 12 * time.Second

type taterPlaybackProbeCacheEntry struct {
	Size    int64
	ModTime int64
	Info    taterPlaybackMediaInfo
}

var taterPlaybackProbeCache sync.Map

type taterPlaybackCapabilities struct {
	CapabilityVersion        int      `json:"capability_version"`
	Platform                 string   `json:"platform"`
	Engine                   string   `json:"engine"`
	OutputName               string   `json:"output_name"`
	OutputConnection         string   `json:"output_connection"`
	PreferredStreamContainer string   `json:"preferred_stream_container"`
	Containers               []string `json:"containers"`
	VideoCodecs              []string `json:"video_codecs"`
	AudioCodecs              []string `json:"audio_codecs"`
	AudioPassthrough         []string `json:"audio_passthrough"`
	MaxWidth                 int      `json:"max_width"`
	MaxHeight                int      `json:"max_height"`
	MaxAudioChannels         int      `json:"max_audio_channels"`
	AudioDownmix             bool     `json:"audio_downmix"`
	CompatibilityMode        bool     `json:"compatibility_mode"`
	PassthroughAvailable     bool     `json:"passthrough_available"`
	VideoHDRFormats          []string `json:"video_hdr_formats"`
	DisplayHDRFormats        []string `json:"display_hdr_formats"`
	DisplayHDREnabled        bool     `json:"display_hdr_enabled"`
	MaxVideoBitDepth         int      `json:"max_video_bit_depth"`
	DolbyVisionProfiles      []int    `json:"dolby_vision_profiles"`
}

type taterPlaybackSessionRequest struct {
	StreamURL    string                    `json:"stream_url"`
	MediaType    string                    `json:"media_type"`
	Profile      string                    `json:"profile"`
	Capabilities taterPlaybackCapabilities `json:"capabilities"`
	AudioTrack   *int                      `json:"audio_track,omitempty"`
}

type taterPlaybackAudioTrack struct {
	Index         int    `json:"index"`
	StreamIndex   int    `json:"stream_index"`
	Codec         string `json:"codec,omitempty"`
	Profile       string `json:"profile,omitempty"`
	Channels      int    `json:"channels,omitempty"`
	ChannelLayout string `json:"channel_layout,omitempty"`
	Language      string `json:"language,omitempty"`
	Title         string `json:"title,omitempty"`
	BitRate       int64  `json:"bit_rate,omitempty"`
	Default       bool   `json:"default"`
	Commentary    bool   `json:"commentary,omitempty"`
	Descriptive   bool   `json:"descriptive,omitempty"`
}

type taterPlaybackMediaInfo struct {
	Container                  string                    `json:"container,omitempty"`
	DurationSeconds            float64                   `json:"duration_seconds,omitempty"`
	VideoCodec                 string                    `json:"video_codec,omitempty"`
	VideoProfile               string                    `json:"video_profile,omitempty"`
	Width                      int                       `json:"width,omitempty"`
	Height                     int                       `json:"height,omitempty"`
	VideoRange                 string                    `json:"video_range,omitempty"`
	PixelFormat                string                    `json:"pixel_format,omitempty"`
	ColorSpace                 string                    `json:"color_space,omitempty"`
	ColorTransfer              string                    `json:"color_transfer,omitempty"`
	ColorPrimaries             string                    `json:"color_primaries,omitempty"`
	VideoBitDepth              int                       `json:"video_bit_depth,omitempty"`
	DolbyVisionProfile         int                       `json:"dolby_vision_profile,omitempty"`
	DolbyVisionCompatibilityID int                       `json:"dolby_vision_compatibility_id,omitempty"`
	AudioCodec                 string                    `json:"audio_codec,omitempty"`
	AudioProfile               string                    `json:"audio_profile,omitempty"`
	AudioChannels              int                       `json:"audio_channels,omitempty"`
	ChannelLayout              string                    `json:"channel_layout,omitempty"`
	AudioTracks                []taterPlaybackAudioTrack `json:"audio_tracks,omitempty"`
}

type taterPlaybackSessionResponse struct {
	StreamURL           string                 `json:"stream_url"`
	Mode                string                 `json:"mode"`
	VideoMode           string                 `json:"video_mode"`
	AudioMode           string                 `json:"audio_mode"`
	VideoCodec          string                 `json:"video_codec,omitempty"`
	AudioCodec          string                 `json:"audio_codec,omitempty"`
	QualityLabel        string                 `json:"quality_label"`
	Reason              string                 `json:"reason,omitempty"`
	OutputName          string                 `json:"output_name,omitempty"`
	OutputConnection    string                 `json:"output_connection,omitempty"`
	OutputContainer     string                 `json:"output_container,omitempty"`
	SourceVideoRange    string                 `json:"source_video_range"`
	OutputVideoRange    string                 `json:"output_video_range"`
	ToneMapped          bool                   `json:"tone_mapped"`
	SelectedAudioTrack  int                    `json:"selected_audio_track"`
	OutputWidth         int                    `json:"output_width,omitempty"`
	OutputHeight        int                    `json:"output_height,omitempty"`
	OutputAudioChannels int                    `json:"output_audio_channels,omitempty"`
	ResolutionLabel     string                 `json:"resolution_label,omitempty"`
	Source              taterPlaybackMediaInfo `json:"source"`
}

type taterFFprobePlaybackSideData struct {
	SideDataType               string `json:"side_data_type"`
	DolbyVisionProfile         int    `json:"dv_profile"`
	DolbyVisionLevel           int    `json:"dv_level"`
	RPUFlag                    int    `json:"rpu_present_flag"`
	ELFlag                     int    `json:"el_present_flag"`
	BLFlag                     int    `json:"bl_present_flag"`
	DolbyVisionCompatibilityID int    `json:"dv_bl_signal_compatibility_id"`
}

type taterFFprobePlaybackResult struct {
	Streams []struct {
		Index            int                            `json:"index"`
		CodecType        string                         `json:"codec_type"`
		CodecName        string                         `json:"codec_name"`
		Profile          string                         `json:"profile"`
		Width            int                            `json:"width"`
		Height           int                            `json:"height"`
		Channels         int                            `json:"channels"`
		ChannelLayout    string                         `json:"channel_layout"`
		PixelFormat      string                         `json:"pix_fmt"`
		ColorSpace       string                         `json:"color_space"`
		ColorTransfer    string                         `json:"color_transfer"`
		ColorPrimaries   string                         `json:"color_primaries"`
		BitsPerRawSample string                         `json:"bits_per_raw_sample"`
		BitRate          string                         `json:"bit_rate"`
		SideDataList     []taterFFprobePlaybackSideData `json:"side_data_list"`
		Tags             struct {
			Language string `json:"language"`
			Title    string `json:"title"`
		} `json:"tags"`
		Disposition struct {
			Default        int `json:"default"`
			Comment        int `json:"comment"`
			VisualImpaired int `json:"visual_impaired"`
		} `json:"disposition"`
	} `json:"streams"`
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
	} `json:"format"`
}

func (s *Server) handleTaterPlayerPlaybackSession(c *fiber.Ctx) error {
	cfg, playerToken, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}

	var req taterPlaybackSessionRequest
	if err := c.BodyParser(&req); err != nil {
		return RespondValidationError(c, "Invalid playback request", err.Error())
	}
	req.StreamURL = strings.TrimSpace(req.StreamURL)
	if req.StreamURL == "" {
		return RespondValidationError(c, "Playback URL is required", "stream_url is empty")
	}
	if parsed, err := url.Parse(req.StreamURL); err != nil || parsed.Scheme == "" {
		return RespondValidationError(c, "Playback URL is invalid", "stream_url must be absolute")
	}

	source := taterPlaybackMediaInfoFromReleaseName(req.StreamURL)
	isTubeTV := taterPlaybackTubeTVChannelNumber(req.StreamURL) != ""
	if probeTarget, found, err := taterPlaybackTubeTVProbeTarget(cfg, req.StreamURL, resolveBaseURL(c, "")); err == nil && found {
		if probed, probeErr := probeTaterPlaybackMediaCached(c.Context(), cfg, probeTarget); probeErr == nil {
			source = probed
		}
	} else if probeTarget, found, err := taterPlaybackProbeTarget(cfg, req.StreamURL, playerToken); err != nil {
		return RespondValidationError(c, "Playback source is invalid", err.Error())
	} else if found {
		if probed, probeErr := probeTaterPlaybackMediaCached(c.Context(), cfg, probeTarget); probeErr == nil {
			source = probed
		}
	}

	plan := buildTaterPlaybackPlan(req, source)
	if isTubeTV {
		plan = buildTaterTVPlaybackPlan(req, source)
	}
	return RespondSuccess(c, plan)
}

func taterPlaybackTubeTVChannelNumber(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/playlist.m3u8") {
		return ""
	}
	return taterTVChannelNumberFromPath(u.Path)
}

func taterPlaybackTubeTVProbeTarget(cfg *config.Config, rawURL, baseURL string) (string, bool, error) {
	number := taterPlaybackTubeTVChannelNumber(rawURL)
	if number == "" {
		return "", false, nil
	}
	if cfg == nil {
		return "", true, fmt.Errorf("Tube TV configuration is unavailable")
	}
	now := time.Now()
	guide, err := taterTVEnsureGuide(cfg, baseURL, now)
	if err != nil {
		return "", true, err
	}
	channel, found := taterTVFindChannel(guide.Channels, number)
	if !found {
		return "", true, fmt.Errorf("Tube TV channel %s is unavailable", number)
	}
	items, err := taterTVResolveStreamItems(cfg, channel, guide.StartedAt, now, 1)
	if err != nil || len(items) == 0 {
		return "", true, fmt.Errorf("Tube TV channel %s has no playable item", number)
	}
	return items[0].Path, true, nil
}

func taterPlaybackMediaInfoFromReleaseName(rawURL string) taterPlaybackMediaInfo {
	path := parsedPlaybackPath(rawURL)
	release := strings.ToLower(filepath.Base(path))
	searchable := "." + strings.NewReplacer("_", ".", "-", ".", " ", ".").Replace(release) + "."
	has := func(value string) bool { return strings.Contains(searchable, "."+value+".") }

	info := taterPlaybackMediaInfo{Container: cleanTaterContainerName(filepath.Ext(path))}
	switch {
	case has("2160p") || has("4k"):
		info.Width, info.Height = 3840, 2160
	case has("1080p") || has("1080i"):
		info.Width, info.Height = 1920, 1080
	case has("720p"):
		info.Width, info.Height = 1280, 720
	}

	switch {
	case has("dv") || has("dovi") || strings.Contains(searchable, ".dolby.vision."):
		info.VideoRange = "dolby_vision"
		info.VideoCodec = "hevc"
		info.VideoBitDepth = 10
	case has("hdr10+") || has("hdr10plus"):
		info.VideoRange = "hdr10plus"
		info.VideoBitDepth = 10
	case has("hdr10") || has("hdr"):
		info.VideoRange = "hdr10"
		info.VideoBitDepth = 10
	}
	if info.VideoCodec == "" {
		switch {
		case has("hevc") || has("h265") || has("x265"):
			info.VideoCodec = "hevc"
		case has("av1"):
			info.VideoCodec = "av1"
		case has("h264") || has("x264") || has("avc"):
			info.VideoCodec = "h264"
		}
	}

	switch {
	case has("truehd"):
		info.AudioCodec = "truehd"
	case strings.Contains(searchable, ".dts.hd.") || has("dtshd") || has("dtsma"):
		info.AudioCodec = "dts_hd"
	case has("eac3") || has("ddp") || strings.Contains(searchable, ".ddp5.1.") || strings.Contains(searchable, ".ddp7.1."):
		info.AudioCodec = "eac3"
	case has("ac3") || has("dd"):
		info.AudioCodec = "ac3"
	case has("dts"):
		info.AudioCodec = "dts"
	case has("aac"):
		info.AudioCodec = "aac"
	}
	switch {
	case strings.Contains(searchable, "7.1."):
		info.AudioChannels = 8
	case strings.Contains(searchable, "5.1."):
		info.AudioChannels = 6
	case strings.Contains(searchable, "2.0."):
		info.AudioChannels = 2
	}
	return info
}

func taterPlaybackProbeTarget(cfg *config.Config, rawURL, playerToken string) (string, bool, error) {
	if localPath, found, err := taterPlaybackLocalSourcePath(cfg, rawURL); found || err != nil {
		return localPath, found, err
	}

	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || strings.TrimRight(u.Path, "/") != "/api/files/stream" {
		return "", false, nil
	}
	virtualPath := strings.TrimSpace(u.Query().Get("path"))
	if virtualPath == "" {
		return "", true, fmt.Errorf("discovery stream path is empty")
	}
	playerToken = strings.TrimSpace(playerToken)
	if playerToken == "" {
		return "", true, fmt.Errorf("discovery stream authorization is empty")
	}
	if cfg == nil || cfg.Server.Port <= 0 {
		return "", true, fmt.Errorf("server playback port is unavailable")
	}

	// Probe the prepared virtual file through the server's own streaming endpoint.
	// Rebuild the URL instead of trusting the client-supplied host or token, which
	// keeps this path from becoming an SSRF primitive for paired players.
	probeURL := url.URL{
		Scheme: "http",
		Host:   "127.0.0.1:" + strconv.Itoa(cfg.Server.Port),
		Path:   "/api/files/stream",
	}
	query := probeURL.Query()
	query.Set("path", virtualPath)
	query.Set("player_token", playerToken)
	query.Set(taterInternalTranscodeInputQuery, "1")
	probeURL.RawQuery = query.Encode()
	return probeURL.String(), true, nil
}

func probeTaterPlaybackMediaCached(parent context.Context, cfg *config.Config, path string) (taterPlaybackMediaInfo, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return taterPlaybackMediaInfo{}, fmt.Errorf("playback source is empty")
	}
	stat, statErr := os.Stat(path)
	if statErr != nil || stat.IsDir() {
		return probeTaterPlaybackMedia(parent, cfg, path)
	}
	cacheKey := filepath.Clean(path)
	if cached, ok := taterPlaybackProbeCache.Load(cacheKey); ok {
		entry := cached.(taterPlaybackProbeCacheEntry)
		if entry.Size == stat.Size() && entry.ModTime == stat.ModTime().UnixNano() {
			return entry.Info, nil
		}
	}
	info, err := probeTaterPlaybackMedia(parent, cfg, path)
	if err != nil {
		return taterPlaybackMediaInfo{}, err
	}
	taterPlaybackProbeCache.Store(cacheKey, taterPlaybackProbeCacheEntry{
		Size: stat.Size(), ModTime: stat.ModTime().UnixNano(), Info: info,
	})
	return info, nil
}

func buildTaterTVPlaybackPlan(req taterPlaybackSessionRequest, source taterPlaybackMediaInfo) taterPlaybackSessionResponse {
	caps := req.Capabilities
	profile := strings.TrimSpace(req.Profile)
	if _, ok := transcodeProfiles[profile]; !ok {
		profile = "hdmi_1080p"
	}
	selectedProfile := transcodeProfiles[profile]
	selectedAudioTrack := selectTaterPlaybackAudioTrack(&source, req.AudioTrack, &caps)
	sourceRange := cleanTaterVideoRange(source.VideoRange)
	if sourceRange == "" {
		sourceRange = "sdr"
	}

	hdrFormats := taterPlaybackSupportedHDRFormats(caps)
	videoCodec := "h264"
	if taterPlaybackCanUseHDRHEVC(caps) && len(hdrFormats) > 0 {
		// A Tube TV session can cross from SDR into HDR after it starts. Keep the
		// codec stable for the whole session so tvOS never has to switch from AVC
		// to HEVC at a program or break boundary.
		videoCodec = "hevc"
	}
	outputRange, toneMapped := taterTVPlaybackOutputRange(source, hdrFormats)
	if videoCodec != "hevc" && sourceRange != "sdr" {
		outputRange = "sdr"
		toneMapped = true
	}

	// Tube TV normalizes every program, commercial and bumper to one stable
	// audio layout for the lifetime of the HLS session. tvOS can request AAC
	// 5.1 when both the current program and configured output are multichannel;
	// older players keep the established stereo contract.
	outputAudioChannels := taterPlaybackTranscodeAudioChannels(caps, source.AudioChannels)
	if outputAudioChannels != 6 {
		outputAudioChannels = 2
	}
	plan := taterPlaybackSessionResponse{
		StreamURL:           req.StreamURL,
		Mode:                "full_transcode",
		VideoMode:           "transcode",
		AudioMode:           "transcode",
		VideoCodec:          videoCodec,
		AudioCodec:          "aac",
		QualityLabel:        "Video " + taterPlaybackVideoCodecLabel(videoCodec) + " • Audio AAC" + taterPlaybackAudioChannelsSuffix(outputAudioChannels),
		Reason:              "Tube TV is normalized into a continuous stream for this player.",
		OutputName:          strings.TrimSpace(caps.OutputName),
		OutputConnection:    strings.TrimSpace(caps.OutputConnection),
		OutputContainer:     "hls",
		SourceVideoRange:    sourceRange,
		OutputVideoRange:    outputRange,
		ToneMapped:          toneMapped,
		SelectedAudioTrack:  selectedAudioTrack,
		OutputAudioChannels: outputAudioChannels,
		Source:              source,
	}
	if sourceRange != "sdr" {
		plan.QualityLabel = taterPlaybackRangePrefix(sourceRange, outputRange, toneMapped) + plan.QualityLabel
	}
	if toneMapped {
		plan.Reason = "The current Tube TV picture is converted to SDR for this display while the channel remains continuous."
	} else if outputRange != "sdr" {
		plan.Reason = "The current Tube TV picture is preserved as Apple-compatible HEVC HDR."
	}
	plan.OutputWidth, plan.OutputHeight = taterPlaybackOutputDimensions(
		source.Width, source.Height, selectedProfile.MaxWidth, selectedProfile.MaxHeight,
	)
	plan.ResolutionLabel = taterPlaybackResolutionLabel(
		source.Width, source.Height, plan.OutputWidth, plan.OutputHeight,
	)
	plan.StreamURL = taterPlaybackPlannedURL(
		req.StreamURL, "full", profile, videoCodec, "", selectedAudioTrack, "hls",
	)
	plan.StreamURL = annotateTaterPlaybackURL(
		plan.StreamURL, plan.VideoMode, plan.VideoCodec, plan.AudioMode, plan.AudioCodec,
		sourceRange, outputRange, toneMapped, selectedAudioTrack,
		source.Width, source.Height, plan.OutputWidth, plan.OutputHeight,
		outputAudioChannels, "hls",
	)
	plan.StreamURL = annotateTaterTVHDRFormats(plan.StreamURL, hdrFormats)
	return plan
}

func taterPlaybackVideoCodecLabel(codec string) string {
	if cleanTaterCodecName(codec) == "hevc" {
		return "HEVC"
	}
	return "H.264"
}

func taterPlaybackCanUseHDRHEVC(caps taterPlaybackCapabilities) bool {
	return strings.EqualFold(strings.TrimSpace(caps.Platform), "tvos") &&
		cleanTaterPreferredStreamContainer(caps.PreferredStreamContainer) == "hls" &&
		taterCodecListContains(caps.VideoCodecs, "hevc") &&
		caps.DisplayHDREnabled && caps.MaxVideoBitDepth >= 10
}

func taterPlaybackSupportedHDRFormats(caps taterPlaybackCapabilities) []string {
	if !caps.DisplayHDREnabled {
		return nil
	}
	seen := map[string]bool{}
	formats := []string{}
	for _, candidate := range caps.VideoHDRFormats {
		format := cleanTaterVideoRange(candidate)
		if format == "" || format == "sdr" || seen[format] ||
			!taterPlaybackRangeSupported(caps.DisplayHDRFormats, format) {
			continue
		}
		seen[format] = true
		formats = append(formats, format)
	}
	return formats
}

func taterTVPlaybackOutputRange(source taterPlaybackMediaInfo, hdrFormats []string) (string, bool) {
	sourceRange := cleanTaterVideoRange(source.VideoRange)
	if sourceRange == "" || sourceRange == "sdr" {
		return "sdr", false
	}
	supports := func(wanted string) bool {
		for _, format := range hdrFormats {
			if cleanTaterVideoRange(format) == wanted {
				return true
			}
		}
		return false
	}
	switch sourceRange {
	case "hdr10":
		if supports("hdr10") {
			return "hdr10", false
		}
	case "hdr10plus":
		// The per-item Tube TV encoder does not carry HDR10+ dynamic metadata
		// through a scale/overlay pass. Preserve the compatible HDR10 picture
		// instead of labeling the new stream as HDR10+.
		if supports("hdr10") {
			return "hdr10", false
		}
	case "hlg":
		if supports("hlg") {
			return "hlg", false
		}
	case "dolby_vision":
		// Tube TV is re-encoded to keep every program and break on one timeline.
		// Profile 7 has an HDR10-compatible base layer. Re-encoding that layer as
		// ordinary HEVC Main 10 discards the enhancement layer and Dolby Vision
		// metadata while retaining an honest HDR10 picture.
		if source.DolbyVisionProfile == 7 && supports("hdr10") {
			return "hdr10", false
		}
		// Profile 8 is single-layer and may carry an HDR10- or HLG-compatible base.
		if source.DolbyVisionProfile == 8 {
			switch source.DolbyVisionCompatibilityID {
			case 1:
				if supports("hdr10") {
					return "hdr10", false
				}
			case 4:
				if supports("hlg") {
					return "hlg", false
				}
			}
		}
	}
	return "sdr", true
}

func taterPlaybackReencodedHDRRange(caps taterPlaybackCapabilities, source taterPlaybackMediaInfo, plannedRange string) string {
	supports := func(videoRange string) bool {
		return taterPlaybackRangeSupported(caps.VideoHDRFormats, videoRange) &&
			taterPlaybackRangeSupported(caps.DisplayHDRFormats, videoRange)
	}
	switch cleanTaterVideoRange(plannedRange) {
	case "hdr10":
		if supports("hdr10") {
			return "hdr10"
		}
	case "hlg":
		if supports("hlg") {
			return "hlg"
		}
	case "hdr10plus":
		// Re-encoding cannot promise preservation of HDR10+ dynamic metadata.
		if supports("hdr10") {
			return "hdr10"
		}
	case "dolby_vision":
		// The ordinary HEVC encoders do not author Dolby Vision metadata. A
		// Profile 8 compatible base can still become honest HDR10 or HLG.
		if source.DolbyVisionProfile == 8 {
			switch source.DolbyVisionCompatibilityID {
			case 1:
				if supports("hdr10") {
					return "hdr10"
				}
			case 4:
				if supports("hlg") {
					return "hlg"
				}
			}
		}
	}
	return ""
}

func annotateTaterTVHDRFormats(rawURL string, formats []string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return rawURL
	}
	query := u.Query()
	query.Del("tater_hdr_formats")
	if len(formats) > 0 {
		query.Set("tater_hdr_formats", strings.Join(formats, ","))
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func buildTaterPlaybackPlan(req taterPlaybackSessionRequest, source taterPlaybackMediaInfo) taterPlaybackSessionResponse {
	caps := req.Capabilities
	selectedAudioTrack := selectTaterPlaybackAudioTrack(&source, req.AudioTrack, &caps)
	profile := strings.TrimSpace(req.Profile)
	if _, ok := transcodeProfiles[profile]; !ok {
		profile = "hdmi_1080p"
	}
	selectedProfile := transcodeProfiles[profile]
	preferredContainer := cleanTaterPreferredStreamContainer(caps.PreferredStreamContainer)
	transcodeAudioChannels := taterPlaybackTranscodeAudioChannels(caps, source.AudioChannels)

	videoCodec := cleanTaterCodecName(source.VideoCodec)
	audioCodec := cleanTaterCodecName(source.AudioCodec)
	videoCompatible := videoCodec == ""
	if videoCodec != "" {
		videoCompatible = taterCodecListContains(caps.VideoCodecs, videoCodec)
	}
	if source.Width > 0 && caps.MaxWidth > 0 && source.Width > caps.MaxWidth {
		videoCompatible = false
	}
	if source.Height > 0 && caps.MaxHeight > 0 && source.Height > caps.MaxHeight {
		videoCompatible = false
	}
	containerCompatible := source.Container == "" ||
		taterContainerListContains(caps.Containers, source.Container)
	requiresContainerChange := preferredContainer != "" && !containerCompatible

	sourceRange := cleanTaterVideoRange(source.VideoRange)
	if sourceRange == "" {
		sourceRange = "sdr"
	}
	outputRange := sourceRange
	toneMapped := false
	rangeFallback := false
	hdrHEVCTranscode := false
	if sourceRange != "sdr" {
		if !taterPlaybackCanOutputRange(caps, source) {
			if fallbackRange := taterPlaybackHDRFallbackRange(caps, source); fallbackRange != "" {
				outputRange = fallbackRange
				rangeFallback = true
				if sourceRange == "dolby_vision" && source.DolbyVisionProfile == 7 {
					// Unlike Profile 8, Profile 7 is dual-layer. Do not remux it as
					// HDR10; decode its HDR10 base layer and create a clean HEVC stream.
					videoCompatible = false
				}
			} else {
				videoCompatible = false
				outputRange = "sdr"
				toneMapped = true
			}
		}
		if source.VideoBitDepth > 0 && caps.MaxVideoBitDepth > 0 &&
			source.VideoBitDepth > caps.MaxVideoBitDepth {
			videoCompatible = false
			outputRange = "sdr"
			toneMapped = true
			rangeFallback = false
		}
		// HDR that must be resized or converted for tvOS stays HEVC Main 10 in
		// fragmented-MP4 HLS. Unsupported Dolby Vision profiles still take the
		// safe SDR tone-map path selected above.
		if strings.EqualFold(strings.TrimSpace(caps.Platform), "tvos") &&
			(!videoCompatible || videoCodec != "hevc") {
			if !toneMapped && taterPlaybackCanUseHDRHEVC(caps) {
				if encodedRange := taterPlaybackReencodedHDRRange(caps, source, outputRange); encodedRange != "" {
					videoCompatible = false
					outputRange = encodedRange
					rangeFallback = outputRange != sourceRange
					hdrHEVCTranscode = true
				} else {
					videoCompatible = false
					outputRange = "sdr"
					toneMapped = true
					rangeFallback = false
				}
			} else {
				videoCompatible = false
				outputRange = "sdr"
				toneMapped = true
				rangeFallback = false
			}
		}
	}

	passthrough := caps.PassthroughAvailable && audioCodec != "" &&
		taterCodecListContains(caps.AudioPassthrough, audioCodec)
	audioCompatible := audioCodec == "" && !caps.CompatibilityMode
	if audioCodec != "" {
		audioCompatible = passthrough || taterCodecListContains(caps.AudioCodecs, audioCodec)
		if !passthrough && !caps.AudioDownmix && source.AudioChannels > 0 && caps.MaxAudioChannels > 0 &&
			source.AudioChannels > caps.MaxAudioChannels {
			audioCompatible = false
		}
	}
	// Keep compatible HEVC video direct, but normalize E-AC-3 when an MKV must be
	// repackaged as fragmented-MP4 HLS for tvOS. Long-running copied E-AC-3
	// fMP4 sessions can accumulate audible delay in AVPlayer even while the
	// source timestamps remain aligned. Audio-only conversion is inexpensive,
	// preserves 5.1, and gives the HLS stream one continuous Apple-safe clock.
	if strings.EqualFold(strings.TrimSpace(caps.Platform), "tvos") &&
		preferredContainer == "hls" && requiresContainerChange &&
		videoCodec == "hevc" && audioCodec == "eac3" && audioCompatible {
		audioCompatible = false
	}

	plan := taterPlaybackSessionResponse{
		StreamURL:           req.StreamURL,
		Mode:                "direct",
		VideoMode:           "direct",
		AudioMode:           "direct",
		VideoCodec:          videoCodec,
		AudioCodec:          audioCodec,
		OutputName:          strings.TrimSpace(caps.OutputName),
		OutputConnection:    strings.TrimSpace(caps.OutputConnection),
		OutputContainer:     source.Container,
		SourceVideoRange:    sourceRange,
		OutputVideoRange:    outputRange,
		ToneMapped:          toneMapped,
		SelectedAudioTrack:  selectedAudioTrack,
		OutputAudioChannels: source.AudioChannels,
		Source:              source,
	}
	if passthrough {
		plan.AudioMode = "bitstream"
	}
	transcodeVideoCodec := "h264"
	if hdrHEVCTranscode {
		transcodeVideoCodec = "hevc"
	}

	switch {
	case videoCompatible && audioCompatible && (requiresContainerChange || rangeFallback):
		plan.Mode = "remux"
		plan.OutputContainer = preferredContainer
		plan.StreamURL = taterPlaybackPlannedURL(req.StreamURL, "remux", profile, videoCodec, audioCodec, selectedAudioTrack, preferredContainer)
		if passthrough {
			plan.QualityLabel = taterPlaybackRangePrefix(sourceRange, outputRange, false) + "Video Direct • Audio Bitstream" + taterPlaybackCodecSuffix(audioCodec)
		} else {
			plan.QualityLabel = taterPlaybackRangePrefix(sourceRange, outputRange, false) + "Video Direct • Audio Direct" + taterPlaybackCodecSuffix(audioCodec)
		}
		plan.Reason = "The tracks are compatible; the server is repackaging the stream for this player."
	case videoCompatible && audioCompatible:
		if passthrough {
			plan.QualityLabel = taterPlaybackRangePrefix(sourceRange, outputRange, false) + "Video Direct • Audio Bitstream" + taterPlaybackCodecSuffix(audioCodec)
			plan.Reason = "The current audio output accepts the source audio format."
		} else {
			plan.QualityLabel = taterPlaybackRangePrefix(sourceRange, outputRange, false) + "Video Direct • Audio Direct" + taterPlaybackCodecSuffix(audioCodec)
			plan.Reason = "The player can decode both source tracks."
		}
		if rangeFallback {
			plan.Reason = "The source includes an HDR10-compatible base layer for this display."
		}
	case videoCompatible:
		plan.Mode = "audio_transcode"
		plan.AudioMode = "transcode"
		plan.AudioCodec = "aac"
		plan.OutputAudioChannels = transcodeAudioChannels
		plan.OutputContainer = preferredContainer
		plan.StreamURL = taterPlaybackPlannedURL(req.StreamURL, "audio", profile, "h264", "", selectedAudioTrack, preferredContainer)
		plan.QualityLabel = taterPlaybackRangePrefix(sourceRange, outputRange, false) + "Video Direct • Audio AAC" + taterPlaybackAudioChannelsSuffix(transcodeAudioChannels)
		plan.Reason = "The source video is compatible, but its audio needs conversion."
	case audioCompatible:
		plan.Mode = "video_transcode"
		plan.VideoMode = "transcode"
		plan.VideoCodec = transcodeVideoCodec
		plan.OutputContainer = preferredContainer
		plan.StreamURL = taterPlaybackPlannedURL(req.StreamURL, "video", profile, transcodeVideoCodec, audioCodec, selectedAudioTrack, preferredContainer)
		if passthrough {
			plan.QualityLabel = taterPlaybackRangePrefix(sourceRange, outputRange, toneMapped) + "Video " + taterPlaybackVideoCodecLabel(transcodeVideoCodec) + " • Audio Bitstream" + taterPlaybackCodecSuffix(audioCodec)
			plan.Reason = "The source audio is preserved while the video is converted."
		} else {
			plan.QualityLabel = taterPlaybackRangePrefix(sourceRange, outputRange, toneMapped) + "Video " + taterPlaybackVideoCodecLabel(transcodeVideoCodec) + " • Audio Direct" + taterPlaybackCodecSuffix(audioCodec)
			plan.Reason = "The source audio is compatible, so only the video is converted."
		}
	default:
		plan.Mode = "full_transcode"
		plan.VideoMode = "transcode"
		plan.AudioMode = "transcode"
		plan.VideoCodec = transcodeVideoCodec
		plan.AudioCodec = "aac"
		plan.OutputAudioChannels = transcodeAudioChannels
		plan.OutputContainer = preferredContainer
		if plan.OutputContainer == "" {
			plan.OutputContainer = "mpegts"
		}
		plan.StreamURL = taterPlaybackPlannedURL(req.StreamURL, "full", profile, transcodeVideoCodec, "", selectedAudioTrack, preferredContainer)
		plan.QualityLabel = taterPlaybackRangePrefix(sourceRange, outputRange, toneMapped) + "Video " + taterPlaybackVideoCodecLabel(transcodeVideoCodec) + " • Audio AAC" + taterPlaybackAudioChannelsSuffix(transcodeAudioChannels)
		plan.Reason = "Both source tracks need conversion for this player."
	}
	if toneMapped {
		if audioCompatible {
			plan.Reason = "The connected display cannot use the source picture format, so only the video is tone-mapped."
		} else {
			plan.Reason = "The picture is tone-mapped for the connected display and the audio is converted for the player."
		}
	}
	plan.OutputWidth, plan.OutputHeight = source.Width, source.Height
	if plan.VideoMode == "transcode" {
		plan.OutputWidth, plan.OutputHeight = taterPlaybackOutputDimensions(
			source.Width, source.Height, selectedProfile.MaxWidth, selectedProfile.MaxHeight,
		)
	}
	plan.ResolutionLabel = taterPlaybackResolutionLabel(
		source.Width, source.Height, plan.OutputWidth, plan.OutputHeight,
	)
	annotatedOutputContainer := ""
	if preferredContainer != "" {
		annotatedOutputContainer = plan.OutputContainer
	}
	plan.StreamURL = annotateTaterPlaybackURL(
		plan.StreamURL, plan.VideoMode, plan.VideoCodec, plan.AudioMode, plan.AudioCodec,
		sourceRange, outputRange, toneMapped, selectedAudioTrack,
		source.Width, source.Height, plan.OutputWidth, plan.OutputHeight,
		plan.OutputAudioChannels, annotatedOutputContainer,
	)
	return plan
}

func annotateTaterPlaybackURL(
	rawURL, videoMode, videoCodec, audioMode, audioCodec, sourceRange, outputRange string,
	toneMapped bool,
	audioTrack, sourceWidth, sourceHeight, outputWidth, outputHeight int,
	outputAudioChannels int, outputContainer string,
) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return rawURL
	}
	query := u.Query()
	query.Set("tater_video_mode", cleanTaterCodecName(videoMode))
	if codec := cleanTaterCodecName(videoCodec); codec != "" {
		query.Set("tater_video_codec", codec)
	} else {
		query.Del("tater_video_codec")
	}
	query.Set("tater_audio_mode", cleanTaterCodecName(audioMode))
	if codec := cleanTaterCodecName(audioCodec); codec != "" {
		query.Set("tater_audio_codec", codec)
	} else {
		query.Del("tater_audio_codec")
	}
	query.Set("tater_source_video_range", cleanTaterVideoRange(sourceRange))
	query.Set("tater_output_video_range", cleanTaterVideoRange(outputRange))
	if container := cleanTaterContainerName(outputContainer); container != "" {
		query.Set("tater_output_container", container)
	} else {
		query.Del("tater_output_container")
	}
	if toneMapped {
		query.Set("tater_tone_map", "1")
	} else {
		query.Del("tater_tone_map")
	}
	if cleanTaterVideoRange(sourceRange) == "dolby_vision" &&
		cleanTaterVideoRange(outputRange) != "dolby_vision" && !toneMapped {
		query.Set("tater_strip_dolby_vision", "1")
	} else {
		query.Del("tater_strip_dolby_vision")
	}
	if audioTrack >= 0 {
		query.Set("tater_audio_track", strconv.Itoa(audioTrack))
	} else {
		query.Del("tater_audio_track")
	}
	setDimension := func(key string, value int) {
		if value > 0 {
			query.Set(key, strconv.Itoa(value))
		} else {
			query.Del(key)
		}
	}
	setDimension("tater_source_width", sourceWidth)
	setDimension("tater_source_height", sourceHeight)
	setDimension("tater_output_width", outputWidth)
	setDimension("tater_output_height", outputHeight)
	if audioMode == "transcode" && outputAudioChannels > 0 {
		query.Set("tater_audio_channels", strconv.Itoa(outputAudioChannels))
	} else {
		query.Del("tater_audio_channels")
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func taterPlaybackTranscodeAudioChannels(caps taterPlaybackCapabilities, sourceChannels int) int {
	// Multichannel AAC is currently an explicit tvOS playback contract. Older
	// players never send this query value and retain the established stereo
	// transcode path.
	if !strings.EqualFold(strings.TrimSpace(caps.Platform), "tvos") {
		return 0
	}
	if sourceChannels >= 6 && caps.MaxAudioChannels >= 6 {
		return 6
	}
	return 2
}

func taterPlaybackAudioChannelsSuffix(channels int) string {
	switch channels {
	case 6:
		return " 5.1"
	case 2:
		return " Stereo"
	default:
		return ""
	}
}

func taterPlaybackOutputDimensions(sourceWidth, sourceHeight, maximumWidth, maximumHeight int) (int, int) {
	if sourceWidth <= 0 || sourceHeight <= 0 {
		return 0, 0
	}
	if maximumWidth <= 0 || maximumHeight <= 0 {
		return sourceWidth, sourceHeight
	}
	scale := math.Min(
		float64(maximumWidth)/float64(sourceWidth),
		float64(maximumHeight)/float64(sourceHeight),
	)
	width := max(2, int(math.Floor(float64(sourceWidth)*scale)))
	height := max(2, int(math.Floor(float64(sourceHeight)*scale)))
	width -= width % 2
	height -= height % 2
	return width, height
}

func taterPlaybackResolutionName(width, height int) string {
	switch {
	case width >= 3500 || height >= 2000:
		return "4K"
	case width >= 2500 || height >= 1400:
		return "1440p"
	case width >= 1800 || height >= 1000:
		return "1080p"
	case width >= 1100 || height >= 650:
		return "720p"
	case width > 0 && height > 0:
		return fmt.Sprintf("%d×%d", width, height)
	default:
		return ""
	}
}

func taterPlaybackResolutionLabel(sourceWidth, sourceHeight, outputWidth, outputHeight int) string {
	source := taterPlaybackResolutionName(sourceWidth, sourceHeight)
	output := taterPlaybackResolutionName(outputWidth, outputHeight)
	if source == "" {
		return output
	}
	if output == "" || (sourceWidth == outputWidth && sourceHeight == outputHeight) {
		return source
	}
	return source + " → " + output
}

func taterPlaybackPlannedURL(rawURL, mode, profile, videoCodec, audioCodec string, audioTrack int, outputContainer string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return rawURL
	}
	query := u.Query()
	for _, key := range []string{"direct", "transcode", "profile", "codec", "audio_codec", "start", "tater_audio_track", "tater_audio_channels", "tater_tone_map", "tater_strip_dolby_vision", "tater_video_codec", "tater_source_video_range", "tater_output_video_range", "tater_output_container", "tater_source_width", "tater_source_height", "tater_output_width", "tater_output_height", "tater_hdr_formats"} {
		query.Del(key)
	}
	switch mode {
	case "audio":
		query.Set("transcode", "audio")
	case "video":
		query.Set("transcode", "video")
	case "full":
		query.Set("transcode", "1")
	case "remux":
		query.Set("transcode", "remux")
	default:
		u.RawQuery = query.Encode()
		return u.String()
	}
	query.Set("profile", profile)
	if mode == "video" || mode == "full" {
		query.Set("codec", cleanTaterCodecName(videoCodec))
	}
	if mode == "video" && cleanTaterCodecName(audioCodec) != "" {
		query.Set("audio_codec", cleanTaterCodecName(audioCodec))
	}
	if audioTrack >= 0 {
		query.Set("tater_audio_track", strconv.Itoa(audioTrack))
	}
	if container := cleanTaterPreferredStreamContainer(outputContainer); container != "" {
		query.Set("tater_output_container", container)
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func taterPlaybackLocalSourcePath(cfg *config.Config, rawURL string) (string, bool, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/api/tater/local/stream") {
		return "", false, nil
	}
	categoryID := strings.TrimSpace(u.Query().Get("category_id"))
	category, ok := taterLocalMediaCategory(cfg, categoryID)
	if !ok {
		return "", true, fmt.Errorf("local media category not found")
	}
	paths := taterLocalMediaCategoryPaths(category)
	sourceIndex, err := strconv.Atoi(strings.TrimSpace(u.Query().Get("source")))
	if err != nil || sourceIndex < 0 || sourceIndex >= len(paths) {
		return "", true, fmt.Errorf("local media source not found")
	}
	relPath := cleanLocalRelativePath(u.Query().Get("path"))
	if relPath == "" {
		return "", true, fmt.Errorf("local media path is empty")
	}
	path, err := safeLocalPath(paths[sourceIndex], relPath)
	if err != nil {
		return "", true, err
	}
	return path, true, nil
}

func probeTaterPlaybackMedia(parent context.Context, cfg *config.Config, path string) (taterPlaybackMediaInfo, error) {
	ffmpegPath := "ffmpeg"
	if cfg != nil {
		ffmpegPath = effectiveFFmpegPath(cfg.Transcoding.FFmpegPath)
	}
	ffprobePath := effectiveFFprobePath(ffmpegPath)
	if ffprobePath == "" {
		return taterPlaybackMediaInfo{}, fmt.Errorf("ffprobe not found")
	}
	ctx, cancel := context.WithTimeout(parent, taterPlaybackProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "error",
		"-show_entries", "format=format_name,duration:stream=index,codec_type,codec_name,profile,width,height,channels,channel_layout,pix_fmt,color_space,color_transfer,color_primaries,bits_per_raw_sample,bit_rate:stream_tags=language,title:stream_disposition=default,comment,visual_impaired:stream_side_data=side_data_type,dv_profile,dv_level,rpu_present_flag,el_present_flag,bl_present_flag,dv_bl_signal_compatibility_id",
		"-of", "json",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		return taterPlaybackMediaInfo{}, err
	}
	var result taterFFprobePlaybackResult
	if err := json.Unmarshal(out, &result); err != nil {
		return taterPlaybackMediaInfo{}, err
	}
	info := taterPlaybackMediaInfo{Container: cleanTaterContainerName(result.Format.FormatName)}
	if duration, parseErr := strconv.ParseFloat(strings.TrimSpace(result.Format.Duration), 64); parseErr == nil && duration > 0 {
		info.DurationSeconds = duration
	}
	audioIndex := 0
	for _, stream := range result.Streams {
		switch strings.ToLower(strings.TrimSpace(stream.CodecType)) {
		case "video":
			if info.VideoCodec == "" {
				info.VideoCodec = cleanTaterCodecName(stream.CodecName)
				info.VideoProfile = strings.TrimSpace(stream.Profile)
				info.Width = stream.Width
				info.Height = stream.Height
				info.PixelFormat = strings.ToLower(strings.TrimSpace(stream.PixelFormat))
				info.ColorSpace = strings.ToLower(strings.TrimSpace(stream.ColorSpace))
				info.ColorTransfer = strings.ToLower(strings.TrimSpace(stream.ColorTransfer))
				info.ColorPrimaries = strings.ToLower(strings.TrimSpace(stream.ColorPrimaries))
				info.VideoBitDepth = taterPlaybackBitDepth(stream.BitsPerRawSample, stream.PixelFormat)
				info.VideoRange, info.DolbyVisionProfile, info.DolbyVisionCompatibilityID = taterPlaybackVideoRange(stream.ColorTransfer, stream.ColorPrimaries, stream.SideDataList)
			}
		case "audio":
			codec := cleanTaterCodecName(stream.CodecName)
			profile := strings.TrimSpace(stream.Profile)
			if codec == "dts" && strings.Contains(strings.ToLower(profile), "dts-hd") {
				codec = "dts_hd"
			}
			bitRate, _ := strconv.ParseInt(strings.TrimSpace(stream.BitRate), 10, 64)
			title := strings.TrimSpace(stream.Tags.Title)
			titleLower := strings.ToLower(title)
			info.AudioTracks = append(info.AudioTracks, taterPlaybackAudioTrack{
				Index:         audioIndex,
				StreamIndex:   stream.Index,
				Codec:         codec,
				Profile:       profile,
				Channels:      stream.Channels,
				ChannelLayout: strings.TrimSpace(stream.ChannelLayout),
				Language:      normalizeTaterAudioLanguage(stream.Tags.Language),
				Title:         title,
				BitRate:       bitRate,
				Default:       stream.Disposition.Default != 0,
				Commentary: stream.Disposition.Comment != 0 ||
					strings.Contains(titleLower, "commentary"),
				Descriptive: stream.Disposition.VisualImpaired != 0 ||
					strings.Contains(titleLower, "descriptive") ||
					strings.Contains(titleLower, "description"),
			})
			audioIndex++
		}
	}
	selectTaterPlaybackAudioTrack(&info, nil, nil)
	return info, nil
}

func selectTaterPlaybackAudioTrack(info *taterPlaybackMediaInfo, requested *int, caps *taterPlaybackCapabilities) int {
	if info == nil || len(info.AudioTracks) == 0 {
		return -1
	}
	selected := -1
	if requested != nil {
		for index := range info.AudioTracks {
			if info.AudioTracks[index].Index == *requested {
				selected = index
				break
			}
		}
	}
	if selected < 0 {
		bestScore := int64(-1 << 62)
		for index, track := range info.AudioTracks {
			score := taterPlaybackAudioTrackScore(track)
			// Keep English and non-commentary preferences dominant, then prefer
			// the best track this player can preserve. This avoids selecting an
			// unsupported lossless track and downmixing it to AAC when the same
			// file already contains a directly playable 5.1 track.
			if caps != nil && taterPlaybackAudioTrackCompatible(track, *caps) {
				score += 500_000
			}
			if selected < 0 || score > bestScore {
				selected = index
				bestScore = score
			}
		}
	}
	track := info.AudioTracks[selected]
	info.AudioCodec = track.Codec
	info.AudioProfile = track.Profile
	info.AudioChannels = track.Channels
	info.ChannelLayout = track.ChannelLayout
	return track.Index
}

func taterPlaybackAudioTrackCompatible(track taterPlaybackAudioTrack, caps taterPlaybackCapabilities) bool {
	codec := cleanTaterCodecName(track.Codec)
	if codec == "" {
		return !caps.CompatibilityMode
	}
	passthrough := caps.PassthroughAvailable &&
		taterCodecListContains(caps.AudioPassthrough, codec)
	if !passthrough && !taterCodecListContains(caps.AudioCodecs, codec) {
		return false
	}
	if !passthrough && !caps.AudioDownmix && track.Channels > 0 &&
		caps.MaxAudioChannels > 0 && track.Channels > caps.MaxAudioChannels {
		return false
	}
	return true
}

func taterPlaybackAudioTrackScore(track taterPlaybackAudioTrack) int64 {
	score := int64(0)
	if isTaterEnglishAudioLanguage(track.Language) {
		score += 1_000_000
	} else if strings.TrimSpace(track.Language) == "" {
		score += 100_000
	}
	if track.Default {
		score += 25_000
	}
	if track.Commentary || track.Descriptive {
		score -= 200_000
	}
	score += int64(track.Channels) * 10_000
	score += int64(taterPlaybackAudioCodecRank(track.Codec)) * 1_000
	if track.BitRate > 0 {
		score += min(track.BitRate/1000, 20_000)
	}
	return score
}

func normalizeTaterAudioLanguage(value string) string {
	language := strings.ToLower(strings.TrimSpace(value))
	language = strings.ReplaceAll(language, "_", "-")
	switch language {
	case "eng", "english":
		return "en"
	}
	if strings.HasPrefix(language, "eng-") {
		return "en-" + strings.TrimPrefix(language, "eng-")
	}
	return language
}

func isTaterEnglishAudioLanguage(value string) bool {
	language := normalizeTaterAudioLanguage(value)
	return language == "en" || strings.HasPrefix(language, "en-")
}

func taterPlaybackAudioCodecRank(value string) int {
	codec := cleanTaterCodecName(value)
	if strings.HasPrefix(codec, "pcm") {
		return 9
	}
	switch codec {
	case "truehd", "dts_hd", "flac", "alac":
		return 9
	case "eac3", "opus":
		return 7
	case "dts", "ac3":
		return 6
	case "aac":
		return 5
	case "mp3":
		return 3
	default:
		return 1
	}
}

func taterPlaybackVideoRange(transfer, primaries string, sideData []taterFFprobePlaybackSideData) (string, int, int) {
	dolbyProfile := 0
	dolbyCompatibilityID := 0
	hdr10Plus := false
	hdrStaticMetadata := false
	for _, data := range sideData {
		kind := strings.ToLower(strings.TrimSpace(data.SideDataType))
		if strings.Contains(kind, "dovi") || strings.Contains(kind, "dolby vision") {
			dolbyProfile = data.DolbyVisionProfile
			dolbyCompatibilityID = data.DolbyVisionCompatibilityID
		}
		if strings.Contains(kind, "smpte2094-40") || strings.Contains(kind, "hdr10+") {
			hdr10Plus = true
		}
		if strings.Contains(kind, "mastering display") || strings.Contains(kind, "content light") {
			hdrStaticMetadata = true
		}
	}
	if dolbyProfile > 0 {
		return "dolby_vision", dolbyProfile, dolbyCompatibilityID
	}
	if hdr10Plus {
		return "hdr10plus", 0, 0
	}
	switch cleanTaterVideoRange(transfer) {
	case "hdr10":
		return "hdr10", 0, 0
	case "hlg":
		return "hlg", 0, 0
	}
	if strings.EqualFold(strings.TrimSpace(primaries), "bt2020") &&
		(strings.Contains(strings.ToLower(strings.TrimSpace(transfer)), "2084") || hdrStaticMetadata) {
		return "hdr10", 0, 0
	}
	return "sdr", 0, 0
}

func taterPlaybackBitDepth(bitsPerRawSample, pixelFormat string) int {
	if bits, err := strconv.Atoi(strings.TrimSpace(bitsPerRawSample)); err == nil && bits > 0 {
		return bits
	}
	format := strings.ToLower(strings.TrimSpace(pixelFormat))
	for _, bits := range []int{16, 14, 12, 10, 9} {
		if strings.Contains(format, strconv.Itoa(bits)) {
			return bits
		}
	}
	if format != "" {
		return 8
	}
	return 0
}

func cleanTaterVideoRange(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("-", "", "_", "", "+", "plus", ".", "").Replace(value)
	switch value {
	case "dolbyvision", "dovi", "dv":
		return "dolby_vision"
	case "hdr10plus", "smpte209440":
		return "hdr10plus"
	case "hdr10", "pq", "smpte2084":
		return "hdr10"
	case "hlg", "aribstdb67":
		return "hlg"
	case "sdr", "bt709", "iec6196621":
		return "sdr"
	default:
		return ""
	}
}

func taterPlaybackRangeSupported(values []string, videoRange string) bool {
	videoRange = cleanTaterVideoRange(videoRange)
	for _, value := range values {
		if cleanTaterVideoRange(value) == videoRange {
			return true
		}
	}
	return false
}

func taterPlaybackCanOutputRange(caps taterPlaybackCapabilities, source taterPlaybackMediaInfo) bool {
	videoRange := cleanTaterVideoRange(source.VideoRange)
	if videoRange == "" || videoRange == "sdr" {
		return true
	}
	if !caps.DisplayHDREnabled ||
		!taterPlaybackRangeSupported(caps.VideoHDRFormats, videoRange) ||
		!taterPlaybackRangeSupported(caps.DisplayHDRFormats, videoRange) {
		return false
	}
	if videoRange == "dolby_vision" {
		// Capability contract v5 makes Dolby Vision profile support explicit on
		// tvOS. An empty list must not mean "every profile" because AVPlayer's
		// HLS support is profile- and packaging-specific.
		if strings.EqualFold(strings.TrimSpace(caps.Platform), "tvos") &&
			caps.CapabilityVersion >= 5 && source.DolbyVisionProfile <= 0 {
			return false
		}
		if source.DolbyVisionProfile <= 0 || len(caps.DolbyVisionProfiles) == 0 {
			return true
		}
		for _, profile := range caps.DolbyVisionProfiles {
			if profile == source.DolbyVisionProfile {
				return true
			}
		}
		return false
	}
	return true
}

func taterPlaybackHDRFallbackRange(caps taterPlaybackCapabilities, source taterPlaybackMediaInfo) string {
	if !caps.DisplayHDREnabled {
		return ""
	}
	supports := func(videoRange string) bool {
		return taterPlaybackRangeSupported(caps.VideoHDRFormats, videoRange) &&
			taterPlaybackRangeSupported(caps.DisplayHDRFormats, videoRange)
	}
	switch cleanTaterVideoRange(source.VideoRange) {
	case "hdr10plus":
		if supports("hdr10") {
			return "hdr10"
		}
	case "dolby_vision":
		// Profile 7's base layer is HDR10-compatible, but it must be decoded and
		// re-encoded rather than treated as a simple remux fallback.
		if source.DolbyVisionProfile == 7 && taterPlaybackCanUseHDRHEVC(caps) && supports("hdr10") {
			return "hdr10"
		}
		// Profile 8 is single-layer and can carry a standards-compatible HDR10
		// or HLG base picture. Its Dolby Vision RPU can be stripped when the
		// display cannot use Dolby Vision.
		if source.DolbyVisionProfile == 8 {
			switch source.DolbyVisionCompatibilityID {
			case 1:
				if supports("hdr10") {
					return "hdr10"
				}
			case 4:
				if supports("hlg") {
					return "hlg"
				}
			}
		}
	}
	return ""
}

func taterPlaybackRangePrefix(sourceRange, outputRange string, toneMapped bool) string {
	sourceRange = cleanTaterVideoRange(sourceRange)
	outputRange = cleanTaterVideoRange(outputRange)
	if sourceRange == "" || sourceRange == "sdr" {
		return ""
	}
	label := taterPlaybackRangeLabel(sourceRange)
	if outputRange != "" && outputRange != sourceRange {
		label += " → " + taterPlaybackRangeLabel(outputRange)
	}
	if toneMapped {
		label += " Tone Map"
	} else if outputRange == sourceRange {
		label += " Direct"
	}
	return label + " • "
}

func taterPlaybackRangeLabel(videoRange string) string {
	switch cleanTaterVideoRange(videoRange) {
	case "dolby_vision":
		return "Dolby Vision"
	case "hdr10plus":
		return "HDR10+"
	case "hdr10":
		return "HDR10"
	case "hlg":
		return "HLG"
	case "sdr":
		return "SDR"
	default:
		return "Video"
	}
}

func parsedPlaybackPath(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	if path := strings.TrimSpace(u.Query().Get("path")); path != "" {
		return path
	}
	return u.Path
}

func cleanTaterContainerName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if index := strings.IndexByte(value, ','); index >= 0 {
		value = value[:index]
	}
	value = strings.TrimPrefix(value, ".")
	switch value {
	case "matroska", "mkv":
		return "mkv"
	case "mov", "mp4", "m4a", "3gp", "3g2", "mj2", "m4v":
		return "mp4"
	case "mpegts", "m2ts", "ts":
		return "mpegts"
	case "hls", "m3u8":
		return "hls"
	default:
		return cleanTaterCodecName(value)
	}
}

func cleanTaterPreferredStreamContainer(value string) string {
	switch cleanTaterContainerName(value) {
	case "mpegts":
		return "mpegts"
	case "hls":
		return "hls"
	default:
		return ""
	}
}

func taterContainerListContains(values []string, container string) bool {
	wanted := cleanTaterContainerName(container)
	if wanted == "" {
		return false
	}
	for _, value := range values {
		if cleanTaterContainerName(value) == wanted {
			return true
		}
	}
	return false
}

func cleanTaterCodecName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			out.WriteRune(char)
		}
	}
	codec := out.String()
	switch codec {
	case "h265", "x265":
		return "hevc"
	case "x264", "avc":
		return "h264"
	case "dolbytruehd":
		return "truehd"
	case "ddp", "dolbydigitalplus":
		return "eac3"
	case "dd", "dolbydigital":
		return "ac3"
	case "dtshd", "dtsma", "dtshdma":
		return "dts_hd"
	default:
		return codec
	}
}

func taterCodecListContains(values []string, codec string) bool {
	codec = cleanTaterCodecName(codec)
	for _, value := range values {
		if cleanTaterCodecName(value) == codec {
			return true
		}
	}
	return false
}

func taterPlaybackCodecSuffix(codec string) string {
	codec = cleanTaterCodecName(codec)
	if codec == "" {
		return ""
	}
	labels := map[string]string{
		"aac": "AAC", "ac3": "Dolby Digital", "eac3": "Dolby Digital Plus",
		"truehd": "Dolby TrueHD", "dts": "DTS", "dts_hd": "DTS-HD MA",
		"flac": "FLAC", "opus": "Opus",
	}
	label := labels[codec]
	if label == "" {
		label = strings.ToUpper(codec)
	}
	return " — " + label
}
