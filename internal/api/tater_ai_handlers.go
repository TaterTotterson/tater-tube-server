package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/TaterTotterson/tater-tube-server/internal/database"
	"github.com/gofiber/fiber/v2"
)

const (
	taterCorePairingCodeTTL = 10 * time.Minute
	taterDefaultProfileID   = "household"
	taterTTSRequestTTL      = 10 * time.Minute
	taterTTSMaxTextRunes    = 800
	taterTTSMaxAudioBytes   = 8 * 1024 * 1024
)

type taterPairCoreRequest struct {
	PIN           string `json:"pin"`
	Name          string `json:"name"`
	AssistantName string `json:"assistant_name"`
}

type taterCorePairResponse struct {
	CoreID        string `json:"core_id"`
	CoreName      string `json:"core_name"`
	AssistantName string `json:"assistant_name"`
	Token         string `json:"token"`
}

type taterViewingEventRequest struct {
	EventID     string          `json:"event_id"`
	ProfileID   string          `json:"profile_id"`
	Source      string          `json:"source"`
	MediaID     string          `json:"media_id"`
	MediaType   string          `json:"media_type"`
	Title       string          `json:"title"`
	SeriesTitle string          `json:"series_title"`
	Season      int             `json:"season"`
	Episode     int             `json:"episode"`
	PositionMS  int64           `json:"position_ms"`
	DurationMS  int64           `json:"duration_ms"`
	State       string          `json:"state"`
	OccurredAt  string          `json:"occurred_at"`
	Metadata    json.RawMessage `json:"metadata"`
}

type taterCandidate struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	MediaType   string          `json:"media_type"`
	Source      string          `json:"source"`
	Year        string          `json:"year,omitempty"`
	Description string          `json:"description,omitempty"`
	Launch      taterUsenetItem `json:"launch"`
}

type taterRecommendationSelection struct {
	CandidateID string `json:"candidate_id"`
	Reason      string `json:"reason"`
}

type taterRecommendationRequest struct {
	ProfileID      string                         `json:"profile_id"`
	AssistantName  string                         `json:"assistant_name"`
	Summary        string                         `json:"summary"`
	ExpiresInHours int                            `json:"expires_in_hours"`
	Items          []taterRecommendationSelection `json:"items"`
}

type taterRecommendationResponse struct {
	ID          string         `json:"id"`
	Rank        int            `json:"rank"`
	CandidateID string         `json:"candidate_id"`
	Title       string         `json:"title"`
	MediaType   string         `json:"media_type"`
	Source      string         `json:"source"`
	Reason      string         `json:"reason"`
	Feedback    string         `json:"feedback,omitempty"`
	Launch      map[string]any `json:"launch"`
}

type taterTTSCreateRequest struct {
	ProfileID        string `json:"profile_id"`
	RecommendationID string `json:"recommendation_id"`
	BatchID          string `json:"batch_id"`
	LocalHour        int    `json:"local_hour"`
}

type taterTTSCompleteRequest struct {
	AudioBase64 string `json:"audio_base64"`
	ContentType string `json:"content_type"`
	Error       string `json:"error"`
}

func (s *Server) handleTaterPairCore(c *fiber.Ctx) error {
	if s.queueRepo == nil {
		return RespondServiceUnavailable(c, "Database not available", "")
	}
	var req taterPairCoreRequest
	if err := c.BodyParser(&req); err != nil {
		return RespondValidationError(c, "Invalid pairing request", err.Error())
	}
	pin := normalizePairingPIN(req.PIN)
	if len(pin) != 6 {
		return RespondValidationError(c, "Pairing PIN must be 6 digits", "")
	}
	name := cleanTaterText(req.Name)
	if name == "" {
		name = "Tater Tube Core"
	}
	token, err := randomHex(32)
	if err != nil {
		return RespondInternalError(c, "Failed to create Tater Core token", err.Error())
	}
	id, err := randomHex(8)
	if err != nil {
		return RespondInternalError(c, "Failed to create Tater Core connection", err.Error())
	}
	now := time.Now().UTC()
	assistantName := cleanTaterAssistantFirstName(req.AssistantName)
	if assistantName == "" {
		assistantName = "Tater"
	}
	ok, err := s.queueRepo.PairTaterCore(c.Context(), hashTaterSecret(pin), now, database.TaterCoreConnection{
		ID:            id,
		Name:          name,
		AssistantName: assistantName,
		TokenHash:     hashTaterSecret(token),
		CreatedAt:     now,
		LastSeenAt:    sql.NullTime{Time: now, Valid: true},
	})
	if err != nil {
		return RespondInternalError(c, "Failed to pair Tater Core", err.Error())
	}
	if !ok {
		return RespondUnauthorized(c, "Invalid or expired pairing PIN", "")
	}
	return RespondSuccess(c, taterCorePairResponse{
		CoreID: id, CoreName: name, AssistantName: assistantName, Token: token,
	})
}

