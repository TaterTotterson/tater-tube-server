package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"
)

func TestTaterPlayerHomeRequiresPairedPlayer(t *testing.T) {
	app := fiber.New()
	server := &Server{configManager: &mockConfigManager{cfg: &config.Config{}}}
	app.Get("/api/v1/player/home", server.handleTaterPlayerHome)

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/player/home", nil))
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
}

func TestTaterPlayerHomeAggregatesLocalMediaAndArtwork(t *testing.T) {
	configDir := t.TempDir()
	libraryRoot := t.TempDir()
	movieDir := filepath.Join(libraryRoot, "Modern.Movie.2026")
	require.NoError(t, os.MkdirAll(movieDir, 0o755))
	moviePath := filepath.Join(movieDir, "Modern.Movie.2026.mkv")
	require.NoError(t, os.WriteFile(moviePath, []byte("media"), 0o644))
	posterBytes := []byte("poster-image")
	require.NoError(t, os.WriteFile(filepath.Join(movieDir, "poster.jpg"), posterBytes, 0o644))

	localEnabled := true
	tubeTVDisabled := false
	cfg := config.DefaultConfig(configDir)
	cfg.LocalMedia.Enabled = &localEnabled
	cfg.LocalMedia.Categories = []config.LocalMediaCategory{{
		ID:          "movies",
		Name:        "Movies",
		LibraryType: "movies",
		Paths:       []string{libraryRoot},
		Enabled:     &localEnabled,
	}}
	cfg.TubeTV.Enabled = &tubeTVDisabled
	cfg.Players.Paired = []config.PlayerConfig{{
		ID:        "home-player",
		Name:      "Living Room",
		TokenHash: hashTaterSecret("home-token"),
	}}

	relPath := "Modern.Movie.2026/Modern.Movie.2026.mkv"
	stateID := taterLocalPlayStateID("local:movies", 0, relPath)
	require.NoError(t, saveTaterPlayStateStore(cfg, taterPlayStateStore{Items: map[string]taterPlayState{
		stateID: {
			ID:          stateID,
			Title:       "Modern Movie",
			MediaType:   "movie",
			CategoryID:  "local:movies",
			SourceIndex: 0,
			Path:        relPath,
			PositionMS:  90_000,
			DurationMS:  600_000,
			UpdatedAt:   time.Now().UTC(),
		},
	}}))

	server := &Server{configManager: &mockConfigManager{cfg: cfg}}
	app := fiber.New()
	app.Get("/api/v1/player/home", server.handleTaterPlayerHome)
	app.Get("/api/v1/player/artwork/local", server.handleTaterPlayerLocalArtwork)

	request := httptest.NewRequest(http.MethodGet, "http://tube.local/api/v1/player/home", nil)
	request.Header.Set(fiber.HeaderAuthorization, "Bearer home-token")
	response, err := app.Test(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)

	var envelope testAPIResponse[taterPlayerHomeResponse]
	require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
	require.True(t, envelope.Success)
	require.Equal(t, taterPlayerHomeProtocolVersion, envelope.Data.ProtocolVersion)
	require.True(t, envelope.Data.Capabilities.LocalMedia)
	require.False(t, envelope.Data.Capabilities.TubeTV)
	require.Len(t, envelope.Data.ContinueWatching, 1)
	require.NotEmpty(t, envelope.Data.RecentlyAdded)
	require.NotEmpty(t, envelope.Data.Libraries)
	require.Contains(t, envelope.Data.ContinueWatching[0].Poster, "/api/v1/player/artwork/local")
	require.Contains(t, envelope.Data.ContinueWatching[0].Poster, "player_token=home-token")

	posterURL, err := url.Parse(envelope.Data.ContinueWatching[0].Poster)
	require.NoError(t, err)
	artworkResponse, err := app.Test(httptest.NewRequest(http.MethodGet, posterURL.RequestURI(), nil))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, artworkResponse.StatusCode)
	require.Equal(t, "image/jpeg", artworkResponse.Header.Get(fiber.HeaderContentType))
	servedPoster, err := io.ReadAll(artworkResponse.Body)
	require.NoError(t, err)
	require.Equal(t, posterBytes, servedPoster)
}

func TestTaterPlayerLocalArtworkRejectsEscapingPath(t *testing.T) {
	libraryRoot := t.TempDir()
	enabled := true
	cfg := &config.Config{
		LocalMedia: config.LocalMediaConfig{
			Enabled: &enabled,
			Categories: []config.LocalMediaCategory{{
				ID:          "movies",
				Name:        "Movies",
				LibraryType: "movies",
				Paths:       []string{libraryRoot},
				Enabled:     &enabled,
			}},
		},
		Players: config.PlayersConfig{Paired: []config.PlayerConfig{{
			ID:        "home-player",
			TokenHash: hashTaterSecret("home-token"),
		}}},
	}
	server := &Server{configManager: &mockConfigManager{cfg: cfg}}
	app := fiber.New()
	app.Get("/api/v1/player/artwork/local", server.handleTaterPlayerLocalArtwork)

	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/player/artwork/local?category_id=movies&source=0&path="+url.QueryEscape("../../outside.mkv")+"&player_token=home-token", nil)
	response, err := app.Test(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, response.StatusCode)
}

func TestTaterPlayerHomeProgramReportsCurrentProgress(t *testing.T) {
	startedAt := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	program := taterPlayerHomeProgramFromSchedule(nil, "", "", startedAt, map[string]any{
		"title":     "Saturday Cartoons",
		"kind":      "episode",
		"mediaType": "episode",
		"start":     100.0,
		"end":       200.0,
	}, 125)

	require.NotNil(t, program)
	require.Equal(t, "Saturday Cartoons", program.Title)
	require.Equal(t, 25.0, program.ProgressPercent)
	require.Equal(t, startedAt.Add(100*time.Second), program.StartsAt)
	require.True(t, strings.EqualFold("episode", program.Kind))
}

func TestTaterPlayerHomeChannelsNeverWaitsForGuideRefresh(t *testing.T) {
	taterTVResetGuide()
	taterTVGuideMu.Lock()
	started := time.Now()
	_, err := taterPlayerHomeChannels(&config.Config{}, "http://server", "token", started)
	elapsed := time.Since(started)
	taterTVGuideMu.Unlock()

	require.Error(t, err)
	require.Less(t, elapsed, 100*time.Millisecond)
}
