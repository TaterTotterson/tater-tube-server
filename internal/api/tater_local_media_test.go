package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	items, err := taterLocalMovieItems(config.LocalMediaCategory{ID: "movies", Name: "Movies"}, []string{root}, "http://server", "token")
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
	shows, err := taterLocalTVItems(cat, []string{root}, "http://server", "token", -1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(shows) != 1 || shows[0].Title != "Some Show" || shows[0].MediaType != "show" {
		t.Fatalf("unexpected show rows: %#v", shows)
	}

	seasons, err := taterLocalTVItems(cat, []string{root}, "http://server", "token", 0, "Some.Show.2020")
	if err != nil {
		t.Fatal(err)
	}
	if len(seasons) != 1 || seasons[0].Title != "Season 1" || seasons[0].MediaType != "season" {
		t.Fatalf("unexpected season rows: %#v", seasons)
	}

	episodes, err := taterLocalTVItems(cat, []string{root}, "http://server", "token", 0, "Some.Show.2020/Season 01")
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 1 || episodes[0].Title != "S01E02 The One" || episodes[0].MediaType != "episode" {
		t.Fatalf("unexpected episode rows: %#v", episodes)
	}
}
