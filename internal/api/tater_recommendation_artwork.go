package api

import (
	"net/url"
	"path/filepath"
	"strings"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

// Recommendation batches outlive artwork scans and player sessions. Resolve
// local artwork when serving the batch so old picks see new sidecars, corrected
// matches, and URLs authenticated for the player requesting them.
func refreshTaterRecommendationLaunch(cfg *config.Config, baseURL, playerToken string, launch taterUsenetItem) taterUsenetItem {
	if launch.Type != "localFile" && launch.Type != "localFolder" {
		return launch
	}
	categoryID := taterRawLocalCategoryID(launch.CategoryID)
	if categoryID == "" {
		return launch
	}
	if launch.Type == "localFile" {
		launch.StreamURL = taterLocalStreamURL(baseURL, categoryID, launch.SourceIndex, launch.Path, playerToken)
	}
	if cfg == nil {
		return launch
	}
	if category, ok := taterLocalMediaCategory(cfg, categoryID); ok {
		roots := taterLocalMediaCategoryPaths(category)
		if launch.SourceIndex >= 0 && launch.SourceIndex < len(roots) {
			if mediaPath, err := safeLocalPath(roots[launch.SourceIndex], launch.Path); err == nil {
				if launch.Type == "localFolder" {
					mediaPath += string(filepath.Separator)
				}
				taterApplyLocalMetadata(mediaPath, &launch)
			}
		}
	}

	fresh := launch
	fresh.Poster, fresh.Backdrop, fresh.SeriesPoster, fresh.SeasonPoster, fresh.EpisodeStill = "", "", "", "", ""
	rows := []taterUsenetItem{fresh}
	decorateTaterPlayerHomeItems(cfg, baseURL, playerToken, rows)
	fresh = rows[0]
	launch.Poster = refreshedTaterRecommendationArtwork(launch.Poster, fresh.Poster)
	launch.Backdrop = refreshedTaterRecommendationArtwork(launch.Backdrop, fresh.Backdrop)
	launch.SeriesPoster = refreshedTaterRecommendationArtwork(launch.SeriesPoster, fresh.SeriesPoster)
	launch.SeasonPoster = refreshedTaterRecommendationArtwork(launch.SeasonPoster, fresh.SeasonPoster)
	launch.EpisodeStill = refreshedTaterRecommendationArtwork(launch.EpisodeStill, fresh.EpisodeStill)
	launch.HasArtwork = strings.TrimSpace(launch.Poster) != ""
	return launch
}

func refreshedTaterRecommendationArtwork(saved, current string) string {
	if current != "" {
		return current
	}
	// Discard a stale local URL if the sidecar was removed, while keeping legacy
	// remote artwork as a fallback when there is no local artwork to replace it.
	if parsed, err := url.Parse(saved); err == nil && parsed.Path == "/api/v1/player/artwork/local" {
		return ""
	}
	return saved
}
