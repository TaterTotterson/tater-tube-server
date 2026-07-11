package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"
)

type testAPIResponse[T any] struct {
	Success bool `json:"success"`
	Data    T    `json:"data"`
}

func TestTaterPairPlayerUsesPairingCodeName(t *testing.T) {
	app := fiber.New()
	manager := &mockConfigManager{cfg: &config.Config{}}
	server := &Server{configManager: manager}
	app.Post("/codes", server.handleTaterCreatePairingCode)
	app.Post("/pair", server.handleTaterPairPlayer)

	codeBody, err := json.Marshal(map[string]string{"name": "Living Room"})
	require.NoError(t, err)
	codeReq := httptest.NewRequest(http.MethodPost, "/codes", bytes.NewReader(codeBody))
	codeReq.Header.Set("Content-Type", "application/json")
	codeResp, err := app.Test(codeReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, codeResp.StatusCode)

	var codeEnvelope testAPIResponse[taterPairingCodeResponse]
	require.NoError(t, json.NewDecoder(codeResp.Body).Decode(&codeEnvelope))
	require.True(t, codeEnvelope.Success)
	require.NotEmpty(t, codeEnvelope.Data.Code)
	require.Equal(t, "Living Room", codeEnvelope.Data.Name)

	pairBody, err := json.Marshal(map[string]string{"pin": codeEnvelope.Data.Code})
	require.NoError(t, err)
	pairReq := httptest.NewRequest(http.MethodPost, "/pair", bytes.NewReader(pairBody))
	pairReq.Header.Set("Content-Type", "application/json")
	pairResp, err := app.Test(pairReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, pairResp.StatusCode)

	require.Len(t, manager.cfg.Players.Paired, 1)
	require.Equal(t, "Living Room", manager.cfg.Players.Paired[0].Name)
	require.Empty(t, manager.cfg.Players.PairingCodes)
}

func TestTaterUpdatePlayerRenamesPairedPlayer(t *testing.T) {
	app := fiber.New()
	manager := &mockConfigManager{cfg: &config.Config{
		Players: config.PlayersConfig{
			Paired: []config.PlayerConfig{
				{
					ID:         "player-1",
					Name:       "Old Name",
					TokenHash:  "hash",
					CreatedAt:  time.Now().UTC().Format(time.RFC3339),
					LastSeenAt: time.Now().UTC().Format(time.RFC3339),
				},
			},
		},
	}}
	server := &Server{configManager: manager}
	app.Patch("/players/:id", server.handleTaterUpdatePlayer)

	body, err := json.Marshal(map[string]string{"name": "Bedroom CRT"})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPatch, "/players/player-1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var envelope testAPIResponse[taterPlayerResponse]
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&envelope))
	require.True(t, envelope.Success)
	require.Equal(t, "Bedroom CRT", envelope.Data.Name)
	require.Equal(t, "Bedroom CRT", manager.cfg.Players.Paired[0].Name)
}