func (s *Server) handleTaterCreateCorePairingCode(c *fiber.Ctx) error {
	if s.queueRepo == nil {
		return RespondServiceUnavailable(c, "Database not available", "")
	}
	var req taterCreatePairingCodeRequest
	_ = c.BodyParser(&req)
	name := cleanTaterText(req.Name)
	if name == "" {
		name = "Tater Tube Core"
	}
	code, err := randomDigits(6)
	if err != nil {
		return RespondInternalError(c, "Failed to create pairing PIN", err.Error())
	}
	id, err := randomHex(8)
	if err != nil {
		return RespondInternalError(c, "Failed to create pairing PIN", err.Error())
	}
	now := time.Now().UTC()
	item := database.TaterCorePairingCode{
		ID:        id,
		Name:      name,
		CodeHash:  hashTaterSecret(code),
		CreatedAt: now,
		ExpiresAt: now.Add(taterCorePairingCodeTTL),
	}
	if err := s.queueRepo.CreateTaterCorePairingCode(c.Context(), item); err != nil {
		return RespondInternalError(c, "Failed to save pairing PIN", err.Error())
	}
	return RespondSuccess(c, fiber.Map{
		"id": item.ID, "name": item.Name, "code": code,
		"created_at": item.CreatedAt, "expires_at": item.ExpiresAt,
	})
}

func (s *Server) taterCoreAuthorized(c *fiber.Ctx) (*database.TaterCoreConnection, bool) {
	if s.queueRepo == nil {
		RespondServiceUnavailable(c, "Database not available", "")
		return nil, false
	}
	token := bearerToken(c.Get("Authorization"))
	if token == "" {
		token = strings.TrimSpace(c.Get("X-Tater-Core-Token"))
	}
	if token == "" {
		RespondUnauthorized(c, "Authentication required", "Pair Tater Tube Core with this server")
		return nil, false
	}
	core, err := s.queueRepo.FindTaterCoreByTokenHash(c.Context(), hashTaterSecret(token))
	if err != nil {
		RespondUnauthorized(c, "Invalid Tater Core token", "")
		return nil, false
	}
	reportedName := taterAssistantNameFromHeader(c.Get("X-Tater-Assistant-Name"))
	if reportedName != "" {
		core.AssistantName = reportedName
	}
	if core.AssistantName == "" {
		core.AssistantName = "Tater"
	}
	_ = s.queueRepo.TouchTaterCore(c.Context(), core.ID, reportedName, time.Now().UTC())
	return core, true
}

func (s *Server) handleTaterSaveViewingEvent(c *fiber.Ctx) error {
	cfg, token, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}
	if s.queueRepo == nil {
		return RespondServiceUnavailable(c, "Database not available", "")
	}
	player, ok := findTaterPlayerByToken(cfg, token)
	if !ok {
		return RespondUnauthorized(c, "Invalid player token", "")
	}
	var req taterViewingEventRequest
	if err := c.BodyParser(&req); err != nil {
		return RespondValidationError(c, "Invalid viewing event", err.Error())
	}
	req.EventID = strings.TrimSpace(req.EventID)
	req.MediaID = strings.TrimSpace(req.MediaID)
	req.Title = cleanTaterText(req.Title)
	if req.EventID == "" || req.MediaID == "" || req.Title == "" {
		return RespondValidationError(c, "event_id, media_id, and title are required", "")
	}
	state := strings.ToLower(strings.TrimSpace(req.State))
	switch state {
	case "started", "progress", "paused", "completed", "stopped":
	default:
		return RespondValidationError(c, "Unsupported viewing state", "")
	}
	profileID := cleanTaterProfileID(req.ProfileID)
	occurredAt := time.Now().UTC()
	if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(req.OccurredAt)); err == nil {
		occurredAt = parsed.UTC()
	}
	metadata := strings.TrimSpace(string(req.Metadata))
	if metadata == "" || !json.Valid([]byte(metadata)) {
		metadata = "{}"
	}
	now := time.Now().UTC()
	item := database.TaterViewingEvent{
		EventID: req.EventID, ProfileID: profileID, PlayerID: player.ID,
		Source: cleanTaterSlug(req.Source, "unknown"), MediaID: req.MediaID,
		MediaType: cleanTaterSlug(req.MediaType, "video"), Title: req.Title,
		SeriesTitle: cleanTaterText(req.SeriesTitle), Season: max(req.Season, 0),
		Episode: max(req.Episode, 0), PositionMS: max(req.PositionMS, 0),
		DurationMS: max(req.DurationMS, 0), State: state, OccurredAt: occurredAt,
		MetadataJSON: metadata, CreatedAt: now,
	}
	if err := s.queueRepo.UpsertTaterViewingEvent(c.Context(), item); err != nil {
		return RespondInternalError(c, "Failed to save viewing event", err.Error())
	}
	return RespondSuccess(c, fiber.Map{"event_id": item.EventID})
}

