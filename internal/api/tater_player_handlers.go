package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
)

const (
	taterPairingCodeTTL = 10 * time.Minute
)

type taterPlayerResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  string `json:"created_at"`
	LastSeenAt string `json:"last_seen_at,omitempty"`
	RevokedAt  string `json:"revoked_at,omitempty"`
}

type taterPairingCodeResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Code      string `json:"code,omitempty"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
}

type taterPlayersResponse struct {
	Players      []taterPlayerResponse      `json:"players"`
	PairingCodes []taterPairingCodeResponse `json:"pairing_codes"`
}

type taterCreatePairingCodeRequest struct {
	Name string `json:"name"`
}

type taterPairPlayerRequest struct {
	PIN  string `json:"pin"`
	Name string `json:"name"`
}

type taterUpdatePlayerRequest struct {
	Name string `json:"name"`
}

type taterPairPlayerResponse struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`
	Token      string `json:"token"`
}

func (s *Server) handleTaterPlayers(c *fiber.Ctx) error {
	if s.configManager == nil {
		return RespondServiceUnavailable(c, "Configuration not available", "")
	}
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondServiceUnavailable(c, "Configuration not available", "")
	}
	return RespondSuccess(c, taterPlayersConfigResponse(cfg.Players, time.Now().UTC()))
}

func (s *Server) handleTaterCreatePairingCode(c *fiber.Ctx) error {
	if s.configManager == nil {
		return RespondServiceUnavailable(c, "Configuration not available", "")
	}

	current := s.configManager.GetConfig()
	if current == nil {
		return RespondServiceUnavailable(c, "Configuration not available", "")
	}
	newCfg := current.DeepCopy()

	var req taterCreatePairingCodeRequest
	_ = c.BodyParser(&req)

	now := time.Now().UTC()
	code, err := randomDigits(6)
	if err != nil {
		return RespondInternalError(c, "Failed to create pairing code", err.Error())
	}
	id, err := randomHex(8)
	if err != nil {
		return RespondInternalError(c, "Failed to create pairing code", err.Error())
	}
	name := cleanTaterText(req.Name)
	if name == "" {
		name = "Tater Tube Player"
	}

	newCfg.Players.PairingCodes = pruneExpiredPairingCodes(newCfg.Players.PairingCodes, now)
	newCfg.Players.PairingCodes = append(newCfg.Players.PairingCodes, config.PlayerPairingCode{
		ID:        id,
		Name:      name,
		CodeHash:  hashTaterSecret(code),
		CreatedAt: now.Format(time.RFC3339),
		ExpiresAt: now.Add(taterPairingCodeTTL).Format(time.RFC3339),
	})

	if err := s.saveUpdatedConfig(newCfg); err != nil {
		return RespondInternalError(c, "Failed to save pairing code", err.Error())
	}

	return RespondSuccess(c, taterPairingCodeResponse{
		ID:        id,
		Name:      name,
		Code:      code,
		CreatedAt: now.Format(time.RFC3339),
		ExpiresAt: now.Add(taterPairingCodeTTL).Format(time.RFC3339),
	})
}

func (s *Server) handleTaterPairPlayer(c *fiber.Ctx) error {
	if s.configManager == nil {
		return RespondServiceUnavailable(c, "Configuration not available", "")
	}

	var req taterPairPlayerRequest
	if err := c.BodyParser(&req); err != nil {
		return RespondValidationError(c, "Invalid pairing request", err.Error())
	}
	pin := normalizePairingPIN(req.PIN)
	if len(pin) != 6 {
		return RespondValidationError(c, "Pairing PIN must be 6 digits", "")
	}

	current := s.configManager.GetConfig()
	if current == nil {
		return RespondServiceUnavailable(c, "Configuration not available", "")
	}
	newCfg := current.DeepCopy()
	now := time.Now().UTC()
	codeHash := hashTaterSecret(pin)

	pairingCodes := pruneExpiredPairingCodes(newCfg.Players.PairingCodes, now)
	matchIndex := -1
	pairingCodeName := ""
	for i, code := range pairingCodes {
		if subtle.ConstantTimeCompare([]byte(code.CodeHash), []byte(codeHash)) == 1 {
			matchIndex = i
			pairingCodeName = code.Name
			break
		}
	}
	if matchIndex < 0 {
		newCfg.Players.PairingCodes = pairingCodes
		_ = s.saveUpdatedConfig(newCfg)
		return RespondUnauthorized(c, "Invalid or expired pairing PIN", "")
	}

	token, err := randomHex(32)
	if err != nil {
		return RespondInternalError(c, "Failed to create player token", err.Error())
	}
	playerID, err := randomHex(8)
	if err != nil {
		return RespondInternalError(c, "Failed to create player", err.Error())
	}
	name := cleanTaterText(req.Name)
	if name == "" {
		name = cleanTaterText(pairingCodeName)
	}
	if name == "" {
		name = "Tater Tube Player"
	}

	pairingCodes = append(pairingCodes[:matchIndex], pairingCodes[matchIndex+1:]...)
	newCfg.Players.PairingCodes = pairingCodes
	newCfg.Players.Paired = append(newCfg.Players.Paired, config.PlayerConfig{
		ID:         playerID,
		Name:       name,
		TokenHash:  hashTaterSecret(token),
		CreatedAt:  now.Format(time.RFC3339),
		LastSeenAt: now.Format(time.RFC3339),
	})

	if err := s.saveUpdatedConfig(newCfg); err != nil {
		return RespondInternalError(c, "Failed to save paired player", err.Error())
	}

	return RespondSuccess(c, taterPairPlayerResponse{
		PlayerID:   playerID,
		PlayerName: name,
		Token:      token,
	})
}

