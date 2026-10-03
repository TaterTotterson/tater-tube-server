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

func TestTaterFreshLocalLibraryIndexCacheRefreshesAfterAtomicWrite(t *testing.T) {
	root := t.TempDir()
	metadataRoot := filepath.Join(root, "metadata")
	enabled := true
	cfg := config.DefaultConfig(root)
	cfg.Metadata.RootPath = metadataRoot
	cfg.LocalMedia.Enabled = &enabled
	cfg.LocalMedia.Categories = []config.LocalMediaCategory{{
		ID: "movies", Name: "Movies", LibraryType: "movies",
		Paths: []string{filepath.Join(root, "movies")}, Enabled: &enabled,
	}}
	fingerprint := taterLocalLibraryFingerprint(cfg)

	first := taterLocalLibraryIndex{
		Schema: taterLocalLibraryIndexSchema, ConfigFingerprint: fingerprint,
		Files: []taterLocalLibraryFileIndex{{CategoryID: "movies", Path: "First.mkv"}},
	}
	if err := writeTaterJSON(taterLocalLibraryIndexPath(cfg), first); err != nil {
		t.Fatal(err)
	}
	loaded, ok := taterFreshLocalLibraryIndex(cfg)
	if !ok || len(loaded.Files) != 1 || loaded.Files[0].Path != "First.mkv" {
		t.Fatalf("unexpected first cached index: %#v", loaded.Files)
	}

	second := first
	second.Files = []taterLocalLibraryFileIndex{{CategoryID: "movies", Path: "Second.mkv"}}
	if err := writeTaterJSON(taterLocalLibraryIndexPath(cfg), second); err != nil {
		t.Fatal(err)
	}
	loaded, ok = taterFreshLocalLibraryIndex(cfg)
	if !ok || len(loaded.Files) != 1 || loaded.Files[0].Path != "Second.mkv" {
		t.Fatalf("cache did not refresh after index replacement: %#v", loaded.Files)
	}
}

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
case "$*" in
  *default=noprint_wrappers*)
    printf '5400.000\n'
    exit 0
    ;;
