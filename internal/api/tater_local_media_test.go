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

	commercialRoot := filepath.Join(configDir, "commercials")
	commercialDir := filepath.Join(commercialRoot, "cartoon-network")
	if err := os.MkdirAll(commercialDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commercialDir, "Snack.Ad.mp4"), []byte("ad"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig(configDir)
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
	cfg.TubeTV.CommercialsPath = commercialRoot
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
	if !strings.Contains(channels[0].StreamURL, "/api/tater/tv/channel/02/stream") {
		t.Fatalf("expected channel stream URL, got %q", channels[0].StreamURL)
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

func TestTaterTVConcatPlanStartsAtLivePosition(t *testing.T) {
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
	commercialRoot := filepath.Join(configDir, "commercials")
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
	cfg.TubeTV.CommercialsPath = commercialRoot

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
	items, err := taterTVResolveConcatItems(cfg, channel, startedAt, time.Now(), 90)
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

	concatPath, cleanup, err := taterTVWriteConcatFile(items)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	data, err := os.ReadFile(concatPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := string(data)
	if !strings.Contains(manifest, "ffconcat version 1.0") {
		t.Fatalf("missing concat header: %s", manifest)
	}
	if !strings.Contains(manifest, "That Movie'\\''s Cut.mkv") {
		t.Fatalf("expected quoted apostrophe in manifest: %s", manifest)
	}
	if !strings.Contains(manifest, "inpoint 45.") || !strings.Contains(manifest, "outpoint 120.") {
		t.Fatalf("expected live in/out points in manifest: %s", manifest)
	}
	if !strings.Contains(manifest, commercialPath) {
		t.Fatalf("expected commercial in manifest: %s", manifest)
	}
}
