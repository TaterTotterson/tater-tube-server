package api

import (
	"context"
	"fmt"
	"math/rand"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

func TestTaterLocalMovieItemsScansCleanMovieRows(t *testing.T) {
	root := t.TempDir()
	movieDir := filepath.Join(root, "Big.Movie.2024.1080p")
	if err := os.MkdirAll(movieDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(movieDir, "Big.Movie.2024.1080p.mkv"),
		filepath.Join(root, "Loose.Movie.1999.720p.mp4"),
		filepath.Join(root, "notes.txt"),
	} {
		if err := os.WriteFile(path, []byte("media"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	items, err := taterLocalMovieItems(nil, config.LocalMediaCategory{ID: "movies", Name: "Movies"}, []string{root}, "http://server", "token")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 movie rows, got %d: %#v", len(items), items)
	}

	if items[0].Title != "Big Movie" || items[0].MediaType != "movie" || items[0].SizeText != "MOVIE 2024" {
		t.Fatalf("unexpected first movie row: %#v", items[0])
	}
	if items[1].Title != "Loose Movie" || items[1].MediaType != "movie" || items[1].SizeText != "MOVIE 1999" {
		t.Fatalf("unexpected second movie row: %#v", items[1])
	}
	if !strings.Contains(items[0].StreamURL, "/api/tater/local/stream") || !strings.Contains(items[0].StreamURL, "player_token=token") {
		t.Fatalf("movie stream URL was not generated correctly: %s", items[0].StreamURL)
	}
}

func TestTaterLocalMovieItemsDefaultToClientSeek(t *testing.T) {
	root := t.TempDir()
	moviePath := filepath.Join(root, "Seek.Movie.2024.mkv")
	if err := os.WriteFile(moviePath, []byte("media"), 0644); err != nil {
		t.Fatal(err)
	}

	enabled := true
	cfg := &config.Config{Transcoding: config.TranscodingConfig{Enabled: &enabled}}
	items, err := taterLocalMovieItems(cfg, config.LocalMediaCategory{ID: "movies", Name: "Movies"}, []string{root}, "http://server", "token")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SeekMode != "client" {
		t.Fatalf("expected local movie catalog to default to client seek: %#v", items)
	}
}

func TestCleanMovieTitleAndYearTrimsOpenYearParen(t *testing.T) {
	title, year := cleanMovieTitleAndYear("Some Movie (2024)")

	if title != "Some Movie" || year != "2024" {
		t.Fatalf("expected clean title/year, got title=%q year=%q", title, year)
	}
}

func TestTaterAttachLocalPlayStatesAddsResumeFields(t *testing.T) {
	cfg := config.DefaultConfig(t.TempDir())
	item := taterUsenetItem{
		Title:       "Resume Movie",
		Type:        "localFile",
		MediaType:   "movie",
		CategoryID:  "local:movies",
		SourceIndex: 0,
		Path:        "Resume.Movie.2024/Resume.Movie.2024.mkv",
	}
	taterSetLocalPlayStateIDs(&item)

	err := saveTaterPlayStateStore(cfg, taterPlayStateStore{Items: map[string]taterPlayState{
		item.PlayStateID: {
			ID:          item.PlayStateID,
			Title:       item.Title,
			MediaType:   item.MediaType,
			CategoryID:  item.CategoryID,
			SourceIndex: item.SourceIndex,
			Path:        item.Path,
			PositionMS:  90_000,
			DurationMS:  600_000,
			UpdatedAt:   time.Now().UTC(),
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	rows := taterAttachLocalPlayStates(cfg, []taterUsenetItem{item})
	if len(rows) != 1 || rows[0].ViewOffset != 90_000 || rows[0].ViewOffsetSec != 90 {
		t.Fatalf("expected resume fields on local row: %#v", rows)
	}
}

func TestTaterAttachLocalPlayStatesUsesLatestSeriesEpisode(t *testing.T) {
	cfg := config.DefaultConfig(t.TempDir())
	first := taterUsenetItem{
		Title:       "S01E01 Pilot",
		Type:        "localFile",
		MediaType:   "episode",
		CategoryID:  "local:tv",
		SourceIndex: 0,
		Path:        "Some.Show.2020/Season 01/Some.Show.S01E01.mkv",
	}
	second := first
	second.Title = "S01E02 Next"
	second.Path = "Some.Show.2020/Season 01/Some.Show.S01E02.mkv"
	taterSetLocalPlayStateIDs(&first)
	taterSetLocalPlayStateIDs(&second)

	err := saveTaterPlayStateStore(cfg, taterPlayStateStore{Items: map[string]taterPlayState{
		first.SeriesStateID: {
			ID:          first.SeriesStateID,
			SeriesID:    first.SeriesStateID,
			Title:       second.Title,
			SeriesTitle: "Some Show",
			MediaType:   "episode",
			CategoryID:  second.CategoryID,
			SourceIndex: second.SourceIndex,
			Path:        second.Path,
			PositionMS:  120_000,
			DurationMS:  1_800_000,
			UpdatedAt:   time.Now().UTC(),
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	rows := taterAttachLocalPlayStates(cfg, []taterUsenetItem{first, second})
	if rows[0].ViewOffset != 0 {
		t.Fatalf("old episode should not inherit latest series resume state: %#v", rows[0])
	}
	if rows[1].ViewOffset != 120_000 || rows[1].SeriesStateID == "" {
		t.Fatalf("latest episode should get series resume state: %#v", rows[1])
	}
}

func TestTaterContinueDisplayStateAdvancesCompletedEpisode(t *testing.T) {
	root := t.TempDir()
	seasonDir := filepath.Join(root, "Some.Show.2020", "Season 01")
	if err := os.MkdirAll(seasonDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"Some.Show.S01E01.Pilot.mkv",
		"Some.Show.S01E02.Next.mkv",
	} {
		if err := os.WriteFile(filepath.Join(seasonDir, name), []byte("media"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.Config{LocalMedia: config.LocalMediaConfig{
		Enabled: boolPtr(true),
		Categories: []config.LocalMediaCategory{{
			ID:          "tv",
			Name:        "TV",
			LibraryType: "tv",
			Paths:       []string{root},
			Enabled:     boolPtr(true),
		}},
	}}
	state := taterPlayState{
		ID:          taterLocalSeriesStateID("local:tv", 0, "Some.Show.2020/Season 01/Some.Show.S01E01.Pilot.mkv"),
		SeriesID:    taterLocalSeriesStateID("local:tv", 0, "Some.Show.2020/Season 01/Some.Show.S01E01.Pilot.mkv"),
		Title:       "S01E01 Pilot",
		SeriesTitle: "Some Show",
		MediaType:   "episode",
		CategoryID:  "local:tv",
		SourceIndex: 0,
		Path:        "Some.Show.2020/Season 01/Some.Show.S01E01.Pilot.mkv",
		PositionMS:  1_790_000,
		DurationMS:  1_800_000,
		Completed:   true,
		UpdatedAt:   time.Now().UTC(),
	}

	next, ok := taterContinueDisplayState(cfg, state)
	if !ok {
		t.Fatal("expected completed episode to advance to next episode")
	}
	if !strings.Contains(next.Path, "S01E02") || next.PositionMS != 0 || next.Completed {
		t.Fatalf("expected next episode at start, got %#v", next)
	}
}

func TestTaterLocalTVItemsBrowsesShowsSeasonsEpisodes(t *testing.T) {
	root := t.TempDir()
	seasonDir := filepath.Join(root, "Some.Show.2020", "Season 01")
	if err := os.MkdirAll(seasonDir, 0755); err != nil {
		t.Fatal(err)
	}
	episodePath := filepath.Join(seasonDir, "Some.Show.S01E02.The.One.1080p.mkv")
	if err := os.WriteFile(episodePath, []byte("media"), 0644); err != nil {
		t.Fatal(err)
	}

	cat := config.LocalMediaCategory{ID: "tv", Name: "TV"}
	shows, err := taterLocalTVItems(nil, cat, []string{root}, "http://server", "token", -1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(shows) != 1 || shows[0].Title != "Some Show" || shows[0].MediaType != "show" {
		t.Fatalf("unexpected show rows: %#v", shows)
	}

	seasons, err := taterLocalTVItems(nil, cat, []string{root}, "http://server", "token", 0, "Some.Show.2020")
	if err != nil {
		t.Fatal(err)
	}
	if len(seasons) != 1 || seasons[0].Title != "Season 1" || seasons[0].MediaType != "season" {
		t.Fatalf("unexpected season rows: %#v", seasons)
	}

	episodes, err := taterLocalTVItems(nil, cat, []string{root}, "http://server", "token", 0, "Some.Show.2020/Season 01")
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 1 || episodes[0].Title != "S01E02 The One" || episodes[0].MediaType != "episode" {
		t.Fatalf("unexpected episode rows: %#v", episodes)
	}
}

func TestTaterLocalMusicItemsBrowseAlbumsAndTracks(t *testing.T) {
	root := t.TempDir()
	albumDir := filepath.Join(root, "Cool.Artist", "Big.Album.2024")
	if err := os.MkdirAll(albumDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"01.Opening.Track.mp3",
		"02.Second.Track.flac",
		"cover.jpg",
	} {
		if err := os.WriteFile(filepath.Join(albumDir, name), []byte("audio"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.Config{LocalMedia: config.LocalMediaConfig{
		Enabled: boolPtr(true),
		Categories: []config.LocalMediaCategory{{
			ID:          "music",
			Name:        "Music",
			LibraryType: "music",
			Paths:       []string{root},
			Enabled:     boolPtr(true),
		}},
	}}
	albums, err := taterLocalMusicAlbums(cfg, "http://server", "token", "music")
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 || albums[0].Title != "Big Album 2024" || albums[0].Artist != "Cool Artist" || albums[0].LeafCount != 2 {
		t.Fatalf("unexpected albums: %#v", albums)
	}

	tracks, err := taterLocalMusicTracks(cfg, "http://server", "token", albums[0].RatingKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 {
		t.Fatalf("expected 2 tracks, got %#v", tracks)
	}
	if tracks[0].Title != "Opening Track" || tracks[0].Index != 1 || tracks[0].Album != "Big Album 2024" {
		t.Fatalf("unexpected first track: %#v", tracks[0])
	}
	if !strings.Contains(tracks[0].StreamURL, "/api/tater/local/stream") || !strings.Contains(tracks[0].StreamURL, "player_token=token") {
		t.Fatalf("track stream URL was not generated correctly: %s", tracks[0].StreamURL)
	}
}

func TestTaterLocalDiscoverRowsAndItems(t *testing.T) {
	movieRoot := t.TempDir()
	tvRoot := t.TempDir()
	horrorDir := filepath.Join(movieRoot, "Halloween.Horror.1978")
	metadataDir := filepath.Join(movieRoot, "Plain.File")
	cartoonDir := filepath.Join(tvRoot, "Looney.Tunes.1941", "Season 01")
	if err := os.MkdirAll(horrorDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(metadataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cartoonDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(horrorDir, "Halloween.Horror.1978.mkv"), []byte("media"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(movieRoot, "Funny.Comedy.1999.mp4"), []byte("media"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadataDir, "Plain.File.mkv"), []byte("media"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadataDir, "movie.nfo"), []byte(`<movie><title>Metadata Action</title><year>1988</year><genre>Action</genre><plot>From sidecar metadata.</plot></movie>`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cartoonDir, "Looney.Tunes.S1941E01.mkv"), []byte("media"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{LocalMedia: config.LocalMediaConfig{
		Enabled: boolPtr(true),
		Categories: []config.LocalMediaCategory{
			{
				ID:          "movies",
				Name:        "Movies",
				LibraryType: "movies",
				Paths:       []string{movieRoot},
				Enabled:     boolPtr(true),
			},
			{
				ID:          "tv",
				Name:        "TV",
				LibraryType: "tv",
				Paths:       []string{tvRoot},
				Enabled:     boolPtr(true),
			},
		},
	}}

	rows := taterLocalDiscoverRows(cfg)
	for _, title := range []string{"Movies", "Series", "Action", "Horror", "Comedy", "Animation & Cartoons", "1940s", "1970s", "1980s", "1990s"} {
		if !hasTaterLocalDiscoverTitle(rows, title) {
			t.Fatalf("expected discover row %q in %#v", title, rows)
		}
	}

	horrorItems, err := taterLocalDiscoverItems(cfg, "http://server", "token", "local-discover:genre:horror")
	if err != nil {
		t.Fatal(err)
	}
	if len(horrorItems) != 1 || horrorItems[0].Title != "Halloween Horror" || horrorItems[0].MediaType != "movie" {
		t.Fatalf("unexpected horror discover items: %#v", horrorItems)
	}
	if !strings.Contains(horrorItems[0].StreamURL, "/api/tater/local/stream") || !strings.Contains(horrorItems[0].StreamURL, "player_token=token") {
		t.Fatalf("local discovery item should include a playable stream URL: %s", horrorItems[0].StreamURL)
	}

	seriesItems, err := taterLocalDiscoverItems(cfg, "http://server", "token", "local-discover:series")
	if err != nil {
		t.Fatal(err)
	}
	if len(seriesItems) != 1 || seriesItems[0].Title != "Looney Tunes" || seriesItems[0].MediaType != "show" {
		t.Fatalf("unexpected series discover items: %#v", seriesItems)
	}

	decadeItems, err := taterLocalDiscoverItems(cfg, "http://server", "token", "local-discover:decade:1970")
	if err != nil {
		t.Fatal(err)
	}
	if len(decadeItems) != 1 || decadeItems[0].Title != "Halloween Horror" {
		t.Fatalf("unexpected 1970s discover items: %#v", decadeItems)
	}

	actionItems, err := taterLocalDiscoverItems(cfg, "http://server", "token", "local-discover:genre:action")
	if err != nil {
		t.Fatal(err)
	}
	if len(actionItems) != 1 || actionItems[0].Title != "Metadata Action" || actionItems[0].Date != "1988" || actionItems[0].Category != "Action" {
		t.Fatalf("expected NFO metadata to feed action discovery, got %#v", actionItems)
	}
}

func hasTaterLocalDiscoverTitle(rows []taterUsenetCategory, title string) bool {
	for _, row := range rows {
		if row.Title == title && row.Type == "localDiscover" {
			return true
		}
	}
	return false
}

func TestTaterTubeTVEnabledRequiresToggleAndLocalMedia(t *testing.T) {
	cfg := config.DefaultConfig(t.TempDir())
	cfg.LocalMedia.Enabled = boolPtr(true)
	cfg.LocalMedia.Categories = []config.LocalMediaCategory{{
		ID:          "tv",
		Name:        "TV",
		LibraryType: "tv",
		Paths:       []string{t.TempDir()},
		Enabled:     boolPtr(true),
	}}

	if !taterTubeTVEnabled(cfg) {
		t.Fatal("expected Tube TV to be enabled when toggle and local media are configured")
	}

	cfg.TubeTV.Enabled = boolPtr(false)
	if taterTubeTVEnabled(cfg) {
		t.Fatal("expected Tube TV to be hidden when disabled")
	}

	cfg.TubeTV.Enabled = boolPtr(true)
	cfg.LocalMedia.Enabled = boolPtr(false)
	if taterTubeTVEnabled(cfg) {
		t.Fatal("expected Tube TV to be hidden without local media")
	}
}

func TestTaterTVCustomSourceSingleMovieFileDoesNotExpandLibrary(t *testing.T) {
	configDir := t.TempDir()
	mediaRoot := filepath.Join(configDir, "movies")
	targetDir := filepath.Join(mediaRoot, "Target.Movie.2024")
	otherDir := filepath.Join(mediaRoot, "Other.Movie.2025")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(otherDir, 0755); err != nil {
		t.Fatal(err)
	}
	targetRel := "Target.Movie.2024/Target.Movie.2024.mkv"
	if err := os.WriteFile(filepath.Join(mediaRoot, filepath.FromSlash(targetRel)), []byte("target"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherDir, "Other.Movie.2025.mkv"), []byte("other"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig(configDir)
	cfg.LocalMedia.Enabled = boolPtr(true)
	cfg.LocalMedia.Categories = []config.LocalMediaCategory{{
		ID:          "movies",
		Name:        "Movies",
		LibraryType: "movies",
		Paths:       []string{mediaRoot},
		Enabled:     boolPtr(true),
	}}

	source := taterTVSource{Title: "Custom", Seen: map[string]bool{}}
	err := taterTVAddRefToSource(cfg, "http://server", "token", &source, config.TubeTVCustomSource{
		CategoryID:  "movies",
		SourceIndex: 0,
		Path:        targetRel,
		Title:       "Target Movie",
		MediaType:   "movie",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(source.Programs) != 1 {
		t.Fatalf("expected one selected movie, got %#v", source.Programs)
	}
	if source.Programs[0].Path != targetRel || source.Programs[0].Title != "Target Movie" {
		t.Fatalf("unexpected selected movie row: %#v", source.Programs[0])
	}
}

func TestTaterNzbWatchAgainRecordsLatestAndTrims(t *testing.T) {
	cfg := config.DefaultConfig(t.TempDir())
	cfg.Newznab.WatchAgainLimit = 2
	cfg.Newznab.WatchAgainRetentionDays = 30

	if err := taterRecordNzbWatchAgain(cfg, "First Movie", "https://indexer.example/api?t=get&id=1&apikey=secret", "Movies"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if err := taterRecordNzbWatchAgain(cfg, "Second Movie", "https://indexer.example/api?t=get&id=2&apikey=secret", "Movies"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if err := taterRecordNzbWatchAgain(cfg, "First Movie", "https://indexer.example/api?t=get&id=1&apikey=secret", "Movies"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if err := taterRecordNzbWatchAgain(cfg, "Third Movie", "https://indexer.example/api?t=get&id=3&apikey=secret", "Movies"); err != nil {
		t.Fatal(err)
	}

	rows, err := taterNzbWatchAgainRows(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected two watch again rows, got %#v", rows)
	}
	if rows[0].Title != "Third Movie" || rows[1].Title != "First Movie" {
		t.Fatalf("expected latest rows to be third then first, got %#v", rows)
	}
	if strings.Contains(rows[1].NzbURL, "secret") || strings.Contains(rows[1].NzbURL, "apikey") {
		t.Fatalf("watch again rows should not expose stored Newznab credentials: %s", rows[1].NzbURL)
	}
	if !strings.Contains(rows[1].SizeText, "2x") {
		t.Fatalf("expected repeat count in row detail, got %#v", rows[1])
	}
}

func TestTaterNzbWatchAgainPrunesExpiredRows(t *testing.T) {
	cfg := config.DefaultConfig(t.TempDir())
	cfg.Newznab.WatchAgainLimit = 50
	cfg.Newznab.WatchAgainRetentionDays = 1
	now := time.Now().UTC()

	if err := saveTaterNzbWatchAgainStoreUnlocked(cfg, taterNzbWatchAgainStore{Items: map[string]taterNzbWatchAgainEntry{
		"old": {
			ID:           "old",
			Title:        "Old Movie",
			NzbURL:       "https://indexer.example/api?t=get&id=old",
			LastPlayedAt: now.Add(-48 * time.Hour),
		},
		"new": {
			ID:           "new",
			Title:        "New Movie",
			NzbURL:       "https://indexer.example/api?t=get&id=new",
			LastPlayedAt: now,
		},
	}}); err != nil {
		t.Fatal(err)
	}

	rows, err := taterNzbWatchAgainRows(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Title != "New Movie" {
		t.Fatalf("expected only unexpired row, got %#v", rows)
	}
}

func TestAttachTaterDurationAddsPlayerFields(t *testing.T) {
	item := taterUsenetItem{Title: "Track"}

	attachTaterDuration(&item, 65.4321)

	if item.Duration != 65 {
		t.Fatalf("expected rounded duration seconds, got %d", item.Duration)
	}
	if item.DurationSeconds != 65.432 {
		t.Fatalf("expected millisecond precision durationSeconds, got %f", item.DurationSeconds)
	}
	if item.DurationDisplay != "1:05" {
		t.Fatalf("expected display duration 1:05, got %q", item.DurationDisplay)
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func fakeFFmpegWithProbe(t *testing.T, configDir string, probeScript string) string {
	t.Helper()
	binDir := filepath.Join(configDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	ffmpegPath := filepath.Join(binDir, "ffmpeg")
	ffprobePath := filepath.Join(binDir, "ffprobe")
	if err := os.WriteFile(ffmpegPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ffprobePath, []byte(probeScript), 0755); err != nil {
		t.Fatal(err)
	}
	return ffmpegPath
}

func TestEffectiveFFprobePathFindsTaterSibling(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	ffmpegPath := filepath.Join(binDir, "tater-ffmpeg")
	ffprobePath := filepath.Join(binDir, "tater-ffprobe")
	if err := os.WriteFile(ffmpegPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ffprobePath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	if got := effectiveFFprobePath(ffmpegPath); got != ffprobePath {
		t.Fatalf("expected sibling tater-ffprobe, got %q want %q", got, ffprobePath)
	}
}

func TestTaterBuildTVLineupUsesServerLocalMediaAndCommercials(t *testing.T) {
	configDir := t.TempDir()
	mediaRoot := filepath.Join(configDir, "media")
	episodeDir := filepath.Join(mediaRoot, "Looney.Tunes", "Season 1936")
	if err := os.MkdirAll(episodeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(episodeDir, "Looney.Tunes.S1936E01.mkv"), []byte("episode"), 0644); err != nil {
		t.Fatal(err)
	}

	commercialRoot := filepath.Join(configDir, "metadata", "tube-tv-commercials")
	commercialDir := filepath.Join(commercialRoot, "cartoon-network")
	if err := os.MkdirAll(commercialDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commercialDir, "Snack.Ad.mp4"), []byte("ad"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig(configDir)
	cfg.Transcoding.FFmpegPath = fakeFFmpegWithProbe(t, configDir, "#!/bin/sh\nprintf '600.000\\n'\n")
	cfg.LocalMedia.Enabled = boolPtr(true)
	cfg.LocalMedia.Categories = []config.LocalMediaCategory{{
		ID:          "tv",
		Name:        "TV",
		LibraryType: "tv",
		Paths:       []string{mediaRoot},
		Enabled:     boolPtr(true),
	}}
	cfg.TubeTV.AutoChannels = boolPtr(false)
	cfg.TubeTV.CommercialsEnabled = boolPtr(true)
	cfg.TubeTV.CustomChannels = []config.TubeTVCustomChannel{{
		ID:                 "cartoons",
		Title:              "Cartoons",
		CommercialCategory: "cartoon-network",
		Sources: []config.TubeTVCustomSource{{
			CategoryID:  "tv",
			SourceIndex: -1,
		}},
	}}

	channels, err := taterBuildTVLineup(cfg, "http://server", "token")
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 1 {
		t.Fatalf("expected one custom channel, got %d: %#v", len(channels), channels)
	}
	if channels[0].Number != "02" || channels[0].Title != "Cartoons" {
		t.Fatalf("unexpected channel metadata: %#v", channels[0])
	}
	if !strings.Contains(channels[0].StreamURL, "/api/tater/tv/channel/02/playlist.m3u8") {
		t.Fatalf("expected channel HLS playlist URL, got %q", channels[0].StreamURL)
	}

	hasEpisode := false
	hasCommercial := false
	for _, row := range channels[0].Schedule {
		switch row["kind"] {
		case "episode":
			hasEpisode = strings.Contains(row["streamUrl"].(string), "/api/tater/local/stream")
		case "commercial":
			hasCommercial = strings.Contains(row["url"].(string), "/api/tater/tv/commercials/file")
		}
	}
	if !hasEpisode || !hasCommercial {
		t.Fatalf("expected episode and commercial schedule entries: %#v", channels[0].Schedule)
	}
}

func TestTaterTVCommercialCategoriesProbeDurations(t *testing.T) {
	configDir := t.TempDir()
	ffmpegPath := fakeFFmpegWithProbe(t, configDir, "#!/bin/sh\nprintf '12.500\\n11.000\\n'\n")

	commercialRoot := filepath.Join(configDir, "metadata", "tube-tv-commercials")
	commercialDir := filepath.Join(commercialRoot, "retro-ads")
	if err := os.MkdirAll(commercialDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commercialDir, "Short Spot.mp4"), []byte("ad"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig(configDir)
	cfg.Transcoding.FFmpegPath = ffmpegPath

	categories := taterTVCommercialCategories(cfg, "http://server", "token")
	if len(categories) != 1 || len(categories[0].Videos) != 1 {
		t.Fatalf("expected one probed commercial, got %#v", categories)
	}
	video := categories[0].Videos[0]
	if !video.DurationKnown {
		t.Fatalf("expected commercial duration to be known: %#v", video)
	}
	if video.Duration != 12.5 || video.FullDuration != 12.5 {
		t.Fatalf("expected probed commercial duration 12.5, got %#v", video)
	}
}

func TestTaterTVBumpersWrapCommercialBreaksAndDoNotRepeatEarly(t *testing.T) {
	configDir := t.TempDir()
	cfg := config.DefaultConfig(configDir)
	cfg.Transcoding.FFmpegPath = fakeFFmpegWithProbe(t, configDir, "#!/bin/sh\nprintf '8.000\\n'\n")

	files := map[string][]string{
		"before/network-ids": {"Before One.mp4", "Before Two.mp4", "Before Three.mp4"},
		"after/back-to-show": {"After One.mp4", "After Two.mp4"},
		"both/station-ids":   {"Both One.mp4", "Both Two.mp4"},
	}
	for relDir, names := range files {
		dir := filepath.Join(taterTVBumperRoot(cfg), relDir)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("bumper"), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}

	groups := taterTVBumperGroups(cfg, "http://server", "token")
	if len(groups) != 3 {
		t.Fatalf("expected three bumper groups, got %#v", groups)
	}
	before, after := taterTVBumperPools(groups, []string{"network-ids", "back-to-show", "station-ids"})
	if len(before) != 5 || len(after) != 4 {
		t.Fatalf("unexpected placement pools: before=%d after=%d", len(before), len(after))
	}

	rng := rand.New(rand.NewSource(12))
	deck := []taterTVBumper{}
	lastKey := ""
	seen := map[string]bool{}
	for i := 0; i < len(before); i++ {
		bumper, ok := taterTVNextBumper(before, &deck, &lastKey, rng)
		if !ok {
			t.Fatal("expected bumper from non-empty pool")
		}
		key := taterTVBumperKey(bumper)
		if seen[key] {
			t.Fatalf("bumper repeated before the deck was exhausted: %s", key)
		}
		seen[key] = true
	}

	cfg.TubeTV.CommercialsEnabled = boolPtr(true)
	source := taterTVSource{
		Title:        "Bumper Channel",
		BumperGroups: []string{"network-ids", "back-to-show", "station-ids"},
		Programs: []taterUsenetItem{{
			Title:           "Feature",
			StreamURL:       "http://server/feature",
			DurationSeconds: 600,
		}},
	}
	commercials := []taterTVCommercialCategory{{
		ID: "ads",
		Videos: []taterTVCommercial{{
			Title:         "Ad",
			CategoryID:    "ads",
			Name:          "ad.mp4",
			Duration:      15,
			FullDuration:  15,
			DurationKnown: true,
		}},
	}}
	schedule, _ := taterTVBuildSchedule(cfg, source, commercials, rand.New(rand.NewSource(3)))
	if len(schedule) < 5 {
		t.Fatalf("expected program, two bumpers, and commercials: %#v", schedule)
	}
	if rowString(schedule[0], "kind") != "movie" || rowString(schedule[1], "kind") != "bumper" {
		t.Fatalf("expected a bumper immediately before commercials: %#v", schedule)
	}
	if placement := rowString(schedule[1], "placement"); placement != "before" && placement != "both" {
		t.Fatalf("unexpected pre-commercial bumper placement %q", placement)
	}
	last := schedule[len(schedule)-1]
	if rowString(last, "kind") != "bumper" {
		t.Fatalf("expected a bumper after commercials: %#v", schedule)
	}
	if placement := rowString(last, "placement"); placement != "after" && placement != "both" {
		t.Fatalf("unexpected post-commercial bumper placement %q", placement)
	}
	for _, row := range schedule[2 : len(schedule)-1] {
		if rowString(row, "kind") != "commercial" {
			t.Fatalf("expected only commercials between bumpers: %#v", schedule)
		}
	}

	path, err := taterTVResolveSchedulePath(cfg, schedule[1])
	if err != nil {
		t.Fatalf("expected bumper schedule path to resolve: %v", err)
	}
	if filepath.Base(path) != rowString(schedule[1], "name") {
		t.Fatalf("resolved unexpected bumper path %q", path)
	}
}

func TestTaterTVUnknownDurationsAreNotScheduled(t *testing.T) {
	configDir := t.TempDir()
	mediaRoot := filepath.Join(configDir, "movies")
	movieDir := filepath.Join(mediaRoot, "Unknown.Movie.2024")
	if err := os.MkdirAll(movieDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(movieDir, "Unknown.Movie.2024.mkv"), []byte("not real media"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig(configDir)
	cfg.Transcoding.FFmpegPath = fakeFFmpegWithProbe(t, configDir, "#!/bin/sh\necho probe failed >&2\nexit 1\n")
	cfg.LocalMedia.Enabled = boolPtr(true)
	cfg.LocalMedia.Categories = []config.LocalMediaCategory{{
		ID:          "movies",
		Name:        "Movies",
		LibraryType: "movies",
		Paths:       []string{mediaRoot},
		Enabled:     boolPtr(true),
	}}
	cfg.TubeTV.AutoChannels = boolPtr(false)
	cfg.TubeTV.CustomChannels = []config.TubeTVCustomChannel{{
		ID:    "movie-channel",
		Title: "Movie Channel",
		Sources: []config.TubeTVCustomSource{{
			CategoryID:  "movies",
			SourceIndex: -1,
		}},
	}}

	channels, err := taterBuildTVLineup(cfg, "http://server", "token")
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 0 {
		t.Fatalf("unknown-duration media should not create fallback Tube TV channels: %#v", channels)
	}
	if duration := taterTVDuration(taterUsenetItem{Title: "Unknown"}, "movie"); duration != 0 {
		t.Fatalf("movie fallback duration should be disabled, got %f", duration)
	}
	if duration := taterTVDuration(taterUsenetItem{Title: "Unknown"}, "episode"); duration != 0 {
		t.Fatalf("episode fallback duration should be disabled, got %f", duration)
	}
}

func TestTaterTVGuideBuildsAndExtendsSharedSchedule(t *testing.T) {
	taterTVResetGuide()
	defer taterTVResetGuide()

	configDir := t.TempDir()
	mediaRoot := filepath.Join(configDir, "movies")
	movieDir := filepath.Join(mediaRoot, "Guide.Movie.2024")
	if err := os.MkdirAll(movieDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(movieDir, "Guide.Movie.2024.mkv"), []byte("movie"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig(configDir)
	cfg.Transcoding.FFmpegPath = fakeFFmpegWithProbe(t, configDir, "#!/bin/sh\nprintf '7200.000\\n'\n")
	cfg.LocalMedia.Enabled = boolPtr(true)
	cfg.LocalMedia.Categories = []config.LocalMediaCategory{{
		ID:          "movies",
		Name:        "Movies",
		LibraryType: "movies",
		Paths:       []string{mediaRoot},
		Enabled:     boolPtr(true),
	}}
	cfg.TubeTV.AutoChannels = boolPtr(false)
	cfg.TubeTV.CustomChannels = []config.TubeTVCustomChannel{{
		ID:    "movie-channel",
		Title: "Movie Channel",
		Sources: []config.TubeTVCustomSource{{
			CategoryID:  "movies",
			SourceIndex: -1,
		}},
	}}

	now := time.Now().Truncate(time.Second)
	guide, err := taterTVEnsureGuide(cfg, "http://server", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(guide.Channels) != 1 {
		t.Fatalf("expected one guide channel, got %#v", guide.Channels)
	}
	firstDuration := guide.Channels[0].TotalDuration
	if firstDuration < taterTVGuideHorizon.Seconds() {
		t.Fatalf("expected guide to plan at least %s, got %s", taterTVGuideHorizon, time.Duration(firstDuration*float64(time.Second)))
	}

	taterTVGuideMu.Lock()
	taterTVGuideCache.PlannedUntil = guide.StartedAt.Add(90 * time.Minute)
	taterTVGuideMu.Unlock()

	extended, err := taterTVEnsureGuide(cfg, "http://server", guide.StartedAt.Add(11*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if extended.StartedAt != guide.StartedAt {
		t.Fatalf("expected shared guide start to remain stable, got %s want %s", extended.StartedAt, guide.StartedAt)
	}
	if extended.Channels[0].TotalDuration <= firstDuration {
		t.Fatalf("expected guide extension beyond %f, got %f", firstDuration, extended.Channels[0].TotalDuration)
	}
}

func TestTaterTVGuideCapsLargeSeriesWhileBuilding(t *testing.T) {
	cfg := config.DefaultConfig(t.TempDir())
	episodes := make([]taterUsenetItem, 5000)
	for i := range episodes {
		episodes[i] = taterUsenetItem{
			Title:           fmt.Sprintf("Episode %04d", i+1),
			Path:            fmt.Sprintf("/series/season/episode-%04d.mkv", i+1),
			StreamURL:       fmt.Sprintf("http://server/episode/%04d", i+1),
			DurationSeconds: 10 * 60,
		}
	}
	source := taterTVSource{
		Title: "Large Series Channel",
		Groups: []taterTVEpisodeGroup{{
			Title:    "Large Series",
			Episodes: episodes,
		}},
	}

	schedule, total := taterTVBuildScheduleUntil(
		cfg,
		source,
		nil,
		rand.New(rand.NewSource(1)),
		0,
		taterTVGuideHorizon.Seconds(),
	)

	if total < taterTVGuideHorizon.Seconds() || total > taterTVGuideHorizon.Seconds()+10*60 {
		t.Fatalf("expected schedule to stop at the guide horizon, got %.0f seconds", total)
	}
	if len(schedule) > 73 {
		t.Fatalf("expected a bounded guide schedule, got %d rows", len(schedule))
	}
}

func TestTaterTVGuidePersistsAcrossMemoryReset(t *testing.T) {
	taterTVResetGuide()

	configDir := t.TempDir()
	mediaRoot := filepath.Join(configDir, "movies")
	movieDir := filepath.Join(mediaRoot, "Saved.Guide.Movie.2024")
	if err := os.MkdirAll(movieDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(movieDir, "Saved.Guide.Movie.2024.mkv"), []byte("movie"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig(configDir)
	cfg.Transcoding.FFmpegPath = fakeFFmpegWithProbe(t, configDir, "#!/bin/sh\nprintf '7200.000\\n'\n")
	cfg.LocalMedia.Enabled = boolPtr(true)
	cfg.LocalMedia.Categories = []config.LocalMediaCategory{{
		ID:          "movies",
		Name:        "Movies",
		LibraryType: "movies",
		Paths:       []string{mediaRoot},
		Enabled:     boolPtr(true),
	}}
	cfg.TubeTV.AutoChannels = boolPtr(false)
	cfg.TubeTV.CustomChannels = []config.TubeTVCustomChannel{{
		ID:    "movie-channel",
		Title: "Movie Channel",
		Sources: []config.TubeTVCustomSource{{
			CategoryID:  "movies",
			SourceIndex: -1,
		}},
	}}
	defer taterTVResetGuideForConfig(cfg)

	now := time.Now().Truncate(time.Second)
	guide, err := taterTVEnsureGuide(cfg, "http://server", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(guide.Channels) != 1 {
		t.Fatalf("expected one guide channel, got %#v", guide.Channels)
	}
	if _, err := os.Stat(taterTVGuideCachePath(cfg)); err != nil {
		t.Fatalf("expected guide cache file: %v", err)
	}

	if err := os.WriteFile(cfg.Transcoding.FFmpegPath, []byte("#!/bin/sh\necho should not probe >&2\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	taterTVResetGuide()

	loaded, err := taterTVEnsureGuide(cfg, "http://server", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.StartedAt != guide.StartedAt {
		t.Fatalf("expected persisted guide start %s, got %s", guide.StartedAt, loaded.StartedAt)
	}
	if len(loaded.Channels) != 1 || loaded.Channels[0].Title != "Movie Channel" {
		t.Fatalf("expected persisted channel, got %#v", loaded.Channels)
	}
	if loaded.Channels[0].TotalDuration != guide.Channels[0].TotalDuration {
		t.Fatalf("expected persisted duration %f, got %f", guide.Channels[0].TotalDuration, loaded.Channels[0].TotalDuration)
	}
}

func TestTaterTVPersonalizeChannelsRestoresScheduledItemURLs(t *testing.T) {
	channels := []taterTVChannel{{
		Number: "02",
		Title:  "Cartoons",
		Schedule: []map[string]any{
			{
				"title":       "Episode One",
				"kind":        "episode",
				"categoryId":  "local:tv",
				"sourceIndex": 0,
				"path":        "Show/Season 01/Episode One.mkv",
			},
			{
				"title":      "Snack Time",
				"kind":       "commercial",
				"categoryId": "retro-ads",
				"name":       "Snack Time.mp4",
			},
		},
	}}

	personalized := taterTVPersonalizeChannels(channels, "http://server:8080", "player token")
	if len(personalized) != 1 || len(personalized[0].Schedule) != 2 {
		t.Fatalf("unexpected personalized channels: %#v", personalized)
	}
	if !strings.Contains(personalized[0].StreamURL, "/api/tater/tv/channel/02/playlist.m3u8") {
		t.Fatalf("expected compatibility channel URL, got %q", personalized[0].StreamURL)
	}
	episodeURL := rowString(personalized[0].Schedule[0], "streamUrl")
	commercialURL := rowString(personalized[0].Schedule[1], "url")
	if !strings.Contains(episodeURL, "/api/tater/tv/channel/02/item/0") ||
		!strings.Contains(episodeURL, "player_token=player+token") {
		t.Fatalf("episode item URL was not restored: %q", episodeURL)
	}
	if !strings.Contains(commercialURL, "/api/tater/tv/channel/02/item/1") ||
		!strings.Contains(commercialURL, "player_token=player+token") {
		t.Fatalf("commercial item URL was not restored: %q", commercialURL)
	}
	if personalized[0].Schedule[0]["serverSeek"] != true ||
		rowString(personalized[0].Schedule[0], "seekMode") != "server" {
		t.Fatalf("scheduled server item should use server seek: %#v", personalized[0].Schedule[0])
	}
	if rowString(channels[0].Schedule[0], "streamUrl") != "" {
		t.Fatal("personalizing the guide mutated the cached schedule")
	}
}

func TestTaterTVChannelItemFromPath(t *testing.T) {
	number, index, ok := taterTVChannelItemFromPath("/api/tater/tv/channel/09/item/42")
	if !ok || number != "09" || index != 42 {
		t.Fatalf("unexpected parsed channel item: number=%q index=%d ok=%v", number, index, ok)
	}
	for _, path := range []string{
		"/api/tater/tv/channel/09/item",
		"/api/tater/tv/channel/09/item/nope",
		"/api/tater/tv/channel/09/item/-1",
		"/api/tater/tv/channel/09/playlist.m3u8",
	} {
		if _, _, ok := taterTVChannelItemFromPath(path); ok {
			t.Fatalf("malformed channel item path accepted: %s", path)
		}
	}
}

func TestTaterTVCurrentSchedulePositionWrapsElapsedTime(t *testing.T) {
	startedAt := time.Now().Add(-95 * time.Second)
	channel := taterTVChannel{
		Number:        "02",
		Title:         "Looping",
		TotalDuration: 90,
		Schedule: []map[string]any{
			{"title": "First", "duration": 60.0, "mediaOffset": 0.0, "start": 0.0, "end": 60.0},
			{"title": "Second", "duration": 30.0, "mediaOffset": 0.0, "start": 60.0, "end": 90.0},
		},
	}

	index, startOffset, remaining := taterTVCurrentSchedulePosition(channel, startedAt, startedAt.Add(95*time.Second))
	if index != 0 {
		t.Fatalf("expected wrapped schedule index 0, got %d", index)
	}
	if startOffset < 4.9 || startOffset > 5.1 {
		t.Fatalf("expected wrapped offset near 5 seconds, got %f", startOffset)
	}
	if remaining < 54.9 || remaining > 55.1 {
		t.Fatalf("expected wrapped remaining near 55 seconds, got %f", remaining)
	}
}

func TestTaterTVNumberSourcesHonorsReservedCustomChannels(t *testing.T) {
	numbered := taterTVNumberSources([]taterTVSource{{
		Title:         "The Phooey",
		SourceType:    "custom",
		ChannelNumber: "09",
	}})
	if len(numbered) != 1 || numbered[0].Number != "09" || numbered[0].Source.Title != "The Phooey" {
		t.Fatalf("expected lone custom channel to keep 09, got %#v", numbered)
	}

	sources := []taterTVSource{{Title: "Custom 09", SourceType: "custom", ChannelNumber: "09"}}
	for i := 1; i <= 8; i++ {
		sources = append(sources, taterTVSource{Title: fmt.Sprintf("Auto %d", i), SourceType: "auto"})
	}
	numbered = taterTVNumberSources(sources)
	if len(numbered) != 9 {
		t.Fatalf("expected 9 numbered sources, got %#v", numbered)
	}
	for i := 0; i < 7; i++ {
		wantNumber := fmt.Sprintf("%02d", i+2)
		wantTitle := fmt.Sprintf("Auto %d", i+1)
		if numbered[i].Number != wantNumber || numbered[i].Source.Title != wantTitle {
			t.Fatalf("slot %d = %#v, want %s/%s", i, numbered[i], wantNumber, wantTitle)
		}
	}
	if numbered[7].Number != "09" || numbered[7].Source.Title != "Custom 09" {
		t.Fatalf("expected reserved custom channel at 09, got %#v", numbered[7])
	}
	if numbered[8].Number != "10" || numbered[8].Source.Title != "Auto 8" {
		t.Fatalf("expected auto channels to continue at 10, got %#v", numbered[8])
	}

	numbered = taterTVNumberSources([]taterTVSource{
		{Title: "Regional One", SourceType: "custom", ChannelNumber: "01"},
		{Title: "Auto Two", SourceType: "auto"},
	})
	if len(numbered) != 2 {
		t.Fatalf("expected channel 01 plus an auto channel, got %#v", numbered)
	}
	if numbered[0].Number != "01" || numbered[0].Source.Title != "Regional One" {
		t.Fatalf("expected custom channel 01 first, got %#v", numbered[0])
	}
	if numbered[1].Number != "02" || numbered[1].Source.Title != "Auto Two" {
		t.Fatalf("expected auto numbering to continue at 02, got %#v", numbered[1])
	}
}

func TestTaterTVStreamItemsStartAtLivePosition(t *testing.T) {
	configDir := t.TempDir()
	mediaRoot := filepath.Join(configDir, "movies")
	movieDir := filepath.Join(mediaRoot, "That Movie")
	if err := os.MkdirAll(movieDir, 0755); err != nil {
		t.Fatal(err)
	}
	moviePath := filepath.Join(movieDir, "That Movie's Cut.mkv")
	if err := os.WriteFile(moviePath, []byte("movie"), 0644); err != nil {
		t.Fatal(err)
	}
	commercialRoot := filepath.Join(configDir, "metadata", "tube-tv-commercials")
	commercialDir := filepath.Join(commercialRoot, "retro-ads")
	if err := os.MkdirAll(commercialDir, 0755); err != nil {
		t.Fatal(err)
	}
	commercialPath := filepath.Join(commercialDir, "Ad Spot.mp4")
	if err := os.WriteFile(commercialPath, []byte("ad"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig(configDir)
	cfg.LocalMedia.Enabled = boolPtr(true)
	cfg.LocalMedia.Categories = []config.LocalMediaCategory{{
		ID:          "movies",
		Name:        "Movies",
		LibraryType: "movies",
		Paths:       []string{mediaRoot},
		Enabled:     boolPtr(true),
	}}

	channel := taterTVChannel{
		Number:        "02",
		Title:         "Movies",
		TotalDuration: 150,
		Schedule: []map[string]any{
			{
				"title":        "That Movie",
				"kind":         "movie",
				"categoryId":   "local:movies",
				"sourceIndex":  0,
				"path":         "That Movie/That Movie's Cut.mkv",
				"duration":     120.0,
				"fullDuration": 120.0,
				"mediaOffset":  0.0,
				"start":        0.0,
				"end":          120.0,
			},
			{
				"title":        "Ad Spot",
				"kind":         "commercial",
				"categoryId":   "retro-ads",
				"name":         "Ad Spot.mp4",
				"duration":     30.0,
				"fullDuration": 30.0,
				"start":        120.0,
				"end":          150.0,
			},
		},
	}

	startedAt := time.Now().Add(-45 * time.Second)
	items, err := taterTVResolveStreamItems(cfg, channel, startedAt, time.Now(), 90)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected movie and commercial, got %#v", items)
	}
	if items[0].Path != moviePath {
		t.Fatalf("unexpected movie path %q", items[0].Path)
	}
	if items[0].StartSeconds < 44 || items[0].StartSeconds > 46 {
		t.Fatalf("expected live start near 45 seconds, got %f", items[0].StartSeconds)
	}
	if items[0].DurationSeconds < 74 || items[0].DurationSeconds > 76 {
		t.Fatalf("expected remaining movie duration near 75 seconds, got %f", items[0].DurationSeconds)
	}

	if items[1].Path != commercialPath {
		t.Fatalf("unexpected commercial path %q", items[1].Path)
	}
	if items[1].StartSeconds != 0 || items[1].DurationSeconds != 30 {
		t.Fatalf("expected full commercial segment, got %#v", items[1])
	}
}

func TestTaterTVItemTranscodeArgsMatchLocalPlayback(t *testing.T) {
	args := buildTaterTVChannelTranscodeArgs(config.TranscodingConfig{}, transcodeProfiles["crt_480p"], "none", "/media/movie.mkv", 12.5, 30, "", "")
	joined := strings.Join(args, " ")

	if strings.Contains(joined, "-f concat") {
		t.Fatalf("item transcode path must not use concat demuxer: %s", joined)
	}
	if strings.Contains(joined, "-re") || strings.Contains(joined, "initial_discontinuity") {
		t.Fatalf("item playback must not use continuous-channel pacing or discontinuities: %s", joined)
	}
	if !strings.Contains(joined, "-ss 12.500") || !strings.Contains(joined, "-t 30.000") {
		t.Fatalf("expected scheduled item start/duration in args: %s", joined)
	}
}

func TestTaterTVSegmentTranscodeArgsAddChannelLogoOverlay(t *testing.T) {
	args := buildTaterTVChannelTranscodeArgs(config.TranscodingConfig{}, transcodeProfiles["crt_480p"], "none", "/media/movie.mkv", 0, 30, "/metadata/logos/cartoon.png", "bottom_right")
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "-loop 1 -framerate 30 -i /metadata/logos/cartoon.png") {
		t.Fatalf("expected channel logo input in args: %s", joined)
	}
	if !strings.Contains(joined, "-filter_complex") || !strings.Contains(joined, "overlay=x=W-w-") {
		t.Fatalf("expected overlay filter in args: %s", joined)
	}
	if !strings.Contains(joined, "-map [vout] -map 0:a:0?") {
		t.Fatalf("expected generated video output mapping in args: %s", joined)
	}
	if strings.Contains(joined, " -vf ") {
		t.Fatalf("logo overlay path should use filter_complex instead of -vf: %s", joined)
	}
}

func TestTaterTVHLSArgsNormalizeAudioAndSegments(t *testing.T) {
	args := buildTaterTVChannelHLSArgs(
		config.TranscodingConfig{},
		transcodeProfiles["crt_480p"],
		"none",
		"/media/movie.mkv",
		12.5,
		30,
		"/metadata/logos/cartoon.png",
		"bottom_right",
		"/tmp/hls/index.m3u8",
		"/tmp/hls/seg-%05d.ts",
	)
	joined := strings.Join(args, " ")

	for _, expected := range []string{
		"-readrate 1",
		"-readrate_initial_burst 2",
		"-filter_complex",
		"overlay=x=W-w-",
		"-af aresample=async=1:first_pts=0",
		"pad=w=640:h=480:x=(ow-iw)/2:y=(oh-ih)/2:color=black,setsar=1,fps=30000/1001",
		"-flags:v +cgop",
		"-g 60",
		"-bf 0",
		"-bsf:v dump_extra=freq=keyframe",
		"-muxdelay 0",
		"-muxpreload 0",
		"-f hls",
		"-hls_time 2",
		"-hls_segment_options mpegts_flags=+resend_headers+initial_discontinuity:mpegts_copyts=1",
		"-hls_flags independent_segments+temp_file",
		"-hls_segment_filename /tmp/hls/seg-%05d.ts",
		"/tmp/hls/index.m3u8",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected %q in HLS args: %s", expected, joined)
		}
	}
}

func TestTaterTVHLSArgsUseIndependentQSVSegments(t *testing.T) {
	args := buildTaterTVChannelHLSArgsWithCodec(
		config.TranscodingConfig{},
		transcodeProfiles["hdmi_1080p"],
		"qsv",
		transcodeCodecHEVC,
		"/media/movie.mkv",
		0,
		30,
		"",
		"",
		"/tmp/hls/index.m3u8",
		"/tmp/hls/seg-%05d.ts",
	)
	joined := strings.Join(args, " ")
	for _, expected := range []string{
		"-c:v hevc_qsv",
		"-forced_idr 1",
		"pad=w=1920:h=1080",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected %q in HLS args: %s", expected, joined)
		}
	}
}

func TestTaterTVChannelLogoOverlayPositions(t *testing.T) {
	profile := transcodeProfiles["crt_480p"]
	tests := []struct {
		position string
		x        string
		y        string
	}{
		{position: "top_left", x: "x=12", y: "y=13"},
		{position: "top_right", x: "x=W-w-12", y: "y=13"},
		{position: "bottom_left", x: "x=12", y: "y=H-h-13"},
		{position: "bottom_right", x: "x=W-w-12", y: "y=H-h-13"},
		{position: "", x: "x=W-w-12", y: "y=H-h-13"},
	}

	for _, tt := range tests {
		filter := taterTVChannelLogoFilter("", profile, tt.position)
		if !strings.Contains(filter, "overlay="+tt.x+":"+tt.y+":") {
			t.Fatalf("position %q produced unexpected overlay filter: %s", tt.position, filter)
		}
	}
}

func TestTaterTVChannelLogoIsHiddenForInterstitials(t *testing.T) {
	logo := "/tmp/channel-logo.png"
	if got := taterTVLogoForItem(taterTVStreamItem{Kind: "movie"}, logo); got != logo {
		t.Fatalf("movie should retain channel logo, got %q", got)
	}
	if got := taterTVLogoForItem(taterTVStreamItem{Kind: "episode"}, logo); got != logo {
		t.Fatalf("episode should retain channel logo, got %q", got)
	}
	if got := taterTVLogoForItem(taterTVStreamItem{Kind: "commercial"}, logo); got != "" {
		t.Fatalf("commercial should not receive channel logo, got %q", got)
	}
	if got := taterTVLogoForItem(taterTVStreamItem{Kind: "bumper"}, logo); got != "" {
		t.Fatalf("bumper should not receive channel logo, got %q", got)
	}
}

func TestTaterTVHLSPlaylistIncludesTokenAndDiscontinuity(t *testing.T) {
	session := &taterTVHLSSession{
		publicID:       "live",
		number:         "02",
		profileID:      "hdmi_1080p",
		requestedAccel: "auto",
		accessed:       time.Now(),
		segments: []taterTVHLSSegment{
			{Sequence: 0, Duration: 4, Path: "item-000/seg-00000.ts"},
			{Sequence: 1, Duration: 4, Path: "item-001/seg-00000.ts", Discontinuity: true},
		},
	}
	playlist := session.playlist("player token")

	if !strings.Contains(playlist, "#EXT-X-DISCONTINUITY") {
		t.Fatalf("expected discontinuity marker in playlist: %s", playlist)
	}
	if !strings.Contains(playlist, "player_token=player+token") {
		t.Fatalf("expected per-request player token in segment URLs: %s", playlist)
	}
	if !strings.Contains(playlist, "/api/tater/tv/channel/02/hls/live/item-001/seg-00000.ts") {
		t.Fatalf("expected HLS segment URL in playlist: %s", playlist)
	}
}

func TestTaterTVHLSPlaylistUsesSmallLiveWindow(t *testing.T) {
	session := &taterTVHLSSession{
		publicID:       "live",
		number:         "02",
		profileID:      "hdmi_1080p",
		requestedAccel: "auto",
		accessed:       time.Now(),
	}
	for i := 0; i < 20; i++ {
		session.segments = append(session.segments, taterTVHLSSegment{
			Sequence:      int64(i),
			Duration:      2,
			Path:          fmt.Sprintf("item-%05d/seg-00000.ts", i),
			Discontinuity: i > 0 && i%4 == 0,
		})
	}
	playlist := session.playlist("token")

	if strings.Contains(playlist, "item-00000/seg-00000.ts") {
		t.Fatalf("playlist should not include stale early segments: %s", playlist)
	}
	if !strings.Contains(playlist, "#EXT-X-MEDIA-SEQUENCE:8") {
		t.Fatalf("expected playlist to start near live edge: %s", playlist)
	}
	if !strings.Contains(playlist, "#EXT-X-DISCONTINUITY-SEQUENCE:1") {
		t.Fatalf("expected dropped discontinuities to be counted: %s", playlist)
	}
	if !strings.Contains(playlist, "#EXT-X-START:TIME-OFFSET=-4.000,PRECISE=YES") {
		t.Fatalf("expected live-edge start hint: %s", playlist)
	}
}

func TestRunTaterTVChannelSegmentsSkipsFailedSegment(t *testing.T) {
	ffmpegPath := filepath.Join(t.TempDir(), "ffmpeg")
	script := `#!/bin/sh
case "$*" in
  *bad-commercial.mp4*) echo "bad commercial" >&2; exit 2 ;;
esac
printf "segment-ok"
`
	if err := os.WriteFile(ffmpegPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	recorder := httptest.NewRecorder()
	err := runTaterTVChannelSegments(
		ctx,
		ffmpegPath,
		config.TranscodingConfig{},
		transcodeProfiles["crt_480p"],
		"none",
		transcodeCodecH264,
		taterTVStreamWriter{w: recorder},
		nil,
		nil,
		taterTVChannel{Number: "02", Title: "Test"},
		[]taterTVStreamItem{
			{Title: "Bad Ad", Kind: "commercial", Path: "bad-commercial.mp4", DurationSeconds: 30, FullDuration: 30},
			{Title: "Next Video", Kind: "movie", Path: "next-video.mkv", DurationSeconds: 30, FullDuration: 30},
		},
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if body := recorder.Body.String(); body != "segment-ok" {
		t.Fatalf("expected successful segment output after failed commercial, got %q", body)
	}
}

func TestTaterTVProgramHLSSkipsFailedItem(t *testing.T) {
	ffmpegPath := filepath.Join(t.TempDir(), "ffmpeg")
	script := `#!/bin/sh
case "$*" in
  *bad-commercial.mp4*) echo "bad commercial" >&2; exit 2 ;;
esac
pattern=""
playlist=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-hls_segment_filename" ]; then pattern="$arg"; fi
  playlist="$arg"
  prev="$arg"
done
mkdir -p "$(dirname "$playlist")"
segment="$(printf "$pattern" 0)"
printf "segment-ok" > "$segment"
cat > "$playlist" <<EOF
#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4.000,
$(basename "$segment")
EOF
`
	if err := os.WriteFile(ffmpegPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	session := &taterTVHLSSession{
		number:     "02",
		ffmpegPath: ffmpegPath,
		profileID:  "crt_480p",
		profile:    transcodeProfiles["crt_480p"],
		accel:      "none",
		channel:    taterTVChannel{Number: "02", Title: "Test"},
		root:       t.TempDir(),
		seen:       map[string]bool{},
		accessed:   time.Now(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := session.transcodeProgramSegments(ctx, []taterTVStreamItem{
		{Title: "Bad Ad", Kind: "commercial", Path: "bad-commercial.mp4", DurationSeconds: 30, FullDuration: 30},
		{Title: "Next Video", Kind: "movie", Path: "next-video.mkv", DurationSeconds: 30, FullDuration: 30},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(session.segments) != 1 {
		t.Fatalf("expected one good HLS segment after failed item, got %#v", session.segments)
	}
	if !strings.Contains(session.segments[0].Path, "item-00001/seg-00000.ts") {
		t.Fatalf("expected segment from second item, got %#v", session.segments[0])
	}
}

func TestTaterTVProgramHLSStopsAfterSessionGoesIdle(t *testing.T) {
	tempDir := t.TempDir()
	ffmpegPath := filepath.Join(tempDir, "ffmpeg")
	startsPath := filepath.Join(tempDir, "starts")
	script := fmt.Sprintf(`#!/bin/sh
printf 'start\n' >> %q
exec sleep 30
`, startsPath)
	if err := os.WriteFile(ffmpegPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	session := &taterTVHLSSession{
		number:     "02",
		ffmpegPath: ffmpegPath,
		profileID:  "crt_480p",
		profile:    transcodeProfiles["crt_480p"],
		accel:      "none",
		channel:    taterTVChannel{Number: "02", Title: "Test"},
		root:       t.TempDir(),
		seen:       map[string]bool{},
		accessed:   time.Now().Add(-taterTVHLSIdleTimeout - time.Second),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := session.transcodeProgramSegments(ctx, []taterTVStreamItem{
		{Title: "First Video", Kind: "movie", Path: "first.mkv", DurationSeconds: 30, FullDuration: 30},
		{Title: "Second Video", Kind: "movie", Path: "second.mkv", DurationSeconds: 30, FullDuration: 30},
	}, ""); err != nil {
		t.Fatal(err)
	}

	starts, err := os.ReadFile(startsPath)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(starts), "start\n"); count != 1 {
		t.Fatalf("expected idle session to stop after one FFmpeg process, got %d starts", count)
	}
}
