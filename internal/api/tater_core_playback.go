package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/TaterTotterson/tater-tube-server/internal/database"
	"github.com/gofiber/fiber/v2"
)

const taterCorePlaybackGrantTTL = 12 * time.Hour

var taterEchoPlaybackProfiles = map[string]bool{
	"echo_show_720p":     true,
	"echo_checkers_480p": true,
	"echo_rook_480p":     true,
}

type taterCorePlaybackResolveRequest struct {
	Query            string `json:"query"`
	CandidateID      string `json:"candidate_id"`
	ProfileID        string `json:"profile"`
	ProfileID2       string `json:"transcode_profile"`
	ViewingProfileID string `json:"viewing_profile_id"`
}

type taterCorePlaybackStateRequest struct {
	PlaybackID string `json:"playback_id"`
	State      string `json:"state"`
	PositionMS int64  `json:"position_ms"`
	DurationMS int64  `json:"duration_ms"`
}

type taterCorePlaybackGrant struct {
	PlaybackID string
	StreamHash string
	CoreID     string
	CoreName   string
	ProfileID  string
	Profile    string
	Item       taterUsenetItem
	Subtitle   string
	ExpiresAt  time.Time
}

func cleanTaterPlaybackQuery(value string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, value)), " ")
}

func taterCorePlaybackProfile(values ...string) string {
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if taterEchoPlaybackProfiles[value] {
			return value
		}
	}
	return "echo_show_720p"
}