func (s *Server) handleTaterCoreContext(c *fiber.Ctx) error {
	if _, ok := s.taterCoreAuthorized(c); !ok {
		return nil
	}
	profileID := cleanTaterProfileID(c.Query("profile_id"))
	limit := queryInt(c, "limit", 30, 1, 100)
	items, err := s.queueRepo.ListTaterViewingEvents(c.Context(), profileID, limit)
	if err != nil {
		return RespondInternalError(c, "Failed to load viewing context", err.Error())
	}
	completed := 0
	inProgress := 0
	for _, item := range items {
		if item.State == "completed" {
			completed++
		} else if item.State == "progress" || item.State == "paused" {
			inProgress++
		}
	}
	return RespondSuccess(c, fiber.Map{
		"profile_id":   profileID,
		"summary":      fmt.Sprintf("%d recent titles: %d completed and %d in progress", len(items), completed, inProgress),
		"events":       items,
		"generated_at": time.Now().UTC(),
	})
}

func (s *Server) handleTaterCoreCandidates(c *fiber.Ctx) error {
	if _, ok := s.taterCoreAuthorized(c); !ok {
		return nil
	}
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondServiceUnavailable(c, "Configuration not available", "")
	}
	profileID := cleanTaterProfileID(c.Query("profile_id"))
	candidates := s.taterRecommendationCandidates(
		c.Context(), cfg, resolveBaseURL(c, ""), profileID,
	)
	limit := queryInt(c, "limit", 200, 1, 500)
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return RespondSuccess(c, fiber.Map{"candidates": candidates, "generated_at": time.Now().UTC()})
}

func (s *Server) handleTaterCoreSaveRecommendations(c *fiber.Ctx) error {
	core, ok := s.taterCoreAuthorized(c)
	if !ok {
		return nil
	}
	var req taterRecommendationRequest
	if err := c.BodyParser(&req); err != nil {
		return RespondValidationError(c, "Invalid recommendations", err.Error())
	}
	if assistantName := cleanTaterAssistantFirstName(req.AssistantName); assistantName != "" {
		core.AssistantName = assistantName
		_ = s.queueRepo.TouchTaterCore(c.Context(), core.ID, assistantName, time.Now().UTC())
	}
	if len(req.Items) == 0 {
		return RespondValidationError(c, "At least one recommendation is required", "")
	}
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondServiceUnavailable(c, "Configuration not available", "")
	}
	profileID := cleanTaterProfileID(req.ProfileID)
	available := s.taterRecommendationCandidates(
		c.Context(), cfg, resolveBaseURL(c, ""), profileID,
	)
	byID := make(map[string]taterCandidate, len(available))
	for _, candidate := range available {
		byID[candidate.ID] = candidate
	}
	now := time.Now().UTC()
	batchID, err := randomHex(12)
	if err != nil {
		return RespondInternalError(c, "Failed to create recommendation batch", err.Error())
	}
	hours := req.ExpiresInHours
	if hours < 1 || hours > 168 {
		hours = 24
	}
	batch := database.TaterRecommendationBatch{
		ID: batchID, ProfileID: profileID, CoreID: core.ID,
		Summary: cleanTaterText(req.Summary), GeneratedAt: now,
		ExpiresAt: now.Add(time.Duration(hours) * time.Hour),
	}
	items := make([]database.TaterRecommendation, 0, min(len(req.Items), 12))
	seen := map[string]bool{}
	for _, selected := range req.Items {
		candidate, exists := byID[strings.TrimSpace(selected.CandidateID)]
		if !exists || seen[candidate.ID] || len(items) >= 12 {
			continue
		}
		seen[candidate.ID] = true
		id, err := randomHex(12)
		if err != nil {
			return RespondInternalError(c, "Failed to create recommendation", err.Error())
		}
		launchJSON, _ := json.Marshal(candidate.Launch)
		reason := cleanTaterText(selected.Reason)
		if reason == "" {
			reason = "Tater thinks this belongs on your screen."
		}
		items = append(items, database.TaterRecommendation{
			ID: id, BatchID: batchID, Rank: len(items) + 1, CandidateID: candidate.ID,
			Title: candidate.Title, MediaType: candidate.MediaType, Source: candidate.Source,
			Reason: reason, LaunchJSON: string(launchJSON), CreatedAt: now,
		})
	}
	if len(items) == 0 {
		return RespondValidationError(c, "No valid candidate IDs were supplied", "")
	}
	if err := s.queueRepo.SaveTaterRecommendations(c.Context(), batch, items); err != nil {
		return RespondInternalError(c, "Failed to save recommendations", err.Error())
	}
	return RespondSuccess(c, fiber.Map{
		"batch_id": batch.ID, "count": len(items), "expires_at": batch.ExpiresAt,
		"assistant_name": core.AssistantName,
	})
}

