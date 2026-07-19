package api

import (
	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
)

func taterBumperSettingEnabled(value *bool) bool {
	return value == nil || *value
}

func taterBumperSettingsMap(cfg *config.Config) fiber.Map {
	if cfg == nil {
		return fiber.Map{
			"live_tv":      true,
			"local_movies": true,
			"local_series": true,
			"nzb_movies":   true,
		}
	}
	return fiber.Map{
		"live_tv":      taterBumperSettingEnabled(cfg.TaterBumpers.LiveTV),
		"local_movies": taterBumperSettingEnabled(cfg.TaterBumpers.LocalMovies),
		"local_series": taterBumperSettingEnabled(cfg.TaterBumpers.LocalSeries),
		"nzb_movies":   taterBumperSettingEnabled(cfg.TaterBumpers.NZBMovies),
	}
}

func (s *Server) handleTaterBumperSettings(c *fiber.Ctx) error {
	cfg, _, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}
	return RespondSuccess(c, taterBumperSettingsMap(cfg))
}
