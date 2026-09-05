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
	"sync/atomic"
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

func TestTaterPlayerTVArtworkUsesSpecificSidecarsWithPosterFallback(t *testing.T) {
	root := t.TempDir()
	showDir := filepath.Join(root, "Severance (2022)")
	seasonDir := filepath.Join(showDir, "Season 01")
	episodePath := filepath.Join(seasonDir, "Severance S01E01.mkv")
	for path, contents := range map[string]string{
		episodePath:                                             "episode",
		filepath.Join(showDir, "poster.jpg"):                    "show-poster",
		filepath.Join(showDir, "backdrop.jpg"):                  "show-backdrop",
		filepath.Join(seasonDir, "poster.png"):                  "season-poster",
		filepath.Join(seasonDir, "Severance S01E01-thumb.webp"): "episode-still",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	enabled := true
	cfg := &config.Config{LocalMedia: config.LocalMediaConfig{Categories: []config.LocalMediaCategory{{
		ID: "tv", Name: "TV", LibraryType: "tv", Paths: []string{root}, Enabled: &enabled,
	}}}}
	relEpisode := "Severance (2022)/Season 01/Severance S01E01.mkv"
	checks := map[string]string{
		"series-poster": filepath.Join(showDir, "poster.jpg"),
		"backdrop":      filepath.Join(showDir, "backdrop.jpg"),
		"season-poster": filepath.Join(seasonDir, "poster.png"),
		"episode-still": filepath.Join(seasonDir, "Severance S01E01-thumb.webp"),
	}
	for kind, expected := range checks {
		got, found := taterPlayerLocalArtworkPathForKind(cfg, "local:tv", 0, relEpisode, kind)
		if !found || got != expected {
			t.Fatalf("%s path=%q found=%v, want %q", kind, got, found, expected)
		}
	}

	items := []taterUsenetItem{{
		Title: "Good News About Hell", MediaType: "episode", CategoryID: "local:tv", SourceIndex: 0, Path: relEpisode,
	}}
	decorateTaterPlayerHomeItems(cfg, "http://tube.local", "player-token", items)
	if items[0].Backdrop == "" || items[0].SeriesPoster == "" || items[0].SeasonPoster == "" || items[0].EpisodeStill == "" {
		t.Fatalf("specific TV artwork URLs were not attached: %#v", items[0])
	}
	if items[0].Poster != items[0].EpisodeStill {
		t.Fatalf("episode still should be the compatible poster fallback: %#v", items[0])
	}
	for field, rawURL := range map[string]string{
		"backdrop": items[0].Backdrop, "series-poster": items[0].SeriesPoster,
		"season-poster": items[0].SeasonPoster, "episode-still": items[0].EpisodeStill,
	} {
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Query().Get("kind") != field {
			t.Fatalf("unexpected %s URL: %q error=%v", field, rawURL, err)
		}
	}

	if err := os.Remove(filepath.Join(seasonDir, "Severance S01E01-thumb.webp")); err != nil {
		t.Fatal(err)
	}
	items[0] = taterUsenetItem{
		Title: "Good News About Hell", MediaType: "episode", CategoryID: "local:tv", SourceIndex: 0, Path: relEpisode,
	}
	decorateTaterPlayerHomeItems(cfg, "http://tube.local", "player-token", items)
	if items[0].EpisodeStill != "" || items[0].Poster != items[0].SeasonPoster {
		t.Fatalf("missing episode art did not fall back to the season poster: %#v", items[0])
	}
}

func TestTaterTMDBTVArtworkScraperWritesBackdropSeasonAndEpisodeSidecars(t *testing.T) {
	oldBaseURL := taterTMDBBaseURL
	oldImageBaseURL := taterTMDBImageBaseURL
	oldClient := taterTMDBHTTPClient
	t.Cleanup(func() {
		taterTMDBBaseURL = oldBaseURL
		taterTMDBImageBaseURL = oldImageBaseURL
		taterTMDBHTTPClient = oldClient
	})

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		switch request.URL.Path {
		case "/3/tv/95396":
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"id":95396,"name":"Severance","poster_path":"/show-poster.jpg","backdrop_path":"/show-backdrop.jpg"}`))
		case "/3/tv/95396/season/1":
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"id":121591,"season_number":1,"poster_path":"/season-1.jpg","episodes":[{"episode_number":1,"still_path":"/episode-1.jpg"}]}`))
		case "/image/show-backdrop.jpg":
			response.Header().Set("Content-Type", "image/jpeg")
			_, _ = response.Write([]byte("\xff\xd8show-backdrop\xff\xd9"))
		case "/image/season-1.jpg":
			response.Header().Set("Content-Type", "image/jpeg")
			_, _ = response.Write([]byte("\xff\xd8season-poster\xff\xd9"))
		case "/image/episode-1.jpg":
			response.Header().Set("Content-Type", "image/jpeg")
			_, _ = response.Write([]byte("\xff\xd8episode-still\xff\xd9"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	taterTMDBBaseURL = server.URL + "/3"
	taterTMDBImageBaseURL = server.URL + "/image"
	taterTMDBHTTPClient = server.Client()

	root := t.TempDir()
	showDir := filepath.Join(root, "Severance (2022)")
	seasonDir := filepath.Join(showDir, "Season 01")
	episodePath := filepath.Join(seasonDir, "Severance S01E01.mkv")
	if err := os.MkdirAll(seasonDir, 0755); err != nil {
		t.Fatal(err)
	}
	for path, contents := range map[string]string{
		episodePath:                          "episode",
		filepath.Join(showDir, "poster.jpg"): "existing-show-poster",
	} {
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	enabled := true
	cfg := &config.Config{LocalMedia: config.LocalMediaConfig{
		Enabled: &enabled, TMDBEnabled: &enabled, TMDBAPIKey: "tmdb-test-key",
		Categories: []config.LocalMediaCategory{{
			ID: "tv", Name: "TV", LibraryType: "tv", Paths: []string{root}, Enabled: &enabled,
		}},
	}}
	video := taterLocalVideoIndex{
		ID: taterVideoMediaID("tv", 0, "show", "Severance (2022)"), CategoryID: "tv", CategoryName: "TV",
		LibraryType: "tv", MediaType: "show", SourceIndex: 0, Path: "Severance (2022)", Title: "Severance",
		TMDBID: 95396, HasArtwork: true, HasMetadata: true,
	}
	index := taterLocalLibraryIndex{
		Videos: []taterLocalVideoIndex{video},
		Files: []taterLocalLibraryFileIndex{{
			CategoryID: "tv", LibraryType: "tv", SourceIndex: 0,
			Path: "Severance (2022)/Season 01/Severance S01E01.mkv",
		}},
	}
	if err := refreshTaterVideoArtwork(context.Background(), cfg, &index, &index.Videos[0], false); err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]string{
		filepath.Join(showDir, "backdrop.jpg"):                 "show-backdrop",
		filepath.Join(seasonDir, "poster.jpg"):                 "season-poster",
		filepath.Join(seasonDir, "Severance S01E01-thumb.jpg"): "episode-still",
	} {
		raw, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(raw), expected) {
			t.Fatalf("sidecar %s did not contain %q: %q error=%v", path, expected, raw, err)
		}
	}
	requestCount := requests.Load()
	if err := refreshTaterVideoArtwork(context.Background(), cfg, &index, &index.Videos[0], false); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != requestCount {
		t.Fatalf("complete TV artwork was fetched again: before=%d after=%d", requestCount, requests.Load())
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

func TestTaterExistingEmbyNFOIsUsedAndNeverOverwritten(t *testing.T) {
	root := t.TempDir()
	movieRoot := filepath.Join(root, "movies")
	movieDir := filepath.Join(movieRoot, "Christmas with the Kranks (2004)")
	if err := os.MkdirAll(movieDir, 0755); err != nil {
		t.Fatal(err)
	}
	moviePath := filepath.Join(movieDir, "Christmas with the Kranks (2004) Remux-1080p.mkv")
	nfoPath := strings.TrimSuffix(moviePath, filepath.Ext(moviePath)) + ".nfo"
	if err := os.WriteFile(moviePath, []byte("movie"), 0644); err != nil {
		t.Fatal(err)
	}
	originalNFO := []byte("\xef\xbb\xbf" + `<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<movie>
  <plot>When Blair leaves home, her parents decide to skip Christmas.</plot>
  <title>Christmas with the Kranks</title>
  <originaltitle>Christmas with the Kranks</originaltitle>
  <director tmdbid="18311">Joe Roth</director>
  <writer tmdbid="10965">Chris Columbus</writer>
  <rating>6.154</rating>
  <year>2004</year>
  <mpaa>PG</mpaa>
  <imdbid>tt0388419</imdbid>
  <tmdbid>13673</tmdbid>
  <tvdbid>3544</tvdbid>
  <genre>Comedy</genre>
  <genre>Family</genre>
  <studio>Revolution Studios</studio>
  <country>United States of America</country>
  <actor><name>Tim Allen</name><role>Luther Krank</role></actor>
  <uniqueid type="imdb">tt0388419</uniqueid>
</movie>`)
	if err := os.WriteFile(nfoPath, originalNFO, 0644); err != nil {
		t.Fatal(err)
	}

	enabled := true
	cfg := &config.Config{LocalMedia: config.LocalMediaConfig{Categories: []config.LocalMediaCategory{{
		ID: "movies", Name: "Movies", LibraryType: "movies", Paths: []string{movieRoot}, Enabled: &enabled,
	}}}}
	item := taterUsenetItem{MediaType: "movie"}
	taterApplyLocalMetadata(moviePath, &item)
	if item.Title != "Christmas with the Kranks" || item.Description == "" || item.IMDbID != "tt0388419" ||
		item.TMDBID != 13673 || item.TVDBID != 3544 || item.ContentRating != "PG" || len(item.Genres) != 2 ||
		len(item.Actors) != 1 || item.Actors[0] != "Tim Allen" {
		t.Fatalf("existing NFO metadata was not fully read: %#v", item)
	}

	video := taterLocalVideoIndex{
		ID:         taterVideoMediaID("movies", 0, "movie", filepath.Base(filepath.Dir(moviePath))+"/"+filepath.Base(moviePath)),
		CategoryID: "movies", LibraryType: "movies", MediaType: "movie", SourceIndex: 0,
		Path:  "Christmas with the Kranks (2004)/Christmas with the Kranks (2004) Remux-1080p.mkv",
		Title: "Christmas with the Kranks", Year: "2004",
	}
	written, err := writeTaterVideoNFO(cfg, &video, taterTMDBVideoDetails{
		ID: 99999, Title: "Wrong Replacement", Overview: "This must not replace user metadata.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if written {
		t.Fatal("existing NFO was unexpectedly replaced")
	}
	after, err := os.ReadFile(nfoPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(originalNFO) {
		t.Fatal("existing Emby/Jellyfin NFO content changed")
	}
	if !video.HasMetadata || video.TMDBID != 13673 || video.NFORef == "" {
		t.Fatalf("existing NFO was not attached to the video index: %#v", video)
	}
}

func TestTaterTVMetadataUsesTVShowNFOAtShowRoot(t *testing.T) {
	root := t.TempDir()
	tvRoot := filepath.Join(root, "tv")
	showDir := filepath.Join(tvRoot, "Severance (2022)")
	seasonDir := filepath.Join(showDir, "Season 01")
	if err := os.MkdirAll(seasonDir, 0755); err != nil {
		t.Fatal(err)
	}
	enabled := true
	cfg := &config.Config{LocalMedia: config.LocalMediaConfig{Categories: []config.LocalMediaCategory{{
		ID: "tv", Name: "TV Shows", LibraryType: "tv", Paths: []string{tvRoot}, Enabled: &enabled,
	}}}}
	video := taterLocalVideoIndex{
		ID:         taterVideoMediaID("tv", 0, "show", "Severance (2022)"),
		CategoryID: "tv", LibraryType: "tv", MediaType: "show", SourceIndex: 0,
		Path: "Severance (2022)", Title: "Severance", Year: "2022",
	}
	written, err := writeTaterVideoNFO(cfg, &video, taterTMDBVideoDetails{
		ID: 95396, Name: "Severance", OriginalName: "Severance", Overview: "Employees undergo a severance procedure.",
		FirstAirDate: "2022-02-17", EpisodeRunTime: []int{50}, VoteAverage: 8.4,
		Genres:      []taterTMDBNamedValue{{ID: 18, Name: "Drama"}},
		ExternalIDs: taterTMDBExternalIDs{IMDbID: "tt11280740", TVDBID: 371980},
		ContentRatings: taterTMDBContentRatings{Results: []struct {
			Country string `json:"iso_3166_1"`
			Rating  string `json:"rating"`
		}{{Country: "US", Rating: "TV-MA"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !written || !video.HasMetadata {
		t.Fatalf("TV metadata was not created: %#v", video)
	}
	nfoPath := filepath.Join(showDir, "tvshow.nfo")
	raw, err := os.ReadFile(nfoPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"<tvshow>", "<title>Severance</title>", "<mpaa>TV-MA</mpaa>", "<tvdbid>371980</tvdbid>", "<tmdbid>95396</tmdbid>"} {
		if !strings.Contains(string(raw), expected) {
			t.Fatalf("TV NFO did not contain %q:\n%s", expected, raw)
		}
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
		case "/3/movie/329865":
			if request.URL.Query().Get("append_to_response") != "credits,external_ids,release_dates" {
				http.Error(response, "missing appended metadata", http.StatusBadRequest)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{
				"id":329865,"title":"Arrival","original_title":"Arrival","overview":"A linguist works with the military to communicate with alien lifeforms.",
				"tagline":"Why are they here?","release_date":"2016-11-10","runtime":116,"vote_average":7.6,"poster_path":"/arrival.jpg","imdb_id":"tt2543164",
				"genres":[{"id":18,"name":"Drama"},{"id":878,"name":"Science Fiction"}],
				"production_companies":[{"id":4,"name":"FilmNation Entertainment"}],
				"production_countries":[{"iso_3166_1":"US","name":"United States of America"}],
				"credits":{"cast":[{"id":9273,"name":"Amy Adams","character":"Louise Banks","order":0}],"crew":[{"id":137427,"name":"Denis Villeneuve","job":"Director"}]},
				"external_ids":{"imdb_id":"tt2543164"},
				"release_dates":{"results":[{"iso_3166_1":"US","release_dates":[{"certification":"PG-13","type":3}]}]}
			}`))
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
	nfoPath := filepath.Join(movieDir, "Arrival (2016).nfo")
	nfoRaw, err := os.ReadFile(nfoPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"<movie>", "<title>Arrival</title>", "<plot>A linguist works with the military", "<imdbid>tt2543164</imdbid>",
		"<tmdbid>329865</tmdbid>", "<genre>Science Fiction</genre>", "<mpaa>PG-13</mpaa>", "<name>Amy Adams</name>",
	} {
		if !strings.Contains(string(nfoRaw), expected) {
			t.Fatalf("generated NFO did not contain %q:\n%s", expected, nfoRaw)
		}
	}
	if !index.Videos[0].HasMetadata || index.Videos[0].IMDbID != "tt2543164" || index.Videos[0].Description == "" {
		t.Fatalf("generated NFO metadata was not applied to the index: %#v", index.Videos[0])
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

func TestTaterTMDBMetadataCanResolveTVDBIDWithoutPoster(t *testing.T) {
	oldBaseURL := taterTMDBBaseURL
	oldClient := taterTMDBHTTPClient
	t.Cleanup(func() {
		taterTMDBBaseURL = oldBaseURL
		taterTMDBHTTPClient = oldClient
	})

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/3/find/371980" || request.URL.Query().Get("external_source") != "tvdb_id" {
			http.Error(response, "invalid TVDB lookup", http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"movie_results":[],"tv_results":[{"id":95396,"name":"Severance","first_air_date":"2022-02-17"}]}`))
	}))
	defer server.Close()
	taterTMDBBaseURL = server.URL + "/3"
	taterTMDBHTTPClient = server.Client()

	enabled := true
	cfg := &config.Config{LocalMedia: config.LocalMediaConfig{TMDBEnabled: &enabled, TMDBAPIKey: "tmdb-test-key"}}
	candidate, err := findTaterRemoteVideoCandidateByExternalID(
		context.Background(),
		cfg,
		taterLocalVideoIndex{LibraryType: "tv", MediaType: "show"},
		"371980",
	)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.TMDBID != 95396 || candidate.Title != "Severance" || candidate.PosterPath != "" {
		t.Fatalf("unexpected TVDB metadata result: %#v", candidate)
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