func (s *Server) handleTaterPlayerRecommendations(c *fiber.Ctx) error {
	cfg, token, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}
	if _, ok := findTaterPlayerByToken(cfg, token); !ok {
		return RespondUnauthorized(c, "Invalid player token", "")
	}
	profileID := cleanTaterProfileID(c.Query("profile_id"))
	batch, items, err := s.queueRepo.GetActiveTaterRecommendations(c.Context(), profileID, time.Now().UTC())
	if err == sql.ErrNoRows {
		return RespondSuccess(c, fiber.Map{"profile_id": profileID, "items": []any{}})
	}
	if err != nil {
		return RespondInternalError(c, "Failed to load recommendations", err.Error())
	}
	baseURL := resolveBaseURL(c, "")
	responseItems := make([]taterRecommendationResponse, 0, len(items))
	for _, item := range items {
		if item.Feedback == "dismissed" || item.Feedback == "not_for_me" {
			continue
		}
		var launch taterUsenetItem
		if err := json.Unmarshal([]byte(item.LaunchJSON), &launch); err != nil {
			continue
		}
		categoryID := taterRawLocalCategoryID(launch.CategoryID)
		if launch.Type == "localFile" && categoryID != "" {
			launch.StreamURL = taterLocalStreamURL(baseURL, categoryID, launch.SourceIndex, launch.Path, token)
		}
		raw, _ := json.Marshal(launch)
		launchMap := map[string]any{}
		_ = json.Unmarshal(raw, &launchMap)
		responseItems = append(responseItems, taterRecommendationResponse{
			ID: item.ID, Rank: item.Rank, CandidateID: item.CandidateID, Title: item.Title,
			MediaType: item.MediaType, Source: item.Source, Reason: item.Reason,
			Feedback: item.Feedback, Launch: launchMap,
		})
	}
	return RespondSuccess(c, fiber.Map{"batch": batch, "profile_id": profileID, "items": responseItems})
}

func (s *Server) handleTaterRecommendationFeedback(c *fiber.Ctx) error {
	if _, _, ok := s.taterAuthorizedConfig(c); !ok {
		return nil
	}
	var req struct {
		Feedback string `json:"feedback"`
	}
	if err := c.BodyParser(&req); err != nil {
		return RespondValidationError(c, "Invalid feedback", err.Error())
	}
	feedback := strings.ToLower(strings.TrimSpace(req.Feedback))
	switch feedback {
	case "played", "liked", "dismissed", "not_for_me":
	default:
		return RespondValidationError(c, "Unsupported feedback", "")
	}
	if err := s.queueRepo.SetTaterRecommendationFeedback(c.Context(), c.Params("id"), feedback, time.Now().UTC()); err != nil {
		return RespondNotFound(c, "Recommendation", c.Params("id"))
	}
	return RespondSuccess(c, fiber.Map{"id": c.Params("id"), "feedback": feedback})
}