func (s *Server) handleTaterUpdatePlayer(c *fiber.Ctx) error {
	if s.configManager == nil {
		return RespondServiceUnavailable(c, "Configuration not available", "")
	}

	playerID := strings.TrimSpace(c.Params("id"))
	if playerID == "" {
		return RespondValidationError(c, "Player ID is required", "")
	}

	var req taterUpdatePlayerRequest
	if err := c.BodyParser(&req); err != nil {
		return RespondValidationError(c, "Invalid player update", err.Error())
	}
	name := cleanTaterText(req.Name)
	if name == "" {
		return RespondValidationError(c, "Player name is required", "")
	}

	current := s.configManager.GetConfig()
	if current == nil {
		return RespondServiceUnavailable(c, "Configuration not available", "")
	}
	newCfg := current.DeepCopy()

	var updated config.PlayerConfig
	found := false
	for i := range newCfg.Players.Paired {
		if newCfg.Players.Paired[i].ID == playerID {
			newCfg.Players.Paired[i].Name = name
			updated = newCfg.Players.Paired[i]
			found = true
			break
		}
	}
	if !found {
		return RespondNotFound(c, "Player", playerID)
	}

	if err := s.saveUpdatedConfig(newCfg); err != nil {
		return RespondInternalError(c, "Failed to rename player", err.Error())
	}
	return RespondSuccess(c, taterPlayerResponse{
		ID:         updated.ID,
		Name:       updated.Name,
		CreatedAt:  updated.CreatedAt,
		LastSeenAt: updated.LastSeenAt,
		RevokedAt:  updated.RevokedAt,
	})
}

func (s *Server) handleTaterRevokePlayer(c *fiber.Ctx) error {
	if s.configManager == nil {
		return RespondServiceUnavailable(c, "Configuration not available", "")
	}

	playerID := strings.TrimSpace(c.Params("id"))
	if playerID == "" {
		return RespondValidationError(c, "Player ID is required", "")
	}

	current := s.configManager.GetConfig()
	if current == nil {
		return RespondServiceUnavailable(c, "Configuration not available", "")
	}
	newCfg := current.DeepCopy()
	now := time.Now().UTC().Format(time.RFC3339)

	found := false
	for i := range newCfg.Players.Paired {
		if newCfg.Players.Paired[i].ID == playerID {
			newCfg.Players.Paired[i].RevokedAt = now
			found = true
			break
		}
	}
	if !found {
		return RespondNotFound(c, "Player", playerID)
	}

	if err := s.saveUpdatedConfig(newCfg); err != nil {
		return RespondInternalError(c, "Failed to revoke player", err.Error())
	}
	return RespondSuccess(c, fiber.Map{"message": "Player revoked"})
}

func (s *Server) handleTaterActiveStreams(c *fiber.Ctx) error {
	cfg, token, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}
	if s.streamTracker == nil {
		return RespondSuccess(c, []any{})
	}

	player, ok := findTaterPlayerByToken(cfg, token)
	if !ok {
		return RespondUnauthorized(c, "Invalid player token", "")
	}
	playerName := taterPlayerDisplayName(player)
	streams := s.streamTracker.GetAll()
	filtered := make([]any, 0, len(streams))
	for _, stream := range streams {
		if strings.EqualFold(strings.TrimSpace(stream.UserName), playerName) {
			filtered = append(filtered, stream)
		}
	}

	return RespondSuccess(c, filtered)
}

