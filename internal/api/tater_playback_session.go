package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
)

const taterPlaybackProbeTimeout = 12 * time.Second

type taterPlaybackCapabilities struct {
	CapabilityVersion    int      `json:"capability_version"`
	Platform             string   `json:"platform"`
	Engine               string   `json:"engine"`
	OutputName           string   `json:"output_name"`
	OutputConnection     string   `json:"output_connection"`
	Containers           []string `json:"containers"`
	VideoCodecs          []string `json:"video_codecs"`
	AudioCodecs          []string `json:"audio_codecs"`
	AudioPassthrough     []string `json:"audio_passthrough"`
	MaxWidth             int      `json:"max_width"`
	MaxHeight            int      `json:"max_height"`
	MaxAudioChannels     int      `json:"max_audio_channels"`
	AudioDownmix         bool     `json:"audio_downmix"`
	CompatibilityMode    bool     `json:"compatibility_mode"`
	PassthroughAvailable bool     `json:"passthrough_available"`
	VideoHDRFormats      []string `json:"video_hdr_formats"`
	DisplayHDRFormats    []string `json:"display_hdr_formats"`
	DisplayHDREnabled    bool     `json:"display_hdr_enabled"`
	MaxVideoBitDepth     int      `json:"max_video_bit_depth"`
	DolbyVisionProfiles  []int    `json:"dolby_vision_profiles"`
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
	StreamURL          string                 `json:"stream_url"`
	Mode               string                 `json:"mode"`
	VideoMode          string                 `json:"video_mode"`
	AudioMode          string                 `json:"audio_mode"`
	VideoCodec         string                 `json:"video_codec,omitempty"`
	AudioCodec         string                 `json:"audio_codec,omitempty"`
	QualityLabel       string                 `json:"quality_label"`
	Reason             string                 `json:"reason,omitempty"`
	OutputName         string                 `json:"output_name,omitempty"`
	OutputConnection   string                 `json:"output_connection,omitempty"`
	SourceVideoRange   string                 `json:"source_video_range"`
	OutputVideoRange   string                 `json:"output_video_range"`
	ToneMapped         bool                   `json:"tone_mapped"`
	SelectedAudioTrack int                    `json:"selected_audio_track"`
	Source             taterPlaybackMediaInfo `json:"source"`
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
	} `json:"format"`
}

func (s *Server) handleTaterPlayerPlaybackSession(c *fiber.Ctx) error {
	cfg, _, ok := s.taterAuthorizedConfig(c)
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

	source := taterPlaybackMediaInfo{
		Container: cleanTaterCodecName(filepath.Ext(parsedPlaybackPath(req.StreamURL))),
	}
	if localPath, found, err := taterPlaybackLocalSourcePath(cfg, req.StreamURL); err != nil {
		return RespondValidationError(c, "Playback source is invalid", err.Error())
	} else if found {
		if probed, probeErr := probeTaterPlaybackMedia(c.Context(), cfg, localPath); probeErr == nil {
			source = probed
		}
	}

	plan := buildTaterPlaybackPlan(req, source)
	return RespondSuccess(c, plan)
}