func (s *Server) handleTaterPlayerCreateTTSRequest(c *fiber.Ctx) error {
	cfg, token, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}
	if s.queueRepo == nil {
		return RespondServiceUnavailable(c, "Database not available", "")
	}
	player, ok := findTaterPlayerByToken(cfg, token)
	if !ok {
		return RespondUnauthorized(c, "Invalid player token", "")
	}
	var req taterTTSCreateRequest
	if err := c.BodyParser(&req); err != nil {
		return RespondValidationError(c, "Invalid TTS request", err.Error())
	}
	profileID := cleanTaterProfileID(req.ProfileID)
	recommendationID := strings.TrimSpace(req.RecommendationID)
	batchID := strings.TrimSpace(req.BatchID)
	var text string
	var err error
	if batchID != "" {
		text, err = s.queueRepo.GetActiveTaterRecommendationSummary(
			c.Context(), batchID, profileID, time.Now().UTC(),
		)
	} else if recommendationID != "" {
		// Compatibility with players released before batch briefings.
		text, err = s.queueRepo.GetActiveTaterRecommendationReason(
			c.Context(), recommendationID, profileID, time.Now().UTC(),
		)
	} else {
		return RespondValidationError(c, "batch_id is required", "")
	}
	if err == sql.ErrNoRows {
		lookupID := batchID
		if lookupID == "" {
			lookupID = recommendationID
		}
		return RespondNotFound(c, "Recommendation briefing", lookupID)
	}
	if err != nil {
		return RespondInternalError(c, "Failed to load recommendation briefing", err.Error())
	}
	text = cleanTaterText(text)
	if text == "" {
		return RespondValidationError(c, "Recommendation briefing is empty", "")
	}
	if batchID != "" {
		text = taterGreetingForHour(req.LocalHour) + ". " + text
	}
	runes := []rune(text)
	if len(runes) > taterTTSMaxTextRunes {
		text = strings.TrimSpace(string(runes[:taterTTSMaxTextRunes]))
	}
	id, err := randomHex(12)
	if err != nil {
		return RespondInternalError(c, "Failed to create TTS request", err.Error())
	}
	now := time.Now().UTC()
	item := database.TaterTTSRequest{
		ID: id, ProfileID: profileID, PlayerID: player.ID,
		Text: text, Status: "pending", ContentType: "audio/wav",
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(taterTTSRequestTTL),
	}
	if err := s.queueRepo.CreateTaterTTSRequest(c.Context(), item); err != nil {
		return RespondInternalError(c, "Failed to queue TTS request", err.Error())
	}
	return RespondCreated(c, fiber.Map{
		"id": item.ID, "status": item.Status, "expires_at": item.ExpiresAt,
	})
}

func (s *Server) handleTaterPlayerTTSRequest(c *fiber.Ctx) error {
	cfg, token, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}
	player, ok := findTaterPlayerByToken(cfg, token)
	if !ok {
		return RespondUnauthorized(c, "Invalid player token", "")
	}
	item, err := s.queueRepo.GetTaterTTSRequest(c.Context(), strings.TrimSpace(c.Params("id")), player.ID)
	if err == sql.ErrNoRows {
		return RespondNotFound(c, "TTS request", c.Params("id"))
	}
	if err != nil {
		return RespondInternalError(c, "Failed to load TTS request", err.Error())
	}
	data := fiber.Map{
		"id": item.ID, "status": item.Status, "content_type": item.ContentType,
		"error": item.Error, "expires_at": item.ExpiresAt,
	}
	if item.Status == "ready" {
		data["audio_url"] = fmt.Sprintf("/api/tater/tts/requests/%s/audio", item.ID)
	}
	return RespondSuccess(c, data)
}

func (s *Server) handleTaterPlayerTTSAudio(c *fiber.Ctx) error {
	cfg, token, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}
	player, ok := findTaterPlayerByToken(cfg, token)
	if !ok {
		return RespondUnauthorized(c, "Invalid player token", "")
	}
	item, err := s.queueRepo.GetTaterTTSRequest(c.Context(), strings.TrimSpace(c.Params("id")), player.ID)
	if err == sql.ErrNoRows {
		return RespondNotFound(c, "TTS request", c.Params("id"))
	}
	if err != nil {
		return RespondInternalError(c, "Failed to load TTS audio", err.Error())
	}
	if item.Status != "ready" || item.AudioBase64 == "" {
		return RespondConflict(c, "TTS audio is not ready", "")
	}
	audio, err := base64.StdEncoding.DecodeString(item.AudioBase64)
	if err != nil || len(audio) == 0 {
		return RespondInternalError(c, "Stored TTS audio is invalid", "")
	}
	contentType := strings.TrimSpace(item.ContentType)
	if contentType == "" {
		contentType = "audio/wav"
	}
	c.Set(fiber.HeaderContentType, contentType)
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Send(audio)
}

