package api

import (
	"context"
	"database/sql"
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
	"github.com/TaterTotterson/tater-tube-server/internal/database"
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

func TestTaterPlayerLibraryRequiresPairedPlayer(t *testing.T) {
	app := fiber.New()
	server := &Server{configManager: &mockConfigManager{cfg: &config.Config{}}}
	app.Get("/api/v1/player/library", server.handleTaterPlayerLibrary)

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/player/library", nil))
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
}

func TestTaterPlayerLinkedHeroFallsBackWithoutTaterLink(t *testing.T) {
	server := &Server{}
	connected, hero := server.taterPlayerLinkedHero(context.Background(), time.Now().UTC())
	require.False(t, connected)
	require.Nil(t, hero)
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
	queueDB, err := database.NewDB(database.Config{
		Type: "sqlite", DatabasePath: filepath.Join(configDir, "player-home.db"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = queueDB.Close() })
	queueRepo := database.NewRepository(queueDB.Connection(), database.DialectSQLite)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, queueRepo.CreateTaterCorePairingCode(ctx, database.TaterCorePairingCode{
		ID: "hero-code", Name: "Living Room Tater", CodeHash: "hero-pin",
		CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}))
	paired, err := queueRepo.PairTaterCore(ctx, "hero-pin", now, database.TaterCoreConnection{
		ID: "hero-core", Name: "Living Room Tater", AssistantName: "Totty",
		TokenHash: "hero-token", CreatedAt: now,
		LastSeenAt: sql.NullTime{Time: now, Valid: true},
	})
	require.NoError(t, err)
	require.True(t, paired)
	require.NoError(t, queueRepo.SaveTaterRecommendations(ctx, database.TaterRecommendationBatch{
		ID: "hero-batch", ProfileID: taterDefaultProfileID, CoreID: "hero-core",
		Summary:     "It is a cozy Friday night, and Moonrise Manor fits the mood.",
		GeneratedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}, []database.TaterRecommendation{{
		ID: "hero-pick", BatchID: "hero-batch", Rank: 1, CandidateID: "hero-movie",
		Title: "Moonrise Manor", MediaType: "movie", Source: "local_media",
		Reason: "A cozy next watch.", LaunchJSON: `{}`, CreatedAt: now,
	}}))

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

	server := &Server{configManager: &mockConfigManager{cfg: cfg}, queueRepo: queueRepo}
	app := fiber.New()
	app.Get("/api/v1/player/home", server.handleTaterPlayerHome)
	app.Get("/api/v1/player/library", server.handleTaterPlayerLibrary)
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
	require.True(t, envelope.Data.Capabilities.TaterLink)
	require.NotNil(t, envelope.Data.Hero)
	require.True(t, envelope.Data.Hero.Personalized)
	require.Equal(t, "Totty", envelope.Data.Hero.Assistant)
	require.Contains(t, envelope.Data.Hero.Message, "Moonrise Manor")
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

	libraryRequest := httptest.NewRequest(http.MethodGet, "http://tube.local/api/v1/player/library", nil)
	libraryRequest.Header.Set(fiber.HeaderAuthorization, "Bearer home-token")
	libraryResponse, err := app.Test(libraryRequest)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, libraryResponse.StatusCode)
	var libraryEnvelope testAPIResponse[struct {
		Rows []taterPlayerLibraryRow `json:"rows"`
	}]
	require.NoError(t, json.NewDecoder(libraryResponse.Body).Decode(&libraryEnvelope))
	require.True(t, libraryEnvelope.Success)
	require.NotEmpty(t, libraryEnvelope.Data.Rows)
	foundRecentlyAdded := false
	for _, row := range libraryEnvelope.Data.Rows {
		if row.Entry.ID == "local-discover:recent" {
			foundRecentlyAdded = true
			require.NotEmpty(t, row.Items)
			require.Contains(t, row.Items[0].Poster, "/api/v1/player/artwork/local")
		}
	}
	require.True(t, foundRecentlyAdded)
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