func buildTaterPlaybackPlan(req taterPlaybackSessionRequest, source taterPlaybackMediaInfo) taterPlaybackSessionResponse {
	caps := req.Capabilities
	selectedAudioTrack := selectTaterPlaybackAudioTrack(&source, req.AudioTrack, &caps)
	profile := strings.TrimSpace(req.Profile)
	if _, ok := transcodeProfiles[profile]; !ok {
		profile = "hdmi_1080p"
	}

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

	sourceRange := cleanTaterVideoRange(source.VideoRange)
	if sourceRange == "" {
		sourceRange = "sdr"
	}
	outputRange := sourceRange
	toneMapped := false
	rangeFallback := false
	if sourceRange != "sdr" {
		if !taterPlaybackCanOutputRange(caps, source) {
			if taterPlaybackCanUseHDRFallback(caps, source) {
				outputRange = "hdr10"
				rangeFallback = true
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

	plan := taterPlaybackSessionResponse{
		StreamURL:          req.StreamURL,
		Mode:               "direct",
		VideoMode:          "direct",
		AudioMode:          "direct",
		VideoCodec:         videoCodec,
		AudioCodec:         audioCodec,
		OutputName:         strings.TrimSpace(caps.OutputName),
		OutputConnection:   strings.TrimSpace(caps.OutputConnection),
		SourceVideoRange:   sourceRange,
		OutputVideoRange:   outputRange,
		ToneMapped:         toneMapped,
		SelectedAudioTrack: selectedAudioTrack,
		Source:             source,
	}
	if passthrough {
		plan.AudioMode = "bitstream"
	}

	switch {
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
		plan.StreamURL = taterPlaybackPlannedURL(req.StreamURL, "audio", profile, "h264", "", selectedAudioTrack)
		plan.QualityLabel = taterPlaybackRangePrefix(sourceRange, outputRange, false) + "Video Direct • Audio AAC"
		plan.Reason = "The source video is compatible, but its audio needs conversion."
	case audioCompatible:
		plan.Mode = "video_transcode"
		plan.VideoMode = "transcode"
		plan.VideoCodec = "h264"
		plan.StreamURL = taterPlaybackPlannedURL(req.StreamURL, "video", profile, "h264", audioCodec, selectedAudioTrack)
		if passthrough {
			plan.QualityLabel = taterPlaybackRangePrefix(sourceRange, outputRange, toneMapped) + "Video H.264 • Audio Bitstream" + taterPlaybackCodecSuffix(audioCodec)
			plan.Reason = "The source audio is preserved while the video is converted."
		} else {
			plan.QualityLabel = taterPlaybackRangePrefix(sourceRange, outputRange, toneMapped) + "Video H.264 • Audio Direct" + taterPlaybackCodecSuffix(audioCodec)
			plan.Reason = "The source audio is compatible, so only the video is converted."
		}
	default:
		plan.Mode = "full_transcode"
		plan.VideoMode = "transcode"
		plan.AudioMode = "transcode"
		plan.VideoCodec = "h264"
		plan.AudioCodec = "aac"
		plan.StreamURL = taterPlaybackPlannedURL(req.StreamURL, "full", profile, "h264", "", selectedAudioTrack)
		plan.QualityLabel = taterPlaybackRangePrefix(sourceRange, outputRange, toneMapped) + "Video H.264 • Audio AAC"
		plan.Reason = "Both source tracks need conversion for this player."
	}
	if toneMapped {
		if audioCompatible {
			plan.Reason = "The connected display cannot use the source picture format, so only the video is tone-mapped."
		} else {
			plan.Reason = "The picture is tone-mapped for the connected display and the audio is converted for the player."
		}
	}
	plan.StreamURL = annotateTaterPlaybackURL(
		plan.StreamURL, plan.VideoMode, plan.AudioMode, plan.AudioCodec,
		sourceRange, outputRange, toneMapped, selectedAudioTrack,
	)
	return plan
}

func annotateTaterPlaybackURL(rawURL, videoMode, audioMode, audioCodec, sourceRange, outputRange string, toneMapped bool, audioTrack int) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return rawURL
	}
	query := u.Query()
	query.Set("tater_video_mode", cleanTaterCodecName(videoMode))
	query.Set("tater_audio_mode", cleanTaterCodecName(audioMode))
	if codec := cleanTaterCodecName(audioCodec); codec != "" {
		query.Set("tater_audio_codec", codec)
	} else {
		query.Del("tater_audio_codec")
	}
	query.Set("tater_source_video_range", cleanTaterVideoRange(sourceRange))
	query.Set("tater_output_video_range", cleanTaterVideoRange(outputRange))
	if toneMapped {
		query.Set("tater_tone_map", "1")
	} else {
		query.Del("tater_tone_map")
	}
	if audioTrack >= 0 {
		query.Set("tater_audio_track", strconv.Itoa(audioTrack))
	} else {
		query.Del("tater_audio_track")
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func taterPlaybackPlannedURL(rawURL, mode, profile, videoCodec, audioCodec string, audioTrack int) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return rawURL
	}
	query := u.Query()
	for _, key := range []string{"direct", "transcode", "profile", "codec", "audio_codec", "start", "tater_audio_track", "tater_tone_map", "tater_source_video_range", "tater_output_video_range"} {
		query.Del(key)
	}
	switch mode {
	case "audio":
		query.Set("transcode", "audio")
	case "video":
		query.Set("transcode", "video")
	case "full":
		query.Set("transcode", "1")
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
		"-show_entries", "format=format_name:stream=index,codec_type,codec_name,profile,width,height,channels,channel_layout,pix_fmt,color_space,color_transfer,color_primaries,bits_per_raw_sample,bit_rate:stream_tags=language,title:stream_disposition=default,comment,visual_impaired:stream_side_data=side_data_type,dv_profile,dv_level,rpu_present_flag,el_present_flag,bl_present_flag,dv_bl_signal_compatibility_id",
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
	if videoRange == "dolby_vision" && source.DolbyVisionProfile > 0 &&
		len(caps.DolbyVisionProfiles) > 0 {
		for _, profile := range caps.DolbyVisionProfiles {
			if profile == source.DolbyVisionProfile {
				return true
			}
		}
		return false
	}
	return true
}

func taterPlaybackCanUseHDRFallback(caps taterPlaybackCapabilities, source taterPlaybackMediaInfo) bool {
	if !caps.DisplayHDREnabled ||
		!taterPlaybackRangeSupported(caps.VideoHDRFormats, "hdr10") ||
		!taterPlaybackRangeSupported(caps.DisplayHDRFormats, "hdr10") {
		return false
	}
	switch cleanTaterVideoRange(source.VideoRange) {
	case "hdr10plus":
		return true
	case "dolby_vision":
		return source.DolbyVisionProfile == 7 || source.DolbyVisionCompatibilityID == 1
	default:
		return false
	}
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
	default:
		return cleanTaterCodecName(value)
	}
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
