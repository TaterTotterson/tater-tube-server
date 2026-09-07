package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/TaterTotterson/tater-tube-server/internal/database"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"
)

func TestTaterPlayerRecommendationsRefreshSavedArtworkForRequestingPlayer(t *testing.T) {
	configDir, libraryRoot := t.TempDir(), t.TempDir()
	moviePath := "Movie (2026)/Movie.mkv"
	showPath := "Some.Show.2024"
	episodePath := showPath + "/Season 01/Some.Show.S01E01.mkv"
	enabled := true
	cfg := config.DefaultConfig(configDir)
	cfg.LocalMedia.Enabled = &enabled
	cfg.LocalMedia.Categories = []config.LocalMediaCategory{
		{ID: "movies", Name: "Movies", LibraryType: "movies", Paths: []string{libraryRoot}, Enabled: &enabled},
		{ID: "tv", Name: "TV Shows", LibraryType: "tv", Paths: []string{libraryRoot}, Enabled: &enabled},
	}
	cfg.Players.Paired = []config.PlayerConfig{
		{ID: "deck", Name: "Steam Deck", TokenHash: hashTaterSecret("deck-token")},
		{ID: "retro", Name: "Retro Player", TokenHash: hashTaterSecret("retro-token")},
	}
	queueDB, err := database.NewDB(database.Config{Type: "sqlite", DatabasePath: filepath.Join(configDir, "picks.db")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = queueDB.Close() })
	repo := database.NewRepository(queueDB.Connection(), database.DialectSQLite)
	now := time.Now().UTC().Truncate(time.Millisecond)
	batch := database.TaterRecommendationBatch{
		ID: "saved-batch", ProfileID: taterDefaultProfileID,
		Summary:     "A movie and a new series for tonight.",
		GeneratedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	launches := []taterUsenetItem{
		{Title: "Movie", Type: "localFile", MediaType: "movie", CategoryID: "local:movies", Path: moviePath, Key: "movie-key", SeekMode: "client",
			Poster: "http://old-host/api/v1/player/artwork/local?player_token=old-token", Backdrop: "http://old-host/api/v1/player/artwork/local?kind=backdrop&player_token=old-token"},
		{Title: "Some Show", Type: "localFolder", MediaType: "show", CategoryID: "local:tv", Path: showPath},
		{Title: "Episode 1", Type: "localFile", MediaType: "episode", CategoryID: "local:tv", Path: episodePath, SeekMode: "client"},
		{Title: "RETRO TV", Type: "module", MediaType: "live", ModuleID: "com.240mp.ota", ChannelNumber: "5.1", ChannelName: "RETRO TV"},
	}
	picks := make([]database.TaterRecommendation, 0, len(launches))
	for index, launch := range launches {
		encoded, err := json.Marshal(launch)
		require.NoError(t, err)
		id := launch.Title
		picks = append(picks, database.TaterRecommendation{
			ID: id, BatchID: batch.ID, CandidateID: id, Rank: index + 1, Title: launch.Title,
			MediaType: launch.MediaType, Source: "local_media", Reason: "A good next watch.", LaunchJSON: string(encoded), CreatedAt: now,
		})
	}
	require.NoError(t, repo.SaveTaterRecommendations(context.Background(), batch, picks))

	// Simulate artwork and NFOs being scraped after this batch was generated.
	files := map[string]string{
		moviePath: "movie", "Movie (2026)/poster.jpg": "movie-poster", "Movie (2026)/backdrop.jpg": "movie-backdrop",
		"Movie (2026)/Movie.nfo": `<movie><title>Updated Movie</title><plot>A fresh movie synopsis.</plot><year>2026</year><mpaa>PG</mpaa></movie>`,
		episodePath:              "episode", showPath + "/poster.jpg": "show-poster", showPath + "/backdrop.jpg": "show-backdrop",
		showPath + "/tvshow.nfo":           `<tvshow><title>Some Show</title><plot>A fresh series synopsis.</plot></tvshow>`,
		showPath + "/Season 01/poster.jpg": "season-poster", showPath + "/Season 01/Some.Show.S01E01-thumb.jpg": "episode-still",
	}
	for relPath, contents := range files {
		path := filepath.Join(libraryRoot, filepath.FromSlash(relPath))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	}
	server := &Server{configManager: &mockConfigManager{cfg: cfg}, queueRepo: repo}
	app := fiber.New()
	app.Get("/api/tater/recommendations", server.handleTaterPlayerRecommendations)
	app.Get("/api/v1/player/artwork/local", server.handleTaterPlayerLocalArtwork)
	var firstPosterURL string
	for _, token := range []string{"deck-token", "retro-token"} {
		request := httptest.NewRequest(http.MethodGet, "http://tube.local/api/tater/recommendations", nil)
		request.Header.Set(fiber.HeaderAuthorization, "Bearer "+token)
		response, err := app.Test(request)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode)
		var envelope testAPIResponse[struct {
			Batch database.TaterRecommendationBatch `json:"batch"`
			Items []taterRecommendationResponse     `json:"items"`
		}]
		require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
		require.NoError(t, response.Body.Close())
		require.Equal(t, batch.ID, envelope.Data.Batch.ID)
		require.Equal(t, batch.Summary, envelope.Data.Batch.Summary)
		require.Len(t, envelope.Data.Items, 4)
		for index, pick := range envelope.Data.Items {
			encoded, err := json.Marshal(pick.Launch)
			require.NoError(t, err)
			var launch taterUsenetItem
			require.NoError(t, json.Unmarshal(encoded, &launch))
			original := launches[index]
			require.Equal(t, original.Type, launch.Type)
			require.Equal(t, original.MediaType, launch.MediaType)
			require.Equal(t, original.Path, launch.Path)
			require.Equal(t, original.CategoryID, launch.CategoryID)
			require.Equal(t, original.SourceIndex, launch.SourceIndex)
			require.Equal(t, original.Key, launch.Key)
			require.Equal(t, original.SeekMode, launch.SeekMode)
			if original.Type == "module" {
				require.Equal(t, original, launch, "legacy OTA module launches must stay unchanged")
				continue
			}
			if original.Type == "localFile" {
				streamURL, err := url.Parse(launch.StreamURL)
				require.NoError(t, err)
				require.Equal(t, "tube.local", streamURL.Host)
				require.Equal(t, "/api/tater/local/stream", streamURL.Path)
				require.Equal(t, token, streamURL.Query().Get("player_token"))
				require.Equal(t, original.Path, streamURL.Query().Get("path"))
			} else {
				require.Empty(t, launch.StreamURL, "series must remain browsable folders")
			}
			require.True(t, launch.HasArtwork)
			artwork := map[string]string{"poster": launch.Poster, "backdrop": launch.Backdrop}
			if original.CategoryID == "local:tv" {
				artwork["series-poster"] = launch.SeriesPoster
			}
			if original.MediaType == "episode" {
				artwork["season-poster"] = launch.SeasonPoster
				artwork["episode-still"] = launch.EpisodeStill
				require.Equal(t, launch.EpisodeStill, launch.Poster, "poster remains available for old players")
			}
			for kind, rawURL := range artwork {
				require.NotEmpty(t, rawURL, kind)
				parsed, err := url.Parse(rawURL)
				require.NoError(t, err)
				require.Equal(t, "tube.local", parsed.Host)
				require.Equal(t, token, parsed.Query().Get("player_token"))
				require.NotEmpty(t, parsed.Query().Get("v"), "artwork cache version is required")
				if kind != "poster" {
					require.Equal(t, kind, parsed.Query().Get("kind"))
				}
				artResponse, err := app.Test(httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil))
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, artResponse.StatusCode, "artwork URL must work without extra auth headers")
				raw, err := io.ReadAll(artResponse.Body)
				require.NoError(t, err)
				require.NotEmpty(t, raw)
				require.NoError(t, artResponse.Body.Close())
			}
			if index == 0 {
				require.Equal(t, "Updated Movie", launch.Title)
				require.Equal(t, "A fresh movie synopsis.", launch.Description)
				require.Equal(t, "2026", launch.Date)
				require.Equal(t, "PG", launch.ContentRating)
				if firstPosterURL == "" {
					firstPosterURL = launch.Poster
				} else {
					require.NotEqual(t, firstPosterURL, launch.Poster, "each paired player gets its own artwork token")
				}
			} else if index == 1 {
				require.Equal(t, "A fresh series synopsis.", launch.Description)
			}
		}
	}
	_, stored, err := repo.GetActiveTaterRecommendations(context.Background(), taterDefaultProfileID, now)
	require.NoError(t, err)
	require.Equal(t, picks[0].LaunchJSON, stored[0].LaunchJSON, "reading picks must not persist a player's token into shared recommendations")
}

func TestTaterRecommendationArtworkKeepsLegacyPosterFallback(t *testing.T) {
	cfg := config.DefaultConfig(t.TempDir())
	cfg.LocalMedia.Categories = []config.LocalMediaCategory{{ID: "movies", LibraryType: "movies", Paths: []string{t.TempDir()}}}
	launch := taterUsenetItem{
		Type: "localFile", MediaType: "movie", CategoryID: "local:movies", Path: "Movie.mkv",
		Poster:   "https://images.example/movie-poster.jpg",
		Backdrop: "http://old-host/api/v1/player/artwork/local?kind=backdrop&player_token=old-token",
	}
	refreshed := refreshTaterRecommendationLaunch(cfg, "http://tube.local", "player-token", launch)
	require.Equal(t, launch.Poster, refreshed.Poster)
	require.True(t, refreshed.HasArtwork)
	require.Empty(t, refreshed.Backdrop, "missing local sidecars must not retain expired artwork URLs")
}
