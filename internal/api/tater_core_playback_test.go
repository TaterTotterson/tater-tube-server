package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

func TestEchoPlaybackProfilesBoundResolutionFrameRateAndBitrate(t *testing.T) {
	tests := []struct {
		id            string
		width, height int
	}{
		{"echo_show_720p", 1280, 720},
		{"echo_checkers_480p", 854, 480},
		{"echo_rook_480p", 480, 480},
	}
	for _, test := range tests {
		profile, ok := transcodeProfiles[test.id]
		if !ok {
			t.Fatalf("missing profile %s", test.id)
		}
		if profile.MaxWidth != test.width || profile.MaxHeight != test.height || profile.MaxFrameRate != 30 {
			t.Fatalf("%s bounds = %#v", test.id, profile)
		}
		_, filters := transcodeVideoSettings("none", "", profile)
		if !strings.Contains(filters, "fps=30") {
			t.Fatalf("%s filters do not cap frame rate: %s", test.id, filters)
		}
	}
}

func TestSelectTaterCorePlaybackCandidatePrefersExactTitle(t *testing.T) {
	candidates := []taterCandidate{
		{ID: "extended", Title: "The Movie Extended", Source: "local_media"},
		{ID: "exact", Title: "The Movie", Source: "local_media"},
	}
	selected, ok := selectTaterCorePlaybackCandidate(candidates, "", "the movie")
	if !ok || selected.ID != "exact" {
		t.Fatalf("selected %#v, ok=%v", selected, ok)
	}
}

func TestResolveTaterCoreSeriesChoosesFirstEpisode(t *testing.T) {
	root := t.TempDir()
	showDir := filepath.Join(root, "A Show", "Season 01")
	if err := os.MkdirAll(showDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"A.Show.S01E02.mkv", "A.Show.S01E01.mkv"} {
		if err := os.WriteFile(filepath.Join(showDir, name), []byte("media"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.DefaultConfig(t.TempDir())
	cfg.LocalMedia.Categories = []config.LocalMediaCategory{{
		ID: "tv", Name: "TV", LibraryType: "tv", Paths: []string{root},
	}}
	item, subtitle, err := resolveTaterCorePlaybackItem(cfg, taterCandidate{
		ID: "show", Title: "A Show", Source: "local_media",
		Launch: taterUsenetItem{
			Type: "localFolder", MediaType: "show", CategoryID: "local:tv", Path: "A Show",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if subtitle != "A Show" || !strings.Contains(item.Path, "S01E01") || item.SeriesStateID == "" {
		t.Fatalf("resolved item = %#v, subtitle=%q", item, subtitle)
	}
}

func TestCorePlaybackStreamGrantIsScopedToResolvedPath(t *testing.T) {
	server := &Server{}
	item := taterUsenetItem{
		Type: "localFile", MediaType: "movie", CategoryID: "local:movies",
		SourceIndex: 1, Path: "Movie/Movie.mkv", VideoRange: "hdr10",
	}
	grant := &taterCorePlaybackGrant{
		PlaybackID: "play-1", StreamHash: hashTaterSecret("stream-secret"),
		CoreID: "core-1", CoreName: "Tater", Item: item, ExpiresAt: time.Now().Add(time.Hour),
	}
	server.storeTaterCorePlaybackGrant(grant, "stream-secret")
	player, ok := server.AuthorizeTaterCorePlaybackStream(
		context.Background(), "stream-secret", "movies", 1, "Movie/Movie.mkv",
	)
	if !ok || player.ID != "tater-core-core-1" {
		t.Fatalf("grant authorization = %#v, ok=%v", player, ok)
	}
	if _, ok := server.AuthorizeTaterCorePlaybackStream(
		context.Background(), "stream-secret", "movies", 1, "Movie/Other.mkv",
	); ok {
		t.Fatal("grant authorized a different media path")
	}
	streamURL := taterCorePlaybackStreamURL(
		"http://tube.local", "stream-secret", "echo_show_720p", item,
	)
	for _, expected := range []string{
		"core_stream_token=stream-secret", "profile=echo_show_720p", "transcode=1",
		"codec=h264", "tater_audio_channels=2", "tater_tone_map=1",
	} {
		if !strings.Contains(streamURL, expected) {
			t.Fatalf("stream URL %q missing %q", streamURL, expected)
		}
	}
	if strings.Contains(streamURL, "player_token=") {
		t.Fatalf("core stream exposed a player token: %s", streamURL)
	}
}
