package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
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

func TestTaterPlayerLibraryShuffleIsStableForOnePlayerSession(t *testing.T) {
	items := make([]taterUsenetItem, 16)
	for index := range items {
		items[index] = taterUsenetItem{
			Title:       fmt.Sprintf("Movie %02d", index),
			CategoryID:  "local:movies",
			SourceIndex: 0,
			Path:        fmt.Sprintf("Movie %02d/movie.mkv", index),
		}
	}

	first := append([]taterUsenetItem(nil), items...)
	second := append([]taterUsenetItem(nil), items...)
	nextSession := append([]taterUsenetItem(nil), items...)
	taterShufflePlayerLibraryItems(first, "player-session-one", "local-discover:genre:action")
	taterShufflePlayerLibraryItems(second, "player-session-one", "local-discover:genre:action")
	taterShufflePlayerLibraryItems(nextSession, "player-session-two", "local-discover:genre:action")

	require.Equal(t, first, second)
	require.NotEqual(t, items, first)
	require.NotEqual(t, first, nextSession)
	require.True(t, taterPlayerLibraryShuffleEligible("local-discover:genre:action"))
	require.False(t, taterPlayerLibraryShuffleEligible("local-discover:decade:1980"))
	require.False(t, taterPlayerLibraryShuffleEligible("local-discover:recent"))
	require.False(t, taterPlayerLibraryShuffleEligible("local-discover:movies"))
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
	writeTaterPlayerTestJPEG(t, filepath.Join(movieDir, "poster.jpg"), 1200, 1800,
		color.RGBA{R: 210, G: 90, B: 30, A: 255})
	writeTaterPlayerTestJPEG(t, filepath.Join(movieDir, "backdrop.jpg"), 1920, 1080,
		color.RGBA{R: 35, G: 70, B: 110, A: 255})

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
	app.Get("/api/tater/playstate/continue", server.handleTaterPlayStateContinue)
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
	require.Contains(t, envelope.Data.ContinueWatching[0].Poster, "thumbnail=poster")
	require.Contains(t, envelope.Data.ContinueWatching[0].Backdrop, "/api/v1/player/artwork/local")
	require.Contains(t, envelope.Data.ContinueWatching[0].Backdrop, "kind=backdrop")
	require.Contains(t, envelope.Data.ContinueWatching[0].Backdrop, "thumbnail=wide")

	posterURL, err := url.Parse(envelope.Data.ContinueWatching[0].Poster)
	require.NoError(t, err)
	require.NotEmpty(t, posterURL.Query().Get("v"))
	artworkResponse, err := app.Test(httptest.NewRequest(http.MethodGet, posterURL.RequestURI(), nil))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, artworkResponse.StatusCode)
	require.Equal(t, "image/jpeg", artworkResponse.Header.Get(fiber.HeaderContentType))
	require.Equal(t, "private, max-age=31536000, immutable",
		artworkResponse.Header.Get(fiber.HeaderCacheControl))
	servedPoster, err := io.ReadAll(artworkResponse.Body)
	require.NoError(t, err)
	posterConfig, _, err := image.DecodeConfig(bytes.NewReader(servedPoster))
	require.NoError(t, err)
	require.Equal(t, 600, posterConfig.Width)
	require.Equal(t, 900, posterConfig.Height)

	backdropURL, err := url.Parse(envelope.Data.ContinueWatching[0].Backdrop)
	require.NoError(t, err)
	backdropResponse, err := app.Test(httptest.NewRequest(http.MethodGet, backdropURL.RequestURI(), nil))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, backdropResponse.StatusCode)
	servedBackdrop, err := io.ReadAll(backdropResponse.Body)
	require.NoError(t, err)
	backdropConfig, _, err := image.DecodeConfig(bytes.NewReader(servedBackdrop))
	require.NoError(t, err)
	require.Equal(t, 960, backdropConfig.Width)
	require.Equal(t, 540, backdropConfig.Height)

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

	continueRequest := httptest.NewRequest(http.MethodGet,
		"http://tube.local/api/tater/playstate/continue", nil)
	continueRequest.Header.Set(fiber.HeaderAuthorization, "Bearer home-token")
	continueResponse, err := app.Test(continueRequest)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, continueResponse.StatusCode)
	var continueEnvelope testAPIResponse[struct {
		Items []taterUsenetItem `json:"items"`
	}]
	require.NoError(t, json.NewDecoder(continueResponse.Body).Decode(&continueEnvelope))
	require.True(t, continueEnvelope.Success)
	require.Len(t, continueEnvelope.Data.Items, 1)
	require.Contains(t, continueEnvelope.Data.Items[0].Poster,
		"/api/v1/player/artwork/local")
	require.Contains(t, continueEnvelope.Data.Items[0].Poster,
		"player_token=home-token")
	require.Contains(t, continueEnvelope.Data.Items[0].Poster,
		"thumbnail=poster")
}