func (s *Server) handleTaterPlayerCancelTTSRequest(c *fiber.Ctx) error {
	cfg, token, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}
	player, ok := findTaterPlayerByToken(cfg, token)
	if !ok {
		return RespondUnauthorized(c, "Invalid player token", "")
	}
	id := strings.TrimSpace(c.Params("id"))
	if _, err := s.queueRepo.GetTaterTTSRequest(c.Context(), id, player.ID); err == sql.ErrNoRows {
		return RespondNotFound(c, "TTS request", id)
	} else if err != nil {
		return RespondInternalError(c, "Failed to load TTS request", err.Error())
	}
	_ = s.queueRepo.CancelTaterTTSRequest(c.Context(), id, player.ID, time.Now().UTC())
	return RespondSuccess(c, fiber.Map{"id": id, "status": "canceled"})
}

func (s *Server) handleTaterCoreClaimTTSRequests(c *fiber.Ctx) error {
	core, ok := s.taterCoreAuthorized(c)
	if !ok {
		return nil
	}
	limit := queryInt(c, "limit", 1, 1, 4)
	items, err := s.queueRepo.ClaimTaterTTSRequests(c.Context(), core.ID, limit, time.Now().UTC())
	if err != nil {
		return RespondInternalError(c, "Failed to claim TTS requests", err.Error())
	}
	return RespondSuccess(c, fiber.Map{"requests": items})
}

func (s *Server) handleTaterCoreCompleteTTSRequest(c *fiber.Ctx) error {
	core, ok := s.taterCoreAuthorized(c)
	if !ok {
		return nil
	}
	var req taterTTSCompleteRequest
	if err := c.BodyParser(&req); err != nil {
		return RespondValidationError(c, "Invalid TTS completion", err.Error())
	}
	errorMessage := cleanTaterText(req.Error)
	contentType := strings.TrimSpace(req.ContentType)
	if contentType == "" {
		contentType = "audio/wav"
	}
	audioBase64 := strings.TrimSpace(req.AudioBase64)
	if errorMessage == "" {
		audio, err := base64.StdEncoding.DecodeString(audioBase64)
		if err != nil || len(audio) == 0 {
			return RespondValidationError(c, "Valid TTS audio is required", "")
		}
		if len(audio) > taterTTSMaxAudioBytes {
			return RespondValidationError(c, "TTS audio is too large", "")
		}
		if len(audio) < 12 || string(audio[:4]) != "RIFF" || string(audio[8:12]) != "WAVE" {
			return RespondValidationError(c, "TTS audio must be WAV", "")
		}
	}
	err := s.queueRepo.CompleteTaterTTSRequest(
		c.Context(), strings.TrimSpace(c.Params("id")), core.ID,
		audioBase64, contentType, errorMessage, time.Now().UTC(),
	)
	if err == sql.ErrNoRows {
		return RespondSuccess(c, fiber.Map{
			"id": c.Params("id"), "discarded": true,
		})
	}
	if err != nil {
		return RespondInternalError(c, "Failed to complete TTS request", err.Error())
	}
	status := "ready"
	if errorMessage != "" {
		status = "failed"
	}
	return RespondSuccess(c, fiber.Map{"id": c.Params("id"), "status": status})
}

