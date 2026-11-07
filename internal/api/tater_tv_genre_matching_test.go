package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

func TestTaterTVThemeMatchingPrefersStructuredGenres(t *testing.T) {
	cartoonTerms := []string{"animation", "animated", "anime", "cartoon", "cartoons"}

	animated := taterUsenetItem{
		Title:       "A Very Serious Feature",
		Genres:      []string{"Animation", "Family"},
		Description: "A dramatic story without cartoon keywords in its title.",
	}
	if !taterTVItemHasAnyTerm(animated, cartoonTerms) {
		t.Fatal("structured Animation genre should match the cartoon theme")
	}

	liveAction := taterUsenetItem{
		Title:       "Ordinary Drama",
		Genres:      []string{"Drama"},
		Description: "The children watch an animated cartoon after school.",
	}
	if taterTVItemHasAnyTerm(liveAction, cartoonTerms) {
		t.Fatal("plot keywords must not override a structured non-animation genre")
	}

	if taterTVItemHasAnyTerm(taterUsenetItem{Title: "Fatal Attraction"}, []string{"action"}) {
		t.Fatal("theme fallback should match whole words, not the action substring in attraction")
	}
}

func TestTaterTVThemedSourcesDoNotPromoteWholeSeriesFromEpisodePlots(t *testing.T) {
	source := taterTVSource{
		Title: "TV",
		Groups: []taterTVEpisodeGroup{
			{
				Title:  "Looney Tunes",
				Genres: []string{"Animation", "Comedy"},
				Episodes: []taterUsenetItem{{
					Title: "Rabbit Season", Description: "A classic short.",
				}},
			},
			{
				Title:  "Friends",
				Genres: []string{"Comedy"},
				Episodes: []taterUsenetItem{{
					Title: "The One with the Cartoon", Description: "The children watch animation.",
				}},
			},
			{
				Title:    "Married with Children",
				Genres:   []string{"Comedy"},
				Episodes: []taterUsenetItem{{Title: "Pilot"}},
			},
		},
	}

	themes := taterTVThemedSources(source)
	cartoon := taterTVTestThemeByTitle(themes, "CARTOON CHANNEL")
	if cartoon == nil || len(cartoon.Groups) != 1 || cartoon.Groups[0].Title != "Looney Tunes" {
		t.Fatalf("cartoon channel should contain only the animated series, got %#v", cartoon)
	}
	comedy := taterTVTestThemeByTitle(themes, "COMEDY CHANNEL")
	if comedy == nil || len(comedy.Groups) != 3 {
		t.Fatalf("comedy channel should retain all three comedies, got %#v", comedy)
	}
}

func TestTaterTVAddFileCarriesShowGenresIntoEpisodeGroup(t *testing.T) {
	source := taterTVSource{}
	show := taterUsenetItem{Title: "Duck Adventures", Genres: []string{"Animation", "Family"}}
	episode := taterUsenetItem{
		Title: "S01E01 Pilot", MediaType: "episode", Path: "Duck Adventures/Season 1/E01.mkv",
		Genres: []string{"Crime"},
	}

	taterTVAddFile(&source, episode, show)
	if len(source.Groups) != 1 {
		t.Fatalf("expected one episode group, got %#v", source.Groups)
	}
	if got := strings.Join(source.Groups[0].Genres, "|"); got != "Animation|Family" {
		t.Fatalf("show genres were not retained as the authoritative episode-group genres: %q", got)
	}
}

func TestTaterTVAttachShowMetadataReadsSeriesNFO(t *testing.T) {
	root := t.TempDir()
	showDir := filepath.Join(root, "Duck Adventures")
	if err := os.MkdirAll(showDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(showDir, "tvshow.nfo"), []byte(
		`<tvshow><title>Duck Adventures</title><genre>Animation</genre><genre>Comedy</genre></tvshow>`,
	), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{LocalMedia: config.LocalMediaConfig{Categories: []config.LocalMediaCategory{{
		ID: "tv", LibraryType: "tv", Paths: []string{root},
	}}}}
	show := taterUsenetItem{
		Title: "Duck Adventures", Type: "localFolder", MediaType: "show",
		CategoryID: "local:tv", SourceIndex: 0, Path: "Duck Adventures",
	}

	taterTVAttachShowMetadata(cfg, &show)
	if got := strings.Join(show.Genres, "|"); got != "Animation|Comedy" {
		t.Fatalf("series genres were not loaded from tvshow.nfo: %q", got)
	}
}

func taterTVTestThemeByTitle(sources []taterTVSource, title string) *taterTVSource {
	for index := range sources {
		if sources[index].Title == title {
			return &sources[index]
		}
	}
	return nil
}
