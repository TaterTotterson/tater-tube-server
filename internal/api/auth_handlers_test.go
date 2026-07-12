package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"
)

type registrationStatusResponse struct {
	RegistrationEnabled bool `json:"registration_enabled"`
	SetupRequired       bool `json:"setup_required"`
	PasswordConfigured  bool `json:"password_configured"`
	UserCount           int  `json:"user_count"`
}

func TestHandleCheckRegistrationUsesPasswordOnlySetupState(t *testing.T) {
	tests := []struct {
		name      string
		hash      string
		wantSetup bool
		wantCount int
	}{
		{name: "no password configured", wantSetup: true, wantCount: 0},
		{name: "password configured", hash: "$2a$10$existing-password-hash", wantSetup: false, wantCount: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := fiber.New()
			server := &Server{
				configManager: &mockConfigManager{
					cfg: &config.Config{
						Auth: config.AuthConfig{PasswordHash: tt.hash},
					},
				},
			}
			app.Get("/registration-status", server.handleCheckRegistration)

			req := httptest.NewRequest(http.MethodGet, "/registration-status", nil)
			resp, err := app.Test(req)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var envelope testAPIResponse[registrationStatusResponse]
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&envelope))
			require.True(t, envelope.Success)
			require.Equal(t, tt.wantSetup, envelope.Data.RegistrationEnabled)
			require.Equal(t, tt.wantSetup, envelope.Data.SetupRequired)
			require.Equal(t, !tt.wantSetup, envelope.Data.PasswordConfigured)
			require.Equal(t, tt.wantCount, envelope.Data.UserCount)
		})
	}
}
