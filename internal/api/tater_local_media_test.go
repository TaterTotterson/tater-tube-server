package api

import (
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