func (s *Server) taterAuthorizedConfig(c *fiber.Ctx) (*config.Config, string, bool) {
	if s.configManager == nil {
		RespondServiceUnavailable(c, "Configuration not available", "")
		return nil, "", false
	}
	token := bearerToken(c.Get("Authorization"))
	if token == "" {
		token = strings.TrimSpace(c.Get("X-Tater-Player-Token"))
	}
	if token == "" {
		RespondUnauthorized(c, "Authentication required", "Pair this Tater Tube player with the server")
		return nil, "", false
	}

	cfg := s.configManager.GetConfig()
	if cfg == nil {
		RespondServiceUnavailable(c, "Configuration not available", "")
		return nil, "", false
	}

	player, ok := findTaterPlayerByToken(cfg, token)
	if !ok {
		slog.WarnContext(c.Context(), "Tater Tube player auth failed", "remote_addr", c.IP())
		RespondUnauthorized(c, "Invalid player token", "")
		return nil, "", false
	}
	s.touchTaterPlayer(player.ID)
	return cfg, token, true
}

func findTaterPlayerByToken(cfg *config.Config, token string) (*config.PlayerConfig, bool) {
	if cfg == nil || strings.TrimSpace(token) == "" {
		return nil, false
	}
	tokenHash := hashTaterSecret(token)
	for i := range cfg.Players.Paired {
		player := &cfg.Players.Paired[i]
		if player.RevokedAt != "" || player.TokenHash == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(player.TokenHash), []byte(tokenHash)) == 1 {
			return player, true
		}
	}
	return nil, false
}

func taterPlayerDisplayName(player *config.PlayerConfig) string {
	if player == nil {
		return "Tater Tube Player"
	}
	name := strings.TrimSpace(player.Name)
	if name == "" {
		return "Tater Tube Player"
	}
	return name
}

func (s *Server) touchTaterPlayer(playerID string) {
	current := s.configManager.GetConfig()
	if current == nil {
		return
	}
	now := time.Now().UTC()
	newCfg := current.DeepCopy()
	for i := range newCfg.Players.Paired {
		player := &newCfg.Players.Paired[i]
		if player.ID != playerID || player.RevokedAt != "" {
			continue
		}
		if last, err := time.Parse(time.RFC3339, player.LastSeenAt); err == nil && now.Sub(last) < time.Minute {
			return
		}
		player.LastSeenAt = now.Format(time.RFC3339)
		if err := s.saveUpdatedConfig(newCfg); err != nil {
			slog.Warn("Failed to update Tater Tube player last seen", "error", err)
		}
		return
	}
}

func (s *Server) saveUpdatedConfig(cfg *config.Config) error {
	if err := s.configManager.ValidateConfigUpdate(cfg); err != nil {
		return err
	}
	if err := s.configManager.UpdateConfig(cfg); err != nil {
		return err
	}
	return s.configManager.SaveConfig()
}

func taterPlayersConfigResponse(players config.PlayersConfig, now time.Time) taterPlayersResponse {
	resp := taterPlayersResponse{
		Players:      make([]taterPlayerResponse, 0, len(players.Paired)),
		PairingCodes: make([]taterPairingCodeResponse, 0, len(players.PairingCodes)),
	}
	for _, player := range players.Paired {
		resp.Players = append(resp.Players, taterPlayerResponse{
			ID:         player.ID,
			Name:       player.Name,
			CreatedAt:  player.CreatedAt,
			LastSeenAt: player.LastSeenAt,
			RevokedAt:  player.RevokedAt,
		})
	}
	for _, code := range pruneExpiredPairingCodes(players.PairingCodes, now) {
		resp.PairingCodes = append(resp.PairingCodes, taterPairingCodeResponse{
			ID:        code.ID,
			Name:      code.Name,
			CreatedAt: code.CreatedAt,
			ExpiresAt: code.ExpiresAt,
		})
	}
	return resp
}

func pruneExpiredPairingCodes(codes []config.PlayerPairingCode, now time.Time) []config.PlayerPairingCode {
	filtered := make([]config.PlayerPairingCode, 0, len(codes))
	for _, code := range codes {
		expires, err := time.Parse(time.RFC3339, code.ExpiresAt)
		if err != nil || now.After(expires) {
			continue
		}
		filtered = append(filtered, code)
	}
	return filtered
}

func bearerToken(value string) string {
	const prefix = "bearer "
	value = strings.TrimSpace(value)
	if len(value) < len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(value[len(prefix):])
}

func normalizePairingPIN(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hashTaterSecret(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func randomHex(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func randomDigits(count int) (string, error) {
	var b strings.Builder
	for i := 0; i < count; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(&b, "%d", n.Int64())
	}
	return b.String(), nil
}
