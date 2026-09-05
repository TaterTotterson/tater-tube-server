package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

func TestTaterVideoArtworkIndexReusesCompatibleMovieAndShowSidecars(t *testing.T) {
	root := t.TempDir()
	movieRoot := filepath.Join(root, "movies")
	tvRoot := filepath.Join(root, "tv")
	movieDir := filepath.Join(movieRoot, "Arrival (2016)")
	showDir := filepath.Join(tvRoot, "Severance (2022)")
	seasonDir := filepath.Join(showDir, "Season 01")
	for _, dir := range []string{movieDir, seasonDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for path, contents := range map[string]string{
		filepath.Join(movieDir, "Arrival (2016).mkv"):    "movie",
		filepath.Join(movieDir, "poster.jpg"):            "movie-poster",
		filepath.Join(seasonDir, "Severance S01E01.mkv"): "episode",
		filepath.Join(showDir, "cover.png"):              "show-poster",
		filepath.Join(showDir, "tvshow.nfo"):             "<tvshow><title>Severance</title><year>2022</year></tvshow>",
	} {
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}

	enabled := true
	cfg := &config.Config{LocalMedia: config.LocalMediaConfig{
		Enabled: &enabled,
		Categories: []config.LocalMediaCategory{
			{ID: "movies", Name: "Movies", LibraryType: "movies", Paths: []string{movieRoot}, Enabled: &enabled},
			{ID: "tv", Name: "TV Shows", LibraryType: "tv", Paths: []string{tvRoot}, Enabled: &enabled},
		},
	}}
	index := taterLocalLibraryIndex{
		Categories: []taterLocalLibraryCategoryIndex{
			{ID: "movies", Name: "Movies", LibraryType: "movies"},
			{ID: "tv", Name: "TV Shows", LibraryType: "tv"},
		},
		Files: []taterLocalLibraryFileIndex{
			{CategoryID: "movies", LibraryType: "movies", SourceIndex: 0, Path: "Arrival (2016)/Arrival (2016).mkv", SizeBytes: 10, ModifiedUnix: 10},
			{CategoryID: "tv", LibraryType: "tv", SourceIndex: 0, Path: "Severance (2022)/Season 01/Severance S01E01.mkv", SizeBytes: 20, ModifiedUnix: 20},
		},
	}
	buildTaterLocalLibraryStats(cfg, &index)
	if len(index.Videos) != 2 {
		t.Fatalf("expected movie and show artwork records, got %#v", index.Videos)
	}
	for _, video := range index.Videos {
		if !video.HasArtwork || video.ArtworkSource != "local" {
			t.Fatalf("expected local sidecar artwork for %#v", video)
		}
	}
	if index.Categories[0].Stats.Artwork != 1 || index.Categories[1].Stats.Artwork != 1 {
		t.Fatalf("unexpected artwork stats: %#v", index.Categories)
	}
	episodeArt, found := taterPlayerLocalArtworkPath(cfg, "tv", 0, "Severance (2022)/Season 01/Severance S01E01.mkv")
	if !found || episodeArt != filepath.Join(showDir, "cover.png") {
		t.Fatalf("episode did not inherit show artwork: path=%q found=%v", episodeArt, found)
	}
}

func TestDecorateTaterDiscoveryArtworkPreservesCinemetaAndOnlyFillsMissing(t *testing.T) {
	enabled := true
	cfg := &config.Config{LocalMedia: config.LocalMediaConfig{
		TMDBEnabled: &enabled,
		TMDBAPIKey:  "tmdb-test-key",
	}}
	items := []taterUsenetItem{
		{Title: "Existing Art", MediaType: "movie", Date: "2025", Poster: "https://cinemeta.example/poster.jpg"},
		{Title: "Missing Art", MediaType: "series", Date: "2024-01-01", GUID: "tt1234567"},
	}

	decorateTaterDiscoveryArtwork(cfg, "http://tube.local", "player-token", items)
	if items[0].Poster != "https://cinemeta.example/poster.jpg" {
		t.Fatalf("existing Cinemeta artwork was replaced: %q", items[0].Poster)
	}
	fallback, err := url.Parse(items[1].Poster)
	if err != nil {
		t.Fatal(err)
	}
	if fallback.Path != "/api/v1/player/artwork/discovery" ||
		fallback.Query().Get("title") != "Missing Art" ||
		fallback.Query().Get("media_type") != "series" ||
		fallback.Query().Get("year") != "2024" ||
		fallback.Query().Get("external_id") != "tt1234567" ||
		fallback.Query().Get("player_token") != "player-token" {
		t.Fatalf("unexpected Discovery fallback URL: %q", items[1].Poster)
	}
}

func TestTaterPlayerArtworkPrefersSavedVideoArtworkChoice(t *testing.T) {
	root := t.TempDir()
	movieRoot := filepath.Join(root, "movies")
	movieDir := filepath.Join(movieRoot, "Example Movie (2026)")
	if err := os.MkdirAll(movieDir, 0755); err != nil {
		t.Fatal(err)
	}
	moviePath := filepath.Join(movieDir, "Example Movie (2026).mkv")
	defaultPoster := filepath.Join(movieDir, "poster.jpg")
	chosenPoster := filepath.Join(movieDir, "Example Movie (2026)-poster.png")
	for path, contents := range map[string]string{
		moviePath:     "movie",
		defaultPoster: "old-poster",
		chosenPoster:  "chosen-poster",
	} {
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	enabled := true
	cfg := &config.Config{
		Metadata: config.MetadataConfig{RootPath: filepath.Join(root, "metadata")},
		LocalMedia: config.LocalMediaConfig{Categories: []config.LocalMediaCategory{{
			ID: "movies", Name: "Movies", LibraryType: "movies", Paths: []string{movieRoot}, Enabled: &enabled,
		}}},
	}
	relPath := "Example Movie (2026)/Example Movie (2026).mkv"
	mediaID := taterVideoMediaID("movies", 0, "movie", relPath)
	if err := writeTaterVideoArtworkStore(cfg, taterVideoArtworkStore{Items: map[string]taterVideoArtworkOverride{
		mediaID: {MediaID: mediaID, Ref: "Example Movie (2026)/Example Movie (2026)-poster.png", Source: "scraped"},
	}}); err != nil {
		t.Fatal(err)
	}

	artworkPath, found := taterStoredVideoArtworkPath(cfg, cfg.LocalMedia.Categories[0], 0, relPath)
	if !found || artworkPath != chosenPoster {
		t.Fatalf("saved artwork choice was not preferred: path=%q found=%v", artworkPath, found)
	}
}

func TestTaterTMDBArtworkScraperWritesPosterBesideMovie(t *testing.T) {
	oldBaseURL := taterTMDBBaseURL
	oldImageBaseURL := taterTMDBImageBaseURL
	oldClient := taterTMDBHTTPClient
	t.Cleanup(func() {
		taterTMDBBaseURL = oldBaseURL
		taterTMDBImageBaseURL = oldImageBaseURL
		taterTMDBHTTPClient = oldClient
	})

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/3/search/movie":
			if request.URL.Query().Get("api_key") != "tmdb-test-key" || request.URL.Query().Get("primary_release_year") != "2016" {
				http.Error(response, "missing query", http.StatusBadRequest)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"results":[{"id":329865,"title":"Arrival","release_date":"2016-11-10","poster_path":"/arrival.jpg","popularity":50}]}`))
		case "/image/arrival.jpg":
			response.Header().Set("Content-Type", "image/jpeg")
			_, _ = response.Write([]byte("\xff\xd8arrival-poster\xff\xd9"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	taterTMDBBaseURL = server.URL + "/3"
	taterTMDBImageBaseURL = server.URL + "/image"
	taterTMDBHTTPClient = server.Client()

	root := t.TempDir()
	movieRoot := filepath.Join(root, "movies")
	movieDir := filepath.Join(movieRoot, "Arrival (2016)")
	if err := os.MkdirAll(movieDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(movieDir, "Arrival (2016).mkv"), []byte("movie"), 0644); err != nil {
		t.Fatal(err)
	}
	enabled := true
	cfg := &config.Config{
		Metadata: config.MetadataConfig{RootPath: filepath.Join(root, "metadata")},
		LocalMedia: config.LocalMediaConfig{
			Enabled: &enabled, TMDBEnabled: &enabled, TMDBAPIKey: "tmdb-test-key",
			Categories: []config.LocalMediaCategory{{
				ID: "movies", Name: "Movies", LibraryType: "movies", Paths: []string{movieRoot}, Enabled: &enabled,
			}},
		},
	}
	index := taterLocalLibraryIndex{
		Categories: []taterLocalLibraryCategoryIndex{{ID: "movies", Name: "Movies", LibraryType: "movies"}},
		Videos: []taterLocalVideoIndex{{
			ID:         taterVideoMediaID("movies", 0, "movie", "Arrival (2016)/Arrival (2016).mkv"),
			CategoryID: "movies", CategoryName: "Movies", LibraryType: "movies", MediaType: "movie",
			SourceIndex: 0, Path: "Arrival (2016)/Arrival (2016).mkv", Title: "Arrival", Year: "2016",
		}},
	}
	if err := refreshTaterVideoArtwork(context.Background(), cfg, &index, &index.Videos[0], false); err != nil {
		t.Fatal(err)
	}
	posterPath := filepath.Join(movieDir, "poster.jpg")
	if raw, err := os.ReadFile(posterPath); err != nil || string(raw) != "\xff\xd8arrival-poster\xff\xd9" {
		t.Fatalf("sidecar poster mismatch: %q error=%v", raw, err)
	}
	video := index.Videos[0]
	if !video.HasArtwork || video.ArtworkSource != "scraped" || video.TMDBID != 329865 || !strings.HasSuffix(video.ArtworkRef, "poster.jpg") {
		t.Fatalf("unexpected scraped artwork record: %#v", video)
	}
	store := readTaterVideoArtworkStore(cfg)
	if stored := store.Items[video.ID]; stored.Ref != video.ArtworkRef || stored.TMDBID != 329865 {
		t.Fatalf("artwork provenance was not saved: %#v", stored)
	}
}

func TestTaterTMDBArtworkUsesDiscoveryIMDbIDBeforeTitleSearch(t *testing.T) {
	oldBaseURL := taterTMDBBaseURL
	oldImageBaseURL := taterTMDBImageBaseURL
	oldClient := taterTMDBHTTPClient
	t.Cleanup(func() {
		taterTMDBBaseURL = oldBaseURL
		taterTMDBImageBaseURL = oldImageBaseURL
		taterTMDBHTTPClient = oldClient
	})

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/3/find/tt11280740":
			if request.URL.Query().Get("external_source") != "imdb_id" || request.URL.Query().Has("include_adult") {
				http.Error(response, "invalid find query", http.StatusBadRequest)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"movie_results":[],"tv_results":[{"id":95396,"name":"Severance","first_air_date":"2022-02-17","poster_path":"/severance.jpg","popularity":80}]}`))
		case "/image/severance.jpg":
			response.Header().Set("Content-Type", "image/jpeg")
			_, _ = response.Write([]byte("\xff\xd8severance-poster\xff\xd9"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	taterTMDBBaseURL = server.URL + "/3"
	taterTMDBImageBaseURL = server.URL + "/image"
	taterTMDBHTTPClient = server.Client()

	enabled := true
	cfg := &config.Config{LocalMedia: config.LocalMediaConfig{
		TMDBEnabled: &enabled,
		TMDBAPIKey:  "tmdb-test-key",
	}}
	candidate, raw, contentType, err := findTaterRemoteVideoArtworkByExternalID(
		context.Background(),
		cfg,
		taterLocalVideoIndex{Title: "A title that cannot match", LibraryType: "tv", MediaType: "show"},
		"tt11280740",
	)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.TMDBID != 95396 || candidate.Title != "Severance" || contentType != "image/jpeg" || !strings.Contains(string(raw), "severance-poster") {
		t.Fatalf("unexpected IMDb artwork result: candidate=%#v type=%q raw=%q", candidate, contentType, raw)
	}
}

func TestTaterMusicArtworkSidecarUsesCompatibleCoverName(t *testing.T) {
	root := t.TempDir()
	musicRoot := filepath.Join(root, "music")
	albumDir := filepath.Join(musicRoot, "Artist", "Album")
	if err := os.MkdirAll(albumDir, 0755); err != nil {
		t.Fatal(err)
	}
	enabled := true
	cfg := &config.Config{LocalMedia: config.LocalMediaConfig{Categories: []config.LocalMediaCategory{{
		ID: "music", Name: "Music", LibraryType: "music", Paths: []string{musicRoot}, Enabled: &enabled,
	}}}}
	album := taterLocalMusicAlbumIndex{CategoryID: "music", SourceIndex: 0, Path: "Artist/Album"}
	ref, available, err := writeTaterMusicArtworkSidecar(cfg, album, []byte("cover"), "image/jpeg")
	if err != nil || !available {
		t.Fatalf("write sidecar failed: available=%v error=%v", available, err)
	}
	if ref != "Artist/Album/cover.jpg" {
		t.Fatalf("unexpected cover reference: %q", ref)
	}
	if raw, err := os.ReadFile(filepath.Join(albumDir, "cover.jpg")); err != nil || string(raw) != "cover" {
		t.Fatalf("cover sidecar mismatch: %q error=%v", raw, err)
	}
}

func TestTaterTMDBArtworkRequiresUnambiguousTitleWithoutYear(t *testing.T) {
	oldBaseURL := taterTMDBBaseURL
	oldClient := taterTMDBHTTPClient
	t.Cleanup(func() {
		taterTMDBBaseURL = oldBaseURL
		taterTMDBHTTPClient = oldClient
	})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(response, `{"results":[{"id":1,"title":"The Thing","release_date":"1951-01-01","poster_path":"/one.jpg"},{"id":2,"title":"The Thing","release_date":"1982-01-01","poster_path":"/two.jpg"}]}`)
	}))
	defer server.Close()
	taterTMDBBaseURL = server.URL
	taterTMDBHTTPClient = server.Client()
	enabled := true
	cfg := &config.Config{LocalMedia: config.LocalMediaConfig{TMDBEnabled: &enabled, TMDBAPIKey: "key"}}
	_, _, _, err := findTaterRemoteVideoArtwork(context.Background(), cfg, taterLocalVideoIndex{Title: "The Thing", MediaType: "movie"})
	if !os.IsNotExist(err) {
		t.Fatalf("expected ambiguous match to be rejected, got %v", err)
	}
}