esac
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

	progressEvents := []taterLocalLibraryScanProgress{}
	first, err := scanTaterLocalLibrary(
		context.Background(),
		cfg,
		taterLocalLibraryIndex{},
		func(progress taterLocalLibraryScanProgress) {
			progressEvents = append(progressEvents, progress)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Files) != 3 || len(first.Albums) != 1 {
		t.Fatalf("unexpected local index: files=%d albums=%d", len(first.Files), len(first.Albums))
	}
	sawCompleteScanProgress := false
	sawCompleteDurationProgress := false
	for _, progress := range progressEvents {
		if progress.Phase == "scanning" && progress.Current == 3 && progress.Total == 3 {
			sawCompleteScanProgress = true
		}
		if progress.Phase == "durations" && progress.Current == 2 && progress.Total == 2 && progress.FilesTotal == 3 {
			sawCompleteDurationProgress = true
		}
	}
	if !sawCompleteScanProgress || !sawCompleteDurationProgress {
		t.Fatalf("expected determinate scan and duration progress, got %#v", progressEvents)
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
	for _, file := range first.Files {
		if file.LibraryType != "music" && file.DurationSeconds != 5400 {
			t.Fatalf("expected indexed video duration, got %#v", file)
		}
	}
	countBefore, err := os.ReadFile(probeCount)
	if err != nil || len(countBefore) != 3 {
		t.Fatalf("expected three ffprobe calls, count=%q error=%v", countBefore, err)
	}
	for i := range first.Files {
		if first.Files[i].LibraryType != "music" {
			first.Files[i].DurationSeconds = 0
			break
		}
	}

	taterLocalMusicMetadataCache.Lock()
	taterLocalMusicMetadataCache.Items = map[string]taterLocalMusicMetadataCacheEntry{}
	taterLocalMusicMetadataCache.Unlock()
	second, err := scanTaterLocalLibrary(context.Background(), cfg, first, nil)
	if err != nil {
		t.Fatal(err)
	}
	countAfter, err := os.ReadFile(probeCount)
	if err != nil || len(countAfter) != 4 {
		t.Fatalf("missing video duration should be backfilled without re-probing other media, count=%q error=%v", countAfter, err)
	}
	for _, file := range second.Files {
		if file.LibraryType != "music" && file.DurationSeconds != 5400 {
			t.Fatalf("expected indexed video duration after backfill, got %#v", file)
		}
	}
	if err := writeTaterJSON(taterLocalLibraryIndexPath(cfg), second); err != nil {
		t.Fatal(err)
	}
	if needed, reason := taterLocalLibraryIndexNeedsMaintenance(cfg); needed {
		t.Fatalf("completed index should not need maintenance: %s", reason)
	}
	second.VideoDurationsScanned = false
	if err := writeTaterJSON(taterLocalLibraryIndexPath(cfg), second); err != nil {
		t.Fatal(err)
	}
	if needed, reason := taterLocalLibraryIndexNeedsMaintenance(cfg); !needed || reason != "Backfilling video durations" {
		t.Fatalf("durationless video should need maintenance, needed=%v reason=%q", needed, reason)
	}
	second.VideoDurationsScanned = true
	if err := writeTaterJSON(taterLocalLibraryIndexPath(cfg), second); err != nil {
		t.Fatal(err)
	}
	loaded, err := readTaterLocalLibraryIndex(cfg)
	if err != nil || len(loaded.Albums) != 1 || loaded.ConfigFingerprint != taterLocalLibraryFingerprint(cfg) {
		t.Fatalf("persistent index did not round-trip: %#v error=%v", loaded, err)
	}
}

func TestTaterLocalLibraryIncrementalScanOnlyProcessesChanges(t *testing.T) {
	root := t.TempDir()
	metadataRoot := filepath.Join(root, "metadata")
	movieRoot := filepath.Join(root, "movies")
	binDir := filepath.Join(root, "bin")
	for _, dir := range []string{metadataRoot, movieRoot, binDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	probeCount := filepath.Join(root, "probe-count")
	ffmpegPath := filepath.Join(binDir, "ffmpeg")
	ffprobePath := filepath.Join(binDir, "ffprobe")
	if err := os.WriteFile(ffmpegPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	probeScript := fmt.Sprintf("#!/bin/sh\nprintf x >> %q\nprintf '3600.000\\n'\n", probeCount)
	if err := os.WriteFile(ffprobePath, []byte(probeScript), 0755); err != nil {
		t.Fatal(err)
	}

	firstPath := filepath.Join(movieRoot, "First.2025.mkv")
	if err := os.WriteFile(firstPath, []byte("first"), 0644); err != nil {
		t.Fatal(err)
	}
	enabled := true
	cfg := &config.Config{
		Metadata:    config.MetadataConfig{RootPath: metadataRoot},
		Transcoding: config.TranscodingConfig{FFmpegPath: ffmpegPath},
		LocalMedia: config.LocalMediaConfig{
			Enabled: &enabled,
			Categories: []config.LocalMediaCategory{{
				ID: "movies", Name: "Movies", LibraryType: "movies",
				Paths: []string{movieRoot}, Enabled: &enabled,
			}},
		},
	}

	first, err := scanTaterLocalLibrary(context.Background(), cfg, taterLocalLibraryIndex{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Files) != 1 {
		t.Fatalf("first scan files = %d, want 1", len(first.Files))
	}

	progressEvents := []taterLocalLibraryScanProgress{}
	second, err := scanTaterLocalLibraryWithOptions(
		context.Background(), cfg, first,
		func(progress taterLocalLibraryScanProgress) { progressEvents = append(progressEvents, progress) },
		taterLocalLibraryScanOptions{Incremental: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Files) != 1 {
		t.Fatalf("unchanged incremental scan files = %d, want 1", len(second.Files))
	}
	for _, progress := range progressEvents {
		if progress.Phase == "discovering" {
			t.Fatal("incremental scan performed the full pre-count walk")
		}
	}
	if count, err := os.ReadFile(probeCount); err != nil || len(count) != 1 {
		t.Fatalf("unchanged file was probed again, count=%q error=%v", count, err)
	}

	secondPath := filepath.Join(movieRoot, "Second.2026.mkv")
	if err := os.WriteFile(secondPath, []byte("second"), 0644); err != nil {
		t.Fatal(err)
	}
	third, err := scanTaterLocalLibraryWithOptions(
		context.Background(), cfg, second, nil, taterLocalLibraryScanOptions{Incremental: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(third.Files) != 2 {
		t.Fatalf("incremental addition files = %d, want 2", len(third.Files))
	}
	if count, err := os.ReadFile(probeCount); err != nil || len(count) != 2 {
		t.Fatalf("new file was not the only additional probe, count=%q error=%v", count, err)
	}

	if err := os.Remove(firstPath); err != nil {
		t.Fatal(err)
	}
	fourth, err := scanTaterLocalLibraryWithOptions(
		context.Background(), cfg, third, nil, taterLocalLibraryScanOptions{Incremental: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(fourth.Files) != 1 || fourth.Files[0].Path != filepath.Base(secondPath) {
		t.Fatalf("incremental removal did not reconcile index: %#v", fourth.Files)
	}
}

func TestTaterLocalLibraryAutoScanSettings(t *testing.T) {
	enabled := true
	disabled := false
	cfg := &config.Config{LocalMedia: config.LocalMediaConfig{
		Enabled: &enabled,
		Categories: []config.LocalMediaCategory{{
			ID: "movies", Name: "Movies", Paths: []string{"/media"}, Enabled: &enabled,
		}},
	}}
	if !taterLocalLibraryAutoScanEnabled(cfg) {
		t.Fatal("nil auto-scan setting should default to enabled")
	}
	if !taterLocalLibraryRealtimeMonitoringEnabled(cfg) {
		t.Fatal("nil real-time monitoring setting should default to enabled")
	}
	if got := taterLocalLibraryAutoScanInterval(cfg); got != 15*time.Minute {
		t.Fatalf("default interval = %s, want 15m", got)
	}
	cfg.LocalMedia.AutoScanEnabled = &disabled
	if taterLocalLibraryAutoScanEnabled(cfg) {
		t.Fatal("explicitly disabled auto scan should remain disabled")
	}
	cfg.LocalMedia.AutoScanIntervalMinutes = 1
	if got := taterLocalLibraryAutoScanInterval(cfg); got != 5*time.Minute {
		t.Fatalf("minimum interval = %s, want 5m", got)
	}
	cfg.LocalMedia.RealtimeMonitoringEnabled = &disabled
	if taterLocalLibraryRealtimeMonitoringEnabled(cfg) {
		t.Fatal("explicitly disabled real-time monitoring should remain disabled")
	}
}

func TestTaterLocalLibraryWatcherReportsPerPathFallback(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "Show", "Season 01")
	hidden := filepath.Join(root, ".incoming")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(hidden, 0755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "missing")
	enabled := true
	cfg := &config.Config{LocalMedia: config.LocalMediaConfig{
		Enabled:                   &enabled,
		RealtimeMonitoringEnabled: &enabled,
		AutoScanEnabled:           &enabled,
		Categories: []config.LocalMediaCategory{
			{ID: "tv", Name: "TV", LibraryType: "tv", Paths: []string{root}, Enabled: &enabled},
			{ID: "movies", Name: "Movies", LibraryType: "movies", Paths: []string{missing}, Enabled: &enabled},
		},
	}}

	watcher, roots, _, status, needsRetry := configureTaterLocalLibraryWatcher(cfg)
	if watcher != nil {
		defer watcher.Close()
	}
	if !status.Active || status.State != "watching" {
		t.Fatalf("monitor status = %#v, want active watcher", status)
	}
	if status.WatchedDirectories != 3 {
		t.Fatalf("watched directories = %d, want root plus two visible descendants", status.WatchedDirectories)
	}
	if !needsRetry {
		t.Fatal("missing library path should schedule a watcher retry")
	}
	states := map[string]string{}
	for _, pathStatus := range status.Paths {
		states[pathStatus.CategoryID] = pathStatus.State
	}
	if states["tv"] != "watching" || states["movies"] != "unavailable" {
		t.Fatalf("unexpected path states: %#v", states)
	}
	if got := taterLocalLibraryEventCategories(filepath.Join(nested, "Episode.mkv"), roots); len(got) != 1 || got[0] != "tv" {
		t.Fatalf("event categories = %#v, want tv", got)
	}
	if got := taterLocalLibraryEventCategories(filepath.Join(hidden, "partial.mkv"), roots); len(got) != 0 {
		t.Fatalf("hidden event categories = %#v, want none", got)
	}
}

func TestTaterLocalLibraryTargetedScanPreservesOtherLibraries(t *testing.T) {
	root := t.TempDir()
	metadataRoot := filepath.Join(root, "metadata")
	movieRoot := filepath.Join(root, "movies")
	tvRoot := filepath.Join(root, "tv")
	binDir := filepath.Join(root, "bin")
	for _, dir := range []string{metadataRoot, movieRoot, tvRoot, binDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	firstMovie := filepath.Join(movieRoot, "First.mkv")
	firstEpisode := filepath.Join(tvRoot, "Show", "Season 01", "Show.S01E01.mkv")
	if err := os.MkdirAll(filepath.Dir(firstEpisode), 0755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{firstMovie, firstEpisode} {
		if err := os.WriteFile(path, []byte("video"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	probeCount := filepath.Join(root, "probe-count")
	ffmpegPath := filepath.Join(binDir, "ffmpeg")
	ffprobePath := filepath.Join(binDir, "ffprobe")
	if err := os.WriteFile(ffmpegPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	probeScript := fmt.Sprintf("#!/bin/sh\nprintf x >> %q\nprintf '120.000\\n'\n", probeCount)
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
			},
		},
	}

	first, err := scanTaterLocalLibrary(context.Background(), cfg, taterLocalLibraryIndex{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Files) != 2 {
		t.Fatalf("initial files = %d, want 2", len(first.Files))
	}
	if err := os.RemoveAll(tvRoot); err != nil {
		t.Fatal(err)
	}
	secondMovie := filepath.Join(movieRoot, "Second.mkv")
	if err := os.WriteFile(secondMovie, []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}

	targeted, err := scanTaterLocalLibraryWithOptions(
		context.Background(), cfg, first, nil,
		taterLocalLibraryScanOptions{
			Incremental: true,
			CategoryIDs: map[string]struct{}{"movies": {}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(targeted.Files) != 3 {
		t.Fatalf("targeted files = %d, want two movies plus preserved TV item", len(targeted.Files))
	}
	if count, err := os.ReadFile(probeCount); err != nil || len(count) != 3 {
		t.Fatalf("targeted scan should probe only the new movie, count=%q error=%v", count, err)
	}

	full, err := scanTaterLocalLibraryWithOptions(
		context.Background(), cfg, targeted, nil, taterLocalLibraryScanOptions{Incremental: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Files) != 2 {
		t.Fatalf("full reconciliation files = %d, want 2 movies", len(full.Files))
	}
}

func TestTaterLocalLibraryMissingAttentionIncludesArtworkAndMetadata(t *testing.T) {
	if taterLocalMusicAlbumNeedsAttention(taterLocalMusicAlbumIndex{
		HasArtwork: true, MetadataAvailable: true, HasMetadata: true,
		ArtistMetadataAvailable: true, HasArtistMetadata: true,
	}) {
		t.Fatal("complete music album should not need attention")
	}
	if !taterLocalMusicAlbumNeedsAttention(taterLocalMusicAlbumIndex{
		HasArtwork: true, MetadataAvailable: true, HasMetadata: false,
	}) {
		t.Fatal("music album missing metadata should need attention")
	}
	if !taterLocalVideoNeedsAttention(taterLocalVideoIndex{HasArtwork: false, HasMetadata: true}) {
		t.Fatal("video missing artwork should need attention")
	}
	if taterLocalVideoNeedsAttention(taterLocalVideoIndex{HasArtwork: true, HasMetadata: true}) {
		t.Fatal("complete video should not need attention")
	}

	status := taterLocalLibraryScanStatus{}
	setTaterLocalLibraryProgress(&status, 37, 100)
	if status.ProgressCurrent != 37 || status.ProgressTotal != 100 || status.ProgressPercent != 37 {
		t.Fatalf("unexpected progress calculation: %#v", status)
	}
	setTaterLocalLibraryProgress(&status, 100, 100)
	if status.ProgressPercent != 99 {
		t.Fatalf("running work should reserve 100 percent for completion: %#v", status)
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

func TestTaterAlbumMetadataScraperFallsBackToArtistGenresAndReportsProgress(t *testing.T) {
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
			_, _ = response.Write([]byte(`{"release-groups":[{"id":"release-group-1","title":"Collecting Dust","score":100,"artist-credit":[{"name":"Surfer Girl","artist":{"id":"artist-1","name":"Surfer Girl"}}]}]}`))
		case "/ws/2/release-group/release-group-1":
			_, _ = response.Write([]byte(`{"id":"release-group-1","genres":[]}`))
		case "/ws/2/artist/artist-1":
			_, _ = response.Write([]byte(`{"id":"artist-1","genres":[{"name":"reggae","count":20},{"name":"indie rock","count":3}]}`))
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
		Albums: []taterLocalMusicAlbumIndex{{
			ID: "album:artist-fallback", CategoryID: "music", Title: "Collecting Dust",
			Artist: "Surfer Girl", HasArtwork: true, ArtworkSource: "embedded",
		}},
	}
	latest := taterMusicEnrichmentProgress{}
	if err := scrapeTaterMissingAlbumArtwork(
		context.Background(), cfg, &index,
		func(progress taterMusicEnrichmentProgress) { latest = progress },
	); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(index.Albums[0].Genres, "|"); got != "Reggae|indie rock|Alternative|Rock" {
		t.Fatalf("artist genres were not applied: %q", got)
	}
	if latest.AlbumsProcessed != 1 || latest.GenreMatches != 1 || latest.GenreUnmatched != 0 {
		t.Fatalf("unexpected enrichment progress: %#v", latest)
	}
}

func TestTaterAlbumMetadataScraperUsesExactArtistSearchWhenReleaseIsMissing(t *testing.T) {
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
			_, _ = response.Write([]byte(`{"release-groups":[]}`))
		case "/ws/2/artist/":
			_, _ = response.Write([]byte(`{"artists":[{"id":"artist-1","name":"The Elovaters","score":100},{"id":"artist-2","name":"Elovater Tribute","score":100}]}`))
		case "/ws/2/artist/artist-1":
			_, _ = response.Write([]byte(`{"id":"artist-1","genres":[{"name":"roots reggae","count":12}]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	taterMusicBrainzBaseURL = server.URL + "/ws/2"
	taterMusicArtworkHTTPClient = server.Client()
	taterMusicBrainzRequestPacing = 0
	cfg := &config.Config{Metadata: config.MetadataConfig{RootPath: t.TempDir()}}
	album := taterLocalMusicAlbumIndex{
		ID: "album:artist-search", CategoryID: "music", Title: "Endless Summer",
		Artist: "The Elovaters", HasArtwork: true, ArtworkSource: "embedded",
	}
	if err := refreshTaterAlbumGenres(context.Background(), cfg, &album); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(album.Genres, "|"); got != "roots reggae|Reggae" {
		t.Fatalf("exact artist fallback genres were not applied: %q", got)
	}
	if album.MusicBrainzID != "" {
		t.Fatalf("artist fallback should not be stored as a release-group ID: %q", album.MusicBrainzID)
	}
}

func TestTaterAlbumMetadataScraperUsesAudioDBAfterMusicBrainz(t *testing.T) {
	oldMusicBrainzURL := taterMusicBrainzBaseURL
	oldCoverArtURL := taterCoverArtArchiveBaseURL
	oldAudioDBURL := taterAudioDBBaseURL
	oldClient := taterMusicArtworkHTTPClient
	oldMusicBrainzPacing := taterMusicBrainzRequestPacing
	oldAudioDBPacing := taterAudioDBRequestPacing
	t.Cleanup(func() {
		taterMusicBrainzBaseURL = oldMusicBrainzURL
		taterCoverArtArchiveBaseURL = oldCoverArtURL
		taterAudioDBBaseURL = oldAudioDBURL
		taterMusicArtworkHTTPClient = oldClient
		taterMusicBrainzRequestPacing = oldMusicBrainzPacing
		taterAudioDBRequestPacing = oldAudioDBPacing
		taterMusicBrainzPacer.Lock()
		taterMusicBrainzPacer.LastRequest = time.Time{}
		taterMusicBrainzPacer.Unlock()
		taterAudioDBPacer.Lock()
		taterAudioDBPacer.LastRequest = time.Time{}
		taterAudioDBPacer.Unlock()
	})

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/ws/2/release-group/":
			_, _ = response.Write([]byte(`{"release-groups":[{"id":"release-group-1","title":"Island Album","score":100,"artist-credit":[{"name":"Island Artist","artist":{"id":"artist-1","name":"Island Artist"}}]}]}`))
		case "/ws/2/release-group/release-group-1":
			_, _ = response.Write([]byte(`{"id":"release-group-1","genres":[]}`))
		case "/release-group/release-group-1":
			http.NotFound(response, request)
		case "/audiodb/test-key/album-mb.php":
			if request.URL.Query().Get("i") != "release-group-1" {
				http.Error(response, "unexpected TheAudioDB release ID", http.StatusBadRequest)
				return
			}
			_, _ = response.Write([]byte(fmt.Sprintf(`{"album":[{"idAlbum":"1","strAlbum":"Island Album","strArtist":"Island Artist","strGenre":"Reggae","strStyle":"Dub / Pop","strAlbumThumb":%q,"strMusicBrainzID":"release-group-1"}]}`, server.URL+"/audiodb-cover.jpg")))
		case "/audiodb-cover.jpg":
			response.Header().Set("Content-Type", "image/jpeg")
			_, _ = response.Write([]byte("\xff\xd8audiodb-cover\xff\xd9"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	taterMusicBrainzBaseURL = server.URL + "/ws/2"
	taterCoverArtArchiveBaseURL = server.URL
	taterAudioDBBaseURL = server.URL + "/audiodb"
	taterMusicArtworkHTTPClient = server.Client()
	taterMusicBrainzRequestPacing = 0
	taterAudioDBRequestPacing = 0
	enabled := true
	cfg := &config.Config{
		Metadata: config.MetadataConfig{RootPath: t.TempDir()},
		LocalMedia: config.LocalMediaConfig{
			AudioDBEnabled: &enabled,
			AudioDBAPIKey:  "test-key",
		},
	}
	index := taterLocalLibraryIndex{
		Categories: []taterLocalLibraryCategoryIndex{{ID: "music", LibraryType: "music"}},
		Albums: []taterLocalMusicAlbumIndex{{
			ID: "album:audiodb", CategoryID: "music", Title: "Island Album", Artist: "Island Artist",
		}},
	}

	if err := refreshTaterAlbumArtwork(context.Background(), cfg, &index, &index.Albums[0], false); err != nil {
		t.Fatal(err)
	}
	album := index.Albums[0]
	if !album.HasArtwork || album.MusicBrainzID != "release-group-1" {
		t.Fatalf("unexpected TheAudioDB artwork result: %#v", album)
	}
	if got := strings.Join(album.Genres, "|"); got != "Reggae|Pop" {
		t.Fatalf("unexpected TheAudioDB genres: %q", got)
	}
}

func TestTaterAlbumMetadataScraperUsesExactArtistConsensusLast(t *testing.T) {
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
			_, _ = response.Write([]byte(`{"release-groups":[]}`))
		case "/ws/2/artist/":
			_, _ = response.Write([]byte(`{"artists":[]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	taterMusicBrainzBaseURL = server.URL + "/ws/2"
	taterMusicArtworkHTTPClient = server.Client()
	taterMusicBrainzRequestPacing = 0
	audioDBEnabled := false
	cfg := &config.Config{
		Metadata:   config.MetadataConfig{RootPath: t.TempDir()},
		LocalMedia: config.LocalMediaConfig{AudioDBEnabled: &audioDBEnabled},
	}
	index := taterLocalLibraryIndex{Albums: []taterLocalMusicAlbumIndex{
		{ID: "album:known", Title: "Known", Artist: "Roots Band", Genres: []string{"Roots Reggae"}, HasArtwork: true},
		{ID: "album:missing", Title: "Missing", Artist: "Roots Band", HasArtwork: true},
		{ID: "album:various-known", Title: "Compilation One", Artist: "Various Artists", Genres: []string{"Rock"}, HasArtwork: true},
		{ID: "album:various-missing", Title: "Compilation Two", Artist: "Various Artists", HasArtwork: true},
	}}
	latest := taterMusicEnrichmentProgress{}
	if err := scrapeTaterMissingAlbumArtwork(
		context.Background(), cfg, &index,
		func(progress taterMusicEnrichmentProgress) { latest = progress },
	); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(index.Albums[1].Genres, "|"); got != "Reggae" {
		t.Fatalf("same-artist broad genre was not applied: %q", got)
	}
	if len(index.Albums[3].Genres) != 0 {
		t.Fatalf("generic compilation artist should not inherit genres: %#v", index.Albums[3].Genres)
	}
	if latest.AlbumsProcessed != 2 || latest.GenreMatches != 1 || latest.GenreUnmatched != 1 {
		t.Fatalf("unexpected consensus progress: %#v", latest)
	}
}

func TestTaterMusicSimplifiedAlbumTitleOnlyRemovesEditionSuffixes(t *testing.T) {
	for input, expected := range map[string]string{
		"World on Fire (Deluxe Version)": "World on Fire",
		"Set in Stone [Instrumentals]":   "Set in Stone",
		"Live at Red Rocks":              "Live at Red Rocks",
		"Album (Part 2)":                 "Album (Part 2)",
	} {
		if got := taterMusicSimplifiedAlbumTitle(input); got != expected {
			t.Fatalf("simplified title %q = %q, want %q", input, got, expected)
		}
	}
}

func TestTaterMusicGenreMatchingRelaxesEditionSuffixWithoutRelaxingArtwork(t *testing.T) {
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
		query := request.URL.Query().Get("query")
		if request.URL.Path != "/ws/2/release-group/" {
			http.NotFound(response, request)
			return
		}
		if strings.Contains(query, `releasegroup:"World on Fire"`) {
			_, _ = response.Write([]byte(`{"release-groups":[{"id":"release-group-1","title":"World on Fire","score":100,"artist-credit":[{"name":"Stick Figure","artist":{"id":"artist-1","name":"Stick Figure"}}]}]}`))
			return
		}
		_, _ = response.Write([]byte(`{"release-groups":[]}`))
	}))
	defer server.Close()

	taterMusicBrainzBaseURL = server.URL + "/ws/2"
	taterMusicArtworkHTTPClient = server.Client()
	taterMusicBrainzRequestPacing = 0
	album := taterLocalMusicAlbumIndex{Title: "World on Fire (Deluxe Version)", Artist: "Stick Figure"}
	genreCandidates, err := searchTaterMusicGenreCandidates(context.Background(), album)
	if err != nil || len(genreCandidates) != 1 || genreCandidates[0].MusicBrainzID != "release-group-1" {
		t.Fatalf("genre matching did not use the simplified edition title: %#v error=%v", genreCandidates, err)
	}
	artworkCandidates, err := searchTaterMusicArtworkCandidates(context.Background(), album)
	if err != nil || len(artworkCandidates) != 0 {
		t.Fatalf("artwork matching should remain exact: %#v error=%v", artworkCandidates, err)
	}
}