func (s *Server) handleTaterAdminState(c *fiber.Ctx) error {
	now := time.Now().UTC()
	connections, err := s.queueRepo.ListTaterCoreConnections(c.Context())
	if err != nil {
		return RespondInternalError(c, "Failed to load Tater Core connections", err.Error())
	}
	codes, err := s.queueRepo.ListTaterCorePairingCodes(c.Context(), now)
	if err != nil {
		return RespondInternalError(c, "Failed to load Tater Core pairing PINs", err.Error())
	}
	viewing, err := s.queueRepo.ListTaterViewingEvents(c.Context(), "", 100)
	if err != nil {
		return RespondInternalError(c, "Failed to load viewing history", err.Error())
	}
	batches, err := s.queueRepo.ListTaterRecommendationBatches(c.Context(), "", 20)
	if err != nil {
		return RespondInternalError(c, "Failed to load recommendation history", err.Error())
	}
	connectionRows := make([]fiber.Map, 0, len(connections))
	for _, item := range connections {
		row := fiber.Map{
			"id": item.ID, "name": item.Name, "assistant_name": item.AssistantName,
			"created_at": item.CreatedAt,
		}
		if item.LastSeenAt.Valid {
			row["last_seen_at"] = item.LastSeenAt.Time
		}
		if item.RevokedAt.Valid {
			row["revoked_at"] = item.RevokedAt.Time
		}
		connectionRows = append(connectionRows, row)
	}
	codeRows := make([]fiber.Map, 0, len(codes))
	for _, item := range codes {
		codeRows = append(codeRows, fiber.Map{
			"id": item.ID, "name": item.Name, "created_at": item.CreatedAt, "expires_at": item.ExpiresAt,
		})
	}
	activeBatch, activeItems, activeErr := s.queueRepo.GetActiveTaterRecommendations(c.Context(), taterDefaultProfileID, now)
	active := fiber.Map{"items": []any{}}
	if activeErr == nil {
		active["batch"] = activeBatch
		rows := make([]fiber.Map, 0, len(activeItems))
		for _, item := range activeItems {
			rows = append(rows, fiber.Map{
				"id": item.ID, "rank": item.Rank, "title": item.Title, "media_type": item.MediaType,
				"source": item.Source, "reason": item.Reason, "feedback": item.Feedback,
			})
		}
		active["items"] = rows
	}
	return RespondSuccess(c, fiber.Map{
		"connections": connectionRows, "pairing_codes": codeRows, "viewing_events": viewing,
		"recommendation_batches": batches, "active_recommendations": active,
	})
}

func (s *Server) handleTaterRevokeCore(c *fiber.Ctx) error {
	if err := s.queueRepo.RevokeTaterCore(c.Context(), strings.TrimSpace(c.Params("id")), time.Now().UTC()); err != nil {
		return RespondNotFound(c, "Tater Core connection", c.Params("id"))
	}
	return RespondSuccess(c, fiber.Map{"message": "Tater Core connection revoked"})
}

func (s *Server) handleTaterClearViewingHistory(c *fiber.Ctx) error {
	profileID := strings.TrimSpace(c.Query("profile_id"))
	if profileID != "" {
		profileID = cleanTaterProfileID(profileID)
	}
	if err := s.queueRepo.ClearTaterViewingEvents(c.Context(), profileID); err != nil {
		return RespondInternalError(c, "Failed to clear viewing history", err.Error())
	}
	return RespondSuccess(c, fiber.Map{"message": "Viewing history cleared"})
}

func (s *Server) taterRecommendationCandidates(
	ctx context.Context,
	cfg *config.Config,
	baseURL string,
	profileID string,
) []taterCandidate {
	items, err := taterLocalDiscoverLibraryItems(cfg, baseURL, "")
	if err != nil {
		items = []taterUsenetItem{}
	}
	historyCandidates := s.taterHistoryRecommendationCandidates(ctx, profileID)
	result := make([]taterCandidate, 0, len(items)+len(historyCandidates))
	seen := map[string]bool{}
	for _, candidate := range historyCandidates {
		if seen[candidate.ID] {
			continue
		}
		seen[candidate.ID] = true
		result = append(result, candidate)
	}
	for _, item := range items {
		if item.Type != "localFile" && item.Type != "localFolder" {
			continue
		}
		id := taterCandidateID(item)
		if seen[id] {
			continue
		}
		seen[id] = true
		mediaType := strings.TrimSpace(item.MediaType)
		if mediaType == "show" {
			mediaType = "series"
		}
		if mediaType == "" {
			mediaType = "video"
		}
		result = append(result, taterCandidate{
			ID: id, Title: item.Title, MediaType: mediaType, Source: "local_media",
			Year: item.Date, Description: item.Description, Launch: item,
		})
	}
	return result
}

func (s *Server) taterHistoryRecommendationCandidates(
	ctx context.Context,
	profileID string,
) []taterCandidate {
	if s.queueRepo == nil {
		return []taterCandidate{}
	}
	events, err := s.queueRepo.ListTaterViewingEvents(ctx, profileID, 200)
	if err != nil {
		return []taterCandidate{}
	}
	return taterOTACandidates(events)
}

