package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

func TestTaterTVBrandBumpersPreferCustomRotation(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{Metadata: config.MetadataConfig{RootPath: root}}
	cfg.Transcoding.FFmpegPath = fakeFFmpegWithProbe(t, root, "#!/bin/sh\nprintf '7.500\\n'\n")

	customRoot := taterTVCustomBumperRoot(cfg)
	if err := os.MkdirAll(customRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(customRoot, "my-custom-bumper.mp4"), []byte("custom"), 0644); err != nil {
		t.Fatal(err)
	}

	bumpers := taterTVBrandBumpers(cfg)
	if len(bumpers) != 1 {
		t.Fatalf("expected only the custom rotation, got %d bumpers", len(bumpers))
	}
	if bumpers[0].Name != "my-custom-bumper.mp4" || bumpers[0].GroupID != taterTVCustomBumperGroupID {
		t.Fatalf("unexpected custom bumper: %#v", bumpers[0])
	}
	if _, _, err := taterTVResolveBrandBumper(cfg, taterTVBrandBumperDefinitions[0].Name); err == nil {
		t.Fatal("expected built-in bumper to be unavailable while the custom rotation is active")
	}

	if err := os.Remove(filepath.Join(customRoot, "my-custom-bumper.mp4")); err != nil {
		t.Fatal(err)
	}
	bumpers = taterTVBrandBumpers(cfg)
	if len(bumpers) != len(taterTVBrandBumperDefinitions) {
		t.Fatalf("expected built-ins to return after deleting the last custom bumper, got %d", len(bumpers))
	}
}

func TestTaterBumperStreamHandlerServesCustomBumper(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Metadata: config.MetadataConfig{RootPath: root},
		Players: config.PlayersConfig{Paired: []config.PlayerConfig{{
			ID:        "player-1",
			Name:      "Office TV",
			TokenHash: hashTaterSecret("custom-bumper-token"),
		}}},
	}
	cfg.Transcoding.FFmpegPath = fakeFFmpegWithProbe(t, root, "#!/bin/sh\nprintf '6.250\\n'\n")
	customRoot := taterTVCustomBumperRoot(cfg)
	if err := os.MkdirAll(customRoot, 0755); err != nil {
		t.Fatal(err)
	}
	contents := []byte("custom-tater-bumper")
	if err := os.WriteFile(filepath.Join(customRoot, "office-intro.mp4"), contents, 0644); err != nil {
		t.Fatal(err)
	}

	handler := NewTaterBumperStreamHandler(func() *config.Config { return cfg }, nil).GetHTTPHandler()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/tater/bumpers/file/office-intro.mp4?player_token=custom-bumper-token&transcode=0",
		nil,
	)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected custom bumper response 200, got %d: %s", res.Code, res.Body.String())
	}
	if got := res.Body.Bytes(); string(got) != string(contents) {
		t.Fatalf("unexpected custom bumper bytes: %q", got)
	}
}