func selectTaterCorePlaybackCandidate(candidates []taterCandidate, candidateID, query string) (taterCandidate, bool) {
	candidateID = strings.TrimSpace(candidateID)
	if candidateID != "" {
		for _, candidate := range candidates {
			if candidate.ID == candidateID {
				return candidate, true
			}
		}
		return taterCandidate{}, false
	}
	needle := cleanTaterPlaybackQuery(query)
	if needle == "" {
		return taterCandidate{}, false
	}
	type scored struct {
		candidate taterCandidate
		score     int
	}
	matches := []scored{}
	for _, candidate := range candidates {
		if candidate.Source != "local_media" {
			continue
		}
		title := cleanTaterPlaybackQuery(candidate.Title)
		score := 0
		switch {
		case title == needle:
			score = 1000
		case strings.HasPrefix(title, needle):
			score = 800 - len(title)
		case strings.Contains(title, needle):
			score = 600 - len(title)
		case strings.Contains(needle, title):
			score = 400 - len(needle)
		}
		if score > 0 {
			matches = append(matches, scored{candidate: candidate, score: score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return strings.ToLower(matches[i].candidate.Title) < strings.ToLower(matches[j].candidate.Title)
	})
	if len(matches) == 0 {
		return taterCandidate{}, false
	}
	return matches[0].candidate, true
}

func resolveTaterCorePlaybackItem(cfg *config.Config, candidate taterCandidate) (taterUsenetItem, string, error) {
	item := candidate.Launch
	if item.Type == "localFile" {
		rows := taterAttachLocalPlayStates(cfg, []taterUsenetItem{item})
		item = rows[0]
		taterSetLocalPlayStateIDs(&item)
		return item, "Tater Tube", nil
	}
	if item.Type != "localFolder" {
		return taterUsenetItem{}, "", fmt.Errorf("%s is not a local playable video", candidate.Title)
	}
	categoryID := taterRawLocalCategoryID(item.CategoryID)
	category, ok := taterLocalMediaCategory(cfg, categoryID)
	if !ok {
		return taterUsenetItem{}, "", fmt.Errorf("the library for %s is unavailable", candidate.Title)
	}
	roots := taterLocalMediaCategoryPaths(category)
	if item.SourceIndex < 0 || item.SourceIndex >= len(roots) {
		return taterUsenetItem{}, "", fmt.Errorf("the media source for %s is unavailable", candidate.Title)
	}
	episodes, err := taterLocalSeriesEpisodeItems(
		cfg, category, roots[item.SourceIndex], item.SourceIndex, item.Path, "", "",
	)
	if err != nil || len(episodes) == 0 {
		return taterUsenetItem{}, "", fmt.Errorf("%s has no playable episodes", candidate.Title)
	}
	seriesID := taterLocalSeriesStateID(item.CategoryID, item.SourceIndex, item.Path)
	if store, loadErr := loadTaterPlayStateStore(cfg); loadErr == nil {
		if state, found := store.Items[seriesID]; found {
			if display, usable := taterContinueDisplayState(cfg, state); usable {
				resolved := taterPlayStateToItem(display, "", "")
				taterApplyPlayStateMetadata(cfg, &resolved)
				taterSetLocalPlayStateIDs(&resolved)
				return resolved, candidate.Title, nil
			}
		}
	}
	resolved := episodes[0]
	taterSetLocalPlayStateIDs(&resolved)
	return resolved, candidate.Title, nil
}

func probeTaterCorePlaybackItem(ctx context.Context, cfg *config.Config, item *taterUsenetItem) {
	if cfg == nil || item == nil || strings.TrimSpace(item.Path) == "" {
		return
	}
	category, ok := taterLocalMediaCategory(cfg, taterRawLocalCategoryID(item.CategoryID))
	if !ok {
		return
	}
	roots := taterLocalMediaCategoryPaths(category)
	if item.SourceIndex < 0 || item.SourceIndex >= len(roots) {
		return
	}
	mediaPath, err := safeLocalPath(roots[item.SourceIndex], item.Path)
	if err != nil {
		return
	}
	info, err := probeTaterPlaybackMediaWithCachePolicy(ctx, cfg, mediaPath, false)
	if err != nil {
		return
	}
	if info.DurationSeconds > 0 {
		attachTaterDuration(item, info.DurationSeconds)
	}
	item.VideoWidth = info.Width
	item.VideoHeight = info.Height
	item.VideoCodec = info.VideoCodec
	item.VideoProfile = info.VideoProfile
	item.VideoRange = info.VideoRange
	item.AudioCodec = info.AudioCodec
	item.AudioProfile = info.AudioProfile
	item.AudioChannels = info.AudioChannels
	item.AudioLayout = info.ChannelLayout
}

func taterCorePlaybackStreamURL(baseURL, streamToken, profile string, item taterUsenetItem) string {
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/api/tater/local/stream")
	if err != nil {
		return ""
	}
	query := u.Query()
	cleanPath := cleanLocalRelativePath(item.Path)
	query.Set("category_id", taterRawLocalCategoryID(item.CategoryID))
	query.Set("source", strconv.Itoa(item.SourceIndex))
	query.Set("path", cleanPath)
	query.Set(taterLocalPathRefQuery, taterLocalPathRef(cleanPath))
	query.Set("core_stream_token", streamToken)
	query.Set("transcode", "1")
	query.Set("profile", profile)
	query.Set("codec", "h264")
	query.Set("tater_audio_channels", "2")
	query.Set("tater_output_video_range", "sdr")
	if taterVideoRangeIsHDR(item.VideoRange) {
		query.Set("tater_tone_map", "1")
		query.Set("tater_source_video_range", cleanTaterVideoRange(item.VideoRange))
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func (s *Server) storeTaterCorePlaybackGrant(grant *taterCorePlaybackGrant, streamToken string) {
	if grant == nil {
		return
	}
	s.taterCorePlaybackGrants.Store("playback:"+grant.PlaybackID, grant)
	s.taterCorePlaybackGrants.Store("stream:"+hashTaterSecret(streamToken), grant)
}

func (s *Server) taterCorePlaybackGrant(key string) (*taterCorePlaybackGrant, bool) {
	value, found := s.taterCorePlaybackGrants.Load(key)
	if !found {
		return nil, false
	}
	grant, ok := value.(*taterCorePlaybackGrant)
	if !ok || grant == nil || time.Now().After(grant.ExpiresAt) {
		s.taterCorePlaybackGrants.Delete(key)
		if grant != nil {
			s.taterCorePlaybackGrants.Delete("playback:" + grant.PlaybackID)
			s.taterCorePlaybackGrants.Delete("stream:" + grant.StreamHash)
		}
		return nil, false
	}
	return grant, true
}

// AuthorizeTaterCorePlaybackStream grants only the exact media path resolved
// by the authenticated Core. It never promotes the Core credential to a
// general Tater Tube player token.
func (s *Server) AuthorizeTaterCorePlaybackStream(_ context.Context, token, categoryID string, sourceIndex int, relPath string) (*config.PlayerConfig, bool) {
	grant, ok := s.taterCorePlaybackGrant("stream:" + hashTaterSecret(token))
	if !ok || taterRawLocalCategoryID(categoryID) != taterRawLocalCategoryID(grant.Item.CategoryID) ||
		sourceIndex != grant.Item.SourceIndex || cleanLocalRelativePath(relPath) != cleanLocalRelativePath(grant.Item.Path) {
		return nil, false
	}
	return &config.PlayerConfig{ID: "tater-core-" + grant.CoreID, Name: grant.CoreName}, true
}

func (s *Server) handleTaterCoreResolvePlayback(c *fiber.Ctx) error {
	core, ok := s.taterCoreAuthorized(c)
	if !ok {
		return nil
	}
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondServiceUnavailable(c, "Configuration not available", "")
	}
	var req taterCorePlaybackResolveRequest
	if err := c.BodyParser(&req); err != nil {
		return RespondValidationError(c, "Invalid playback request", err.Error())
	}
	profileID := taterCorePlaybackProfile(req.ProfileID, req.ProfileID2)
	viewingProfileID := cleanTaterProfileID(req.ViewingProfileID)
	candidates := s.taterRecommendationCandidates(c.Context(), cfg, resolveBaseURL(c, ""), viewingProfileID)
	candidate, found := selectTaterCorePlaybackCandidate(candidates, req.CandidateID, req.Query)
	if !found {
		return RespondNotFound(c, "Tater Tube video", strings.TrimSpace(req.Query))
	}
	item, subtitle, err := resolveTaterCorePlaybackItem(cfg, candidate)
	if err != nil {
		return RespondValidationError(c, "Video is not playable", err.Error())
	}
	probeTaterCorePlaybackItem(c.Context(), cfg, &item)
	playbackID, err := randomHex(16)
	if err != nil {
		return RespondInternalError(c, "Failed to create playback session", err.Error())
	}
	streamToken, err := randomHex(32)
	if err != nil {
		return RespondInternalError(c, "Failed to create playback stream", err.Error())
	}
	expiresAt := time.Now().UTC().Add(taterCorePlaybackGrantTTL)
	grant := &taterCorePlaybackGrant{
		PlaybackID: playbackID, StreamHash: hashTaterSecret(streamToken),
		CoreID: core.ID, CoreName: core.Name, ProfileID: viewingProfileID,
		Profile: profileID, Item: item, Subtitle: subtitle, ExpiresAt: expiresAt,
	}
	s.storeTaterCorePlaybackGrant(grant, streamToken)
	durationMS := int64(math.Round(item.DurationSeconds * 1000))
	if durationMS <= 0 && item.Duration > 0 {
		durationMS = item.Duration * 1000
	}
	profile := transcodeProfiles[profileID]
	return RespondSuccess(c, fiber.Map{
		"playback_id": playbackID, "candidate_id": candidate.ID,
		"title": item.Title, "subtitle": subtitle, "media_type": item.MediaType,
		"stream_url":  taterCorePlaybackStreamURL(resolveBaseURL(c, ""), streamToken, profileID, item),
		"position_ms": max(item.ViewOffset, int64(0)), "duration_ms": max(durationMS, int64(0)),
		"fps": 30, "transcode_profile": profileID, "transcode_name": profile.Name,
		"output_width": profile.MaxWidth, "output_height": profile.MaxHeight,
		"expires_at": expiresAt,
	})
}

func (s *Server) handleTaterCorePlaybackState(c *fiber.Ctx) error {
	core, ok := s.taterCoreAuthorized(c)
	if !ok {
		return nil
	}
	var req taterCorePlaybackStateRequest
	if err := c.BodyParser(&req); err != nil {
		return RespondValidationError(c, "Invalid playback state", err.Error())
	}
	grant, found := s.taterCorePlaybackGrant("playback:" + strings.TrimSpace(req.PlaybackID))
	if !found || grant.CoreID != core.ID {
		return RespondNotFound(c, "Tater Core playback session", req.PlaybackID)
	}
	stateName := strings.ToLower(strings.TrimSpace(req.State))
	switch stateName {
	case "play", "playing", "resume", "progress", "pause", "paused", "stop", "stopped", "ended", "completed", "error":
	default:
		return RespondValidationError(c, "Unsupported playback state", stateName)
	}
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondServiceUnavailable(c, "Configuration not available", "")
	}
	item := grant.Item
	taterSetLocalPlayStateIDs(&item)
	durationMS := max(req.DurationMS, int64(math.Round(item.DurationSeconds*1000)))
	positionMS := max(req.PositionMS, int64(0))
	completed := stateName == "ended" || stateName == "completed"
	state := taterPlayState{
		ID: item.PlayStateID, SeriesID: item.SeriesStateID, Title: item.Title,
		SeriesTitle: grant.Subtitle, MediaType: item.MediaType,
		CategoryID: item.CategoryID, SourceIndex: item.SourceIndex, Path: item.Path,
		PositionMS: positionMS, DurationMS: durationMS, Completed: completed,
		UpdatedAt: time.Now().UTC(),
	}
	if strings.EqualFold(state.MediaType, "episode") && state.SeriesID != "" {
		state.ID = state.SeriesID
	}
	store, err := loadTaterPlayStateStore(cfg)
	if err != nil {
		return RespondServiceUnavailable(c, "Failed to load play state", err.Error())
	}
	store.Items[state.ID] = state
	if err := saveTaterPlayStateStore(cfg, store); err != nil {
		return RespondServiceUnavailable(c, "Failed to save play state", err.Error())
	}
	if s.queueRepo != nil {
		viewingState := map[string]string{
			"play": "started", "playing": "started", "resume": "progress", "progress": "progress",
			"pause": "paused", "paused": "paused", "stop": "stopped", "stopped": "stopped",
			"ended": "completed", "completed": "completed", "error": "stopped",
		}[stateName]
		metadata, _ := json.Marshal(map[string]any{
			"action": stateName, "transcode_profile": grant.Profile,
			"category_id": item.CategoryID, "source_index": item.SourceIndex, "path": item.Path,
		})
		_ = s.queueRepo.UpsertTaterViewingEvent(c.Context(), database.TaterViewingEvent{
			EventID: grant.PlaybackID, ProfileID: grant.ProfileID,
			PlayerID: "tater-core-" + core.ID, Source: "local_media",
			MediaID: state.ID, MediaType: item.MediaType, Title: item.Title,
			SeriesTitle: grant.Subtitle, PositionMS: positionMS, DurationMS: durationMS,
			State: viewingState, OccurredAt: time.Now().UTC(), MetadataJSON: string(metadata), CreatedAt: time.Now().UTC(),
		})
	}
	return RespondSuccess(c, fiber.Map{"saved": true, "completed": completed})
}