func taterOTACandidates(events []database.TaterViewingEvent) []taterCandidate {
	type channelHistory struct {
		title         string
		number        string
		name          string
		mediaID       string
		watchCount    int
		lastWatchedAt time.Time
	}
	byChannel := map[string]*channelHistory{}
	for _, event := range events {
		if event.Source != "over_the_air" || event.MediaType != "live" {
			continue
		}
		metadata := map[string]any{}
		if err := json.Unmarshal([]byte(event.MetadataJSON), &metadata); err != nil {
			continue
		}
		moduleID, _ := metadata["module_id"].(string)
		number, _ := metadata["channel_number"].(string)
		name, _ := metadata["channel_name"].(string)
		moduleID = strings.TrimSpace(moduleID)
		number = cleanTaterText(number)
		name = cleanTaterText(name)
		if moduleID != "com.240mp.ota" || (number == "" && name == "") {
			continue
		}
		key := strings.ToLower(number + "\x00" + name)
		row := byChannel[key]
		if row == nil {
			row = &channelHistory{
				title:   cleanTaterText(event.Title),
				number:  number,
				name:    name,
				mediaID: strings.TrimSpace(event.MediaID),
			}
			byChannel[key] = row
		}
		row.watchCount++
		if event.OccurredAt.After(row.lastWatchedAt) {
			row.lastWatchedAt = event.OccurredAt
			if title := cleanTaterText(event.Title); title != "" {
				row.title = title
			}
		}
	}

	rows := make([]*channelHistory, 0, len(byChannel))
	for _, row := range byChannel {
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].watchCount != rows[j].watchCount {
			return rows[i].watchCount > rows[j].watchCount
		}
		return rows[i].lastWatchedAt.After(rows[j].lastWatchedAt)
	})

	result := make([]taterCandidate, 0, len(rows))
	for _, row := range rows {
		title := row.title
		if title == "" {
			title = strings.TrimSpace(strings.Join([]string{row.number, row.name}, " "))
		}
		identity := row.mediaID
		if identity == "" {
			identity = row.number + "\x00" + row.name
		}
		sum := sha256.Sum256([]byte("com.240mp.ota\x00" + identity))
		watchLabel := "session"
		if row.watchCount != 1 {
			watchLabel = "sessions"
		}
		result = append(result, taterCandidate{
			ID:          "ota-" + hex.EncodeToString(sum[:12]),
			Title:       title,
			MediaType:   "live",
			Source:      "over_the_air",
			Description: fmt.Sprintf("Live channel watched in %d recent %s.", row.watchCount, watchLabel),
			Launch: taterUsenetItem{
				Title:         title,
				Type:          "module",
				MediaType:     "live",
				ModuleID:      "com.240mp.ota",
				ChannelNumber: row.number,
				ChannelName:   row.name,
			},
		})
	}
	return result
}

func taterGreetingForHour(hour int) string {
	switch {
	case hour >= 5 && hour < 12:
		return "Good morning"
	case hour >= 12 && hour < 17:
		return "Good afternoon"
	default:
		return "Good evening"
	}
}

func cleanTaterProfileID(value string) string {
	return cleanTaterSlug(value, taterDefaultProfileID)
}

func cleanTaterAssistantFirstName(value string) string {
	fields := strings.Fields(cleanTaterText(value))
	if len(fields) == 0 {
		return ""
	}
	runes := []rune(fields[0])
	if len(runes) > 48 {
		runes = runes[:48]
	}
	return string(runes)
}

func taterAssistantNameFromHeader(value string) string {
	raw := strings.TrimSpace(value)
	if decoded, err := url.QueryUnescape(raw); err == nil {
		raw = decoded
	}
	return cleanTaterAssistantFirstName(raw)
}

func cleanTaterSlug(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			out.WriteRune(r)
		}
	}
	if out.Len() == 0 {
		return fallback
	}
	if out.Len() > 64 {
		return out.String()[:64]
	}
	return out.String()
}

func queryInt(c *fiber.Ctx, key string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(c.Query(key))
	if err != nil {
		return fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func taterCandidateID(item taterUsenetItem) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		item.Type, item.MediaType, item.CategoryID, strconv.Itoa(item.SourceIndex), item.Path, item.Title,
	}, "\x00")))
	return hex.EncodeToString(sum[:12])
}
