package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TaterTotterson/tater-tube-server/internal/auth"
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

type authConfigResponse struct {
	LoginRequired      bool `json:"login_required"`
	PasswordConfigured bool `json:"password_configured"`
}

func TestHandleCheckRegistrationUsesPasswordOnlySetupState(t *testing.T) {
	tests := []struct {
		name             string
		hash             string
		wantRegistration bool
		wantConfigured   bool
		wantCount        int
	}{
		{name: "no password configured", wantRegistration: true, wantConfigured: false, wantCount: 0},
		{name: "password configured", hash: "$2a$10$existing-password-hash", wantRegistration: false, wantConfigured: true, wantCount: 1},
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
			require.Equal(t, tt.wantRegistration, envelope.Data.RegistrationEnabled)
			require.False(t, envelope.Data.SetupRequired)
			require.Equal(t, tt.wantConfigured, envelope.Data.PasswordConfigured)
			require.Equal(t, tt.wantCount, envelope.Data.UserCount)
		})
	}
}

func TestHandleGetAuthConfigRequiresLoginOnlyWhenPasswordExists(t *testing.T) {
	tests := []struct {
		name              string
		hash              string
		wantLoginRequired bool
	}{
		{name: "no password opens the web ui", wantLoginRequired: false},
		{name: "password enables login", hash: "$2a$10$existing-password-hash", wantLoginRequired: true},
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
			app.Get("/config", server.handleGetAuthConfig)

			req := httptest.NewRequest(http.MethodGet, "/config", nil)
			resp, err := app.Test(req)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var envelope testAPIResponse[authConfigResponse]
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&envelope))
			require.True(t, envelope.Success)
			require.Equal(t, tt.wantLoginRequired, envelope.Data.LoginRequired)
			require.Equal(t, tt.wantLoginRequired, envelope.Data.PasswordConfigured)
		})
	}
}

func TestRequireAuthWhenEnabledUsesPasswordHashNotLegacyFlag(t *testing.T) {
	legacyLoginRequired := true
	app := fiber.New()
	server := &Server{
		configManager: &mockConfigManager{
			cfg: &config.Config{
				Auth: config.AuthConfig{
					LoginRequired: &legacyLoginRequired,
					PasswordHash:  "",
				},
			},
		},
	}
	app.Use(server.requireAuthWhenEnabled(nil))
	app.Get("/protected", func(c *fiber.Ctx) error {
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestHandleClearServerPasswordRemovesHashAndDisablesLogin(t *testing.T) {
	legacyLoginRequired := true
	manager := &mockConfigManager{
		cfg: &config.Config{
			Auth: config.AuthConfig{
				LoginRequired: &legacyLoginRequired,
				PasswordHash:  "$2a$10$existing-password-hash",
			},
		},
	}
	app := fiber.New()
	server := &Server{configManager: manager}
	app.Delete("/auth/password", func(c *fiber.Ctx) error {
		c.Locals(string(auth.UserContextKey), passwordAdminUser())
		return server.handleClearServerPassword(c)
	})
	app.Get("/protected", server.requireAuthWhenEnabled(nil), func(c *fiber.Ctx) error {
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodDelete, "/auth/password", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, manager.cfg.Auth.PasswordHash)
	require.NotEmpty(t, resp.Header.Values("Set-Cookie"))

	req = httptest.NewRequest(http.MethodGet, "/protected", nil)
	resp, err = app.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}
