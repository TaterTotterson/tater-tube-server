package api

import (
	"github.com/TaterTotterson/tater-tube-server/internal/version"
	"github.com/gofiber/fiber/v2"
)

func (s *Server) handleTaterServerInfo(c *fiber.Ctx) error {
	providerCount := 0
	if s.configManager != nil {
		if cfg := s.configManager.GetConfig(); cfg != nil {
			providerCount = len(cfg.Providers)
		}
	}

	return RespondSuccess(c, map[string]any{
		"name":        "Tater Tube Server",
		"version":     version.Version,
		"git_commit":  version.GitCommit,
		"ready":       s.IsReady(),
		"started_at":  s.startTime,
		"providers":   providerCount,
		"modules":     []string{"usenet_streaming"},
		"stream_path": "/api/files/stream",
		"endpoints": map[string]string{
			"server":               "/api/tater/server",
			"players":              "/api/tater/players",
			"player_pair":          "/api/tater/players/pair",
			"player_pairing_codes": "/api/tater/players/codes",
			"usenet_status":        "/api/tater/usenet/status",
			"usenet_catalog":       "/api/tater/usenet/catalog",
			"usenet_items":         "/api/tater/usenet/items",
			"usenet_search":        "/api/tater/usenet/search",
			"usenet_discover":      "/api/tater/usenet/discover",
			"usenet_trending":      "/api/tater/usenet/trending",
			"usenet_play":          "/api/tater/usenet/play",
			"local_stream":         "/api/tater/local/stream",
		},
	})
}