func TestTaterPlayerHomeCanLoadShelvesWithoutLiveGuide(t *testing.T) {
	configDir := t.TempDir()
	enabled := true
	cfg := config.DefaultConfig(configDir)
	cfg.LocalMedia.Enabled = &enabled
	cfg.LocalMedia.Categories = []config.LocalMediaCategory{{
		ID: "movies", Name: "Movies", LibraryType: "movies",
		Paths: []string{t.TempDir()}, Enabled: &enabled,
	}}
	cfg.TubeTV.Enabled = &enabled
	cfg.Players.Paired = []config.PlayerConfig{{
		ID: "home-player", TokenHash: hashTaterSecret("home-token"),
	}}

	now := time.Now().UTC().Truncate(time.Second)
	taterTVGuideMu.Lock()
	previousGuide := taterTVGuideCache
	taterTVGuideCache = &taterTVGuideCacheEntry{
		StartedAt:    now.Add(-time.Minute),
		GeneratedAt:  now.Add(-time.Minute),
		UpdatedAt:    now,
		PlannedUntil: now.Add(time.Hour),
		Fingerprint:  taterTVGuideFingerprint(cfg),
		Channels: []taterTVChannel{{
			Number: "12", Title: "Movie Night", TotalDuration: 3600,
			Schedule: []map[string]any{{
				"title": "Playing Now", "kind": "movie", "start": 0.0, "end": 3600.0,
			}},
		}},
	}
	taterTVGuideMu.Unlock()
	t.Cleanup(func() {
		taterTVGuideMu.Lock()
		taterTVGuideCache = previousGuide
		taterTVGuideMu.Unlock()
	})

	server := &Server{configManager: &mockConfigManager{cfg: cfg}}
	app := fiber.New()
	app.Get("/api/v1/player/home", server.handleTaterPlayerHome)

	requestHome := func(path string) taterPlayerHomeResponse {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set(fiber.HeaderAuthorization, "Bearer home-token")
		response, err := app.Test(request)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode)
		var envelope testAPIResponse[taterPlayerHomeResponse]
		require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
		require.True(t, envelope.Success)
		return envelope.Data
	}

	combined := requestHome("/api/v1/player/home")
	require.True(t, combined.Capabilities.TubeTV)
	require.Len(t, combined.LiveChannels, 1)

	shelvesOnly := requestHome("/api/v1/player/home?include_live=0")
	require.True(t, shelvesOnly.Capabilities.TubeTV)
	require.Empty(t, shelvesOnly.LiveChannels)
}

func writeTaterPlayerTestJPEG(t *testing.T, path string, width, height int, fill color.Color) {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(source, source.Bounds(), &image.Uniform{C: fill}, image.Point{}, draw.Src)
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, jpeg.Encode(file, source, &jpeg.Options{Quality: 90}))
	require.NoError(t, file.Close())
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
		"title":        "Saturday Cartoons",
		"kind":         "episode",
		"mediaType":    "episode",
		"poster":       "https://art.example/poster.jpg",
		"backdrop":     "https://art.example/backdrop.jpg",
		"episodeStill": "https://art.example/episode.jpg",
		"start":        100.0,
		"end":          200.0,
	}, 125)

	require.NotNil(t, program)
	require.Equal(t, "Saturday Cartoons", program.Title)
	require.Equal(t, 25.0, program.ProgressPercent)
	require.Equal(t, startedAt.Add(100*time.Second), program.StartsAt)
	require.True(t, strings.EqualFold("episode", program.Kind))
	require.Equal(t, "https://art.example/backdrop.jpg", program.Backdrop)
	require.Equal(t, "https://art.example/episode.jpg", program.EpisodeStill)
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
