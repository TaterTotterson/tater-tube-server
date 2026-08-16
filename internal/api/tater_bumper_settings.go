package api

import (
	"math/rand"
	"net/url"
	"strings"
	"time"

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

func (s *Server) handleTaterBumperNext(c *fiber.Ctx) error {
	cfg, playerToken, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}

	source, ok := normalizeTaterBumperSource(c.Query("source"))
	if !ok {
		return RespondValidationError(c, "Unknown bumper source", "source must be one of live_tv, local_movies, local_series, nzb_movies")
	}
	if !taterBumperSourceEnabled(cfg, source) {
		return RespondSuccess(c, fiber.Map{
			"enabled": false,
			"source":  source,
		})
	}
	if len(taterTVBrandBumperDefinitions) == 0 {
		return RespondSuccess(c, fiber.Map{
			"enabled": false,
			"source":  source,
		})
	}

	seed := time.Now().UnixNano()
	definition := taterTVBrandBumperDefinitions[rand.New(rand.NewSource(seed)).Intn(len(taterTVBrandBumperDefinitions))]
	return RespondSuccess(c, fiber.Map{
		"enabled": true,
		"source":  source,
		"item": fiber.Map{
			"type":            taterTVBrandBumperKind,
			"mediaType":       "bumper",
			"name":            definition.Name,
			"title":           definition.Title,
			"duration":        definition.Duration,
			"durationSeconds": definition.Duration,
			"streamUrl":       taterBumperFileURL(resolveBaseURL(c, ""), definition.Name, playerToken),
		},
	})
}

func normalizeTaterBumperSource(source string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "", "local", "local_movie", "local_movies":
		return "local_movies", true
	case "local_tv", "local_show", "local_series":
		return "local_series", true
	case "nzb", "stream", "nzb_movie", "nzb_movies":
		return "nzb_movies", true
	case "tv", "tube_tv", "live_tv":
		return "live_tv", true
	default:
		return "", false
	}
}

func taterBumperSourceEnabled(cfg *config.Config, source string) bool {
	if cfg == nil {
		return true
	}
	switch source {
	case "live_tv":
		return taterBumperSettingEnabled(cfg.TaterBumpers.LiveTV)
	case "local_series":
		return taterBumperSettingEnabled(cfg.TaterBumpers.LocalSeries)
	case "nzb_movies":
		return taterBumperSettingEnabled(cfg.TaterBumpers.NZBMovies)
	default:
		return taterBumperSettingEnabled(cfg.TaterBumpers.LocalMovies)
	}
}

func taterBumperFileURL(baseURL, name, playerToken string) string {
	if strings.TrimSpace(baseURL) == "" {
		return ""
	}
	safeName := taterTVSafeFileName(name)
	if safeName == "" || !taterTVIsBuiltInBumperName(safeName) {
		return ""
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/api/tater/bumpers/file/" + url.PathEscape(safeName))
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("player_token", playerToken)
	u.RawQuery = q.Encode()
	return u.String()
}

func taterBumperDefinitionByName(name string) (taterTVBrandBumperDefinition, bool) {
	safeName := taterTVSafeFileName(name)
	for _, definition := range taterTVBrandBumperDefinitions {
		if definition.Name == safeName {
			return definition, true
		}
	}
	return taterTVBrandBumperDefinition{}, false
}
