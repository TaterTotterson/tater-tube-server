package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
)

type taterTMDBTestRequest struct {
	APIKey string `json:"api_key"`
}

func testTaterTMDBConnection(ctx context.Context, cfg *config.Config, apiKey string) error {
	if cfg == nil {
		return fmt.Errorf("server configuration is unavailable")
	}
	key := strings.TrimSpace(apiKey)
	if key == "" {
		key = strings.TrimSpace(cfg.LocalMedia.TMDBAPIKey)
	}
	if key == "" {
		return fmt.Errorf("enter a TMDB API key or Read Access Token first")
	}
	if len(key) > 4096 {
		return fmt.Errorf("the TMDB credential is too long")
	}
	probeConfig := *cfg
	probeConfig.LocalMedia = cfg.LocalMedia
	probeConfig.LocalMedia.TMDBAPIKey = key
	response, err := taterTMDBRequest(ctx, &probeConfig, "configuration", nil)
	if err != nil {
		return fmt.Errorf("could not reach TMDB")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, taterTMDBResponseMaximumBytes))
	switch response.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("TMDB rejected this key or token")
	default:
		return fmt.Errorf("TMDB returned HTTP %d", response.StatusCode)
	}
}

func (s *Server) handleLocalMediaTMDBTest(c *fiber.Ctx) error {
	if s.configManager == nil || s.configManager.GetConfig() == nil {
		return RespondServiceUnavailable(c, "Configuration management not available", "CONFIG_UNAVAILABLE")
	}
	request := taterTMDBTestRequest{}
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&request); err != nil {
			return RespondValidationError(c, "Invalid TMDB test request", "")
		}
	}
	if err := testTaterTMDBConnection(c.Context(), s.configManager.GetConfig(), request.APIKey); err != nil {
		return RespondValidationError(c, err.Error(), "")
	}
	return RespondSuccess(c, fiber.Map{
		"valid":   true,
		"message": "TMDB connection successful",
	})
}
