package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
)

type taterPairCoreRequest struct {
	PIN  string `json:"pin"`
	Name string `json:"name"`
}

type taterCorePairResponse struct {
	CoreID   string `json:"core_id"`
	CoreName string `json:"core_name"`
	Token    string `json:"token"`
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
	ok, err := s.queueRepo.PairTaterCore(c.Context(), hashTaterSecret(pin), now, database.TaterCoreConnection{
		ID:         id,
		Name:       name,
		TokenHash:  hashTaterSecret(token),
		CreatedAt:  now,
		LastSeenAt: sql.NullTime{Time: now, Valid: true},
	})
	if err != nil {
		return RespondInternalError(c, "Failed to pair Tater Core", err.Error())
	}
	if !ok {
		return RespondUnauthorized(c, "Invalid or expired pairing PIN", "")
	}
	return RespondSuccess(c, taterCorePairResponse{CoreID: id, CoreName: name, Token: token})
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
	_ = s.queueRepo.TouchTaterCore(c.Context(), core.ID, time.Now().UTC())
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
	candidates := s.taterRecommendationCandidates(cfg, resolveBaseURL(c, ""))
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
	if len(req.Items) == 0 {
		return RespondValidationError(c, "At least one recommendation is required", "")
	}
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondServiceUnavailable(c, "Configuration not available", "")
	}
	available := s.taterRecommendationCandidates(cfg, resolveBaseURL(c, ""))
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
		ID: batchID, ProfileID: cleanTaterProfileID(req.ProfileID), CoreID: core.ID,
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
	return RespondSuccess(c, fiber.Map{"batch_id": batch.ID, "count": len(items), "expires_at": batch.ExpiresAt})
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
		row := fiber.Map{"id": item.ID, "name": item.Name, "created_at": item.CreatedAt}
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

func (s *Server) taterRecommendationCandidates(cfg *config.Config, baseURL string) []taterCandidate {
	items, err := taterLocalDiscoverLibraryItems(cfg, baseURL, "")
	if err != nil {
		return []taterCandidate{}
	}
	result := make([]taterCandidate, 0, len(items))
	seen := map[string]bool{}
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

func cleanTaterProfileID(value string) string {
	return cleanTaterSlug(value, taterDefaultProfileID)
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
