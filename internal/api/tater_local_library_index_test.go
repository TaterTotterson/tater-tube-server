package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

func TestTaterLocalLibraryIndexBuildsStatsArtworkAndReusesUnchangedMusicMetadata(t *testing.T) {
	root := t.TempDir()
	metadataRoot := filepath.Join(root, "metadata")
	musicRoot := filepath.Join(root, "music")
	movieRoot := filepath.Join(root, "movies")
	tvRoot := filepath.Join(root, "tv")
	albumDir := filepath.Join(musicRoot, "Bob Marley", "Exodus")
	seasonDir := filepath.Join(tvRoot, "Some Show", "Season 01")
	for _, dir := range []string{metadataRoot, albumDir, movieRoot, seasonDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	trackPath := filepath.Join(albumDir, "09 Three Little Birds.flac")
	for path, contents := range map[string]string{
		trackPath:                                   "audio",
		filepath.Join(albumDir, "cover.jpg"):        "cover",
		filepath.Join(movieRoot, "Movie.2024.mkv"):  "movie",
		filepath.Join(seasonDir, "Show.S01E01.mkv"): "episode",
	} {
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	probeCount := filepath.Join(root, "probe-count")
	ffmpegPath := filepath.Join(binDir, "ffmpeg")
	ffprobePath := filepath.Join(binDir, "ffprobe")
	if err := os.WriteFile(ffmpegPath, []byte("#!/bin/sh\nprintf '\\377\\330art\\377\\331'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	probeScript := fmt.Sprintf(`#!/bin/sh
printf x >> %q
cat <<'EOF'
{"format":{"duration":"180","tags":{"title":"Three Little Birds","artist":"Bob Marley & The Wailers","album_artist":"Bob Marley & The Wailers","album":"Exodus","genre":"Reggae","track":"9","date":"1977"}},"streams":[]}
EOF
`, probeCount)
	if err := os.WriteFile(ffprobePath, []byte(probeScript), 0755); err != nil {
		t.Fatal(err)
	}

	enabled := true
	cfg := &config.Config{
		Metadata:    config.MetadataConfig{RootPath: metadataRoot},
		Transcoding: config.TranscodingConfig{FFmpegPath: ffmpegPath},
		LocalMedia: config.LocalMediaConfig{
			Enabled: &enabled,
			Categories: []config.LocalMediaCategory{
				{ID: "movies", Name: "Movies", LibraryType: "movies", Paths: []string{movieRoot}, Enabled: &enabled},
				{ID: "tv", Name: "TV", LibraryType: "tv", Paths: []string{tvRoot}, Enabled: &enabled},
				{ID: "music", Name: "Music", LibraryType: "music", Paths: []string{musicRoot}, Enabled: &enabled},
			},
		},
	}

	first, err := scanTaterLocalLibrary(context.Background(), cfg, taterLocalLibraryIndex{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Files) != 3 || len(first.Albums) != 1 {
		t.Fatalf("unexpected local index: files=%d albums=%d", len(first.Files), len(first.Albums))
	}
	album := first.Albums[0]
	if album.Title != "Exodus" || album.Artist != "Bob Marley & The Wailers" || album.TrackCount != 1 {
		t.Fatalf("unexpected album: %#v", album)
	}
	if !album.HasArtwork || album.ArtworkSource != "local" || !strings.HasSuffix(album.ArtworkRef, "cover.jpg") {
		t.Fatalf("expected local folder artwork: %#v", album)
	}
	statsByID := map[string]taterLocalLibraryStats{}
	for _, category := range first.Categories {
		statsByID[category.ID] = category.Stats
	}
	if statsByID["movies"].Movies != 1 || statsByID["tv"].Shows != 1 || statsByID["tv"].Episodes != 1 {
		t.Fatalf("unexpected video stats: %#v", statsByID)
	}
	if statsByID["music"].Artists != 1 || statsByID["music"].Albums != 1 || statsByID["music"].Songs != 1 || statsByID["music"].Artwork != 1 {
		t.Fatalf("unexpected music stats: %#v", statsByID["music"])
	}
	countBefore, err := os.ReadFile(probeCount)
	if err != nil || len(countBefore) != 1 {
		t.Fatalf("expected one ffprobe call, count=%q error=%v", countBefore, err)
	}

	taterLocalMusicMetadataCache.Lock()
	taterLocalMusicMetadataCache.Items = map[string]taterLocalMusicMetadataCacheEntry{}
	taterLocalMusicMetadataCache.Unlock()
	second, err := scanTaterLocalLibrary(context.Background(), cfg, first, nil)
	if err != nil {
		t.Fatal(err)
	}
	countAfter, err := os.ReadFile(probeCount)
	if err != nil || len(countAfter) != 1 {
		t.Fatalf("unchanged track should reuse persistent metadata, count=%q error=%v", countAfter, err)
	}
	if err := writeTaterJSON(taterLocalLibraryIndexPath(cfg), second); err != nil {
		t.Fatal(err)
	}
	loaded, err := readTaterLocalLibraryIndex(cfg)
	if err != nil || len(loaded.Albums) != 1 || loaded.ConfigFingerprint != taterLocalLibraryFingerprint(cfg) {
		t.Fatalf("persistent index did not round-trip: %#v error=%v", loaded, err)
	}
}

func TestTaterAlbumArtworkScraperCachesConfidentMusicBrainzMatch(t *testing.T) {
	oldMusicBrainzURL := taterMusicBrainzBaseURL
	oldCoverArtURL := taterCoverArtArchiveBaseURL
	oldClient := taterMusicArtworkHTTPClient
	oldPacing := taterMusicBrainzRequestPacing
	t.Cleanup(func() {
		taterMusicBrainzBaseURL = oldMusicBrainzURL
		taterCoverArtArchiveBaseURL = oldCoverArtURL
		taterMusicArtworkHTTPClient = oldClient
		taterMusicBrainzRequestPacing = oldPacing
		taterMusicBrainzPacer.Lock()
		taterMusicBrainzPacer.LastRequest = time.Time{}
		taterMusicBrainzPacer.Unlock()
	})

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/ws/2/release-group/" && request.URL.Query().Get("query") != "":
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"release-groups":[{"id":"release-group-1","title":"Exodus","score":100,"artist-credit":[{"name":"Bob Marley & The Wailers"}]},{"id":"wrong","title":"Other Album","score":100,"artist-credit":[{"name":"Other Artist"}]}]}`))
		case request.URL.Path == "/ws/2/release-group/release-group-1":
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"id":"release-group-1","genres":[{"name":"roots reggae","count":8},{"name":"reggae","count":20}]}`))
		case request.URL.Path == "/release-group/release-group-1":
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(fmt.Sprintf(`{"images":[{"front":true,"approved":true,"thumbnails":{"500":%q}}]}`, server.URL+"/cover.jpg")))
		case request.URL.Path == "/cover.jpg":
			response.Header().Set("Content-Type", "image/jpeg")
			_, _ = response.Write([]byte("\xff\xd8cached-cover\xff\xd9"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	taterMusicBrainzBaseURL = server.URL + "/ws/2"
	taterCoverArtArchiveBaseURL = server.URL
	taterMusicArtworkHTTPClient = server.Client()
	taterMusicBrainzRequestPacing = 0

	metadataRoot := t.TempDir()
	cfg := &config.Config{Metadata: config.MetadataConfig{RootPath: metadataRoot}}
	index := taterLocalLibraryIndex{
		Categories: []taterLocalLibraryCategoryIndex{{ID: "music", LibraryType: "music"}},
		Albums: []taterLocalMusicAlbumIndex{{
			ID: "album:test", CategoryID: "music", Title: "Exodus", Artist: "Bob Marley & The Wailers",
		}},
	}
	if err := refreshTaterAlbumArtwork(context.Background(), cfg, &index, &index.Albums[0], false); err != nil {
		t.Fatal(err)
	}
	album := index.Albums[0]
	if !album.HasArtwork || album.ArtworkSource != "scraped" || album.MusicBrainzID != "release-group-1" || album.ArtworkLocked {
		t.Fatalf("unexpected scraped artwork result: %#v", album)
	}
	if strings.Join(album.Genres, "|") != "Reggae|roots reggae" {
		t.Fatalf("MusicBrainz genres were not applied: %#v", album.Genres)
	}
	path, err := safeTaterMusicArtworkCachePath(cfg, album.ArtworkRef)
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "\xff\xd8cached-cover\xff\xd9" {
		t.Fatalf("cached artwork mismatch: %q error=%v", raw, err)
	}
	store := readTaterMusicArtworkStore(cfg)
	if store.Items[album.ID].MusicBrainzID != "release-group-1" ||
		strings.Join(store.Items[album.ID].Genres, "|") != "Reggae|roots reggae" {
		t.Fatalf("artwork match was not persisted: %#v", store.Items)
	}
}

func TestTaterAlbumMetadataScraperEnrichesGenresWhenArtworkAlreadyExists(t *testing.T) {
	oldMusicBrainzURL := taterMusicBrainzBaseURL
	oldClient := taterMusicArtworkHTTPClient
	oldPacing := taterMusicBrainzRequestPacing
	t.Cleanup(func() {
		taterMusicBrainzBaseURL = oldMusicBrainzURL
		taterMusicArtworkHTTPClient = oldClient
		taterMusicBrainzRequestPacing = oldPacing
		taterMusicBrainzPacer.Lock()
		taterMusicBrainzPacer.LastRequest = time.Time{}
		taterMusicBrainzPacer.Unlock()
	})

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/ws/2/release-group/":
			_, _ = response.Write([]byte(`{"release-groups":[{"id":"release-group-1","title":"Exodus","score":100,"artist-credit":[{"name":"Bob Marley & The Wailers"}]}]}`))
		case "/ws/2/release-group/release-group-1":
			_, _ = response.Write([]byte(`{"id":"release-group-1","genres":[{"name":"dancehall","count":5}]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	taterMusicBrainzBaseURL = server.URL + "/ws/2"
	taterMusicArtworkHTTPClient = server.Client()
	taterMusicBrainzRequestPacing = 0
	cfg := &config.Config{Metadata: config.MetadataConfig{RootPath: t.TempDir()}}
	index := taterLocalLibraryIndex{
		Categories: []taterLocalLibraryCategoryIndex{{ID: "music", LibraryType: "music"}},
		Albums: []taterLocalMusicAlbumIndex{{
			ID: "album:genres", CategoryID: "music", Title: "Exodus",
			Artist: "Bob Marley & The Wailers", HasArtwork: true, ArtworkSource: "local",
		}},
	}

	if err := scrapeTaterMissingAlbumArtwork(context.Background(), cfg, &index, nil); err != nil {
		t.Fatal(err)
	}
	album := index.Albums[0]
	if !album.HasArtwork || album.ArtworkSource != "local" ||
		strings.Join(album.Genres, "|") != "dancehall|Reggae" {
		t.Fatalf("unexpected enriched album: %#v", album)
	}
}
