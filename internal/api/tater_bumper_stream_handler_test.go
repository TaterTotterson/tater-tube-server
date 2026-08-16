package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

func TestTaterBumperStreamHandlerServesBuiltInBumper(t *testing.T) {
	cfg := &config.Config{
		Metadata: config.MetadataConfig{
			RootPath: t.TempDir(),
		},
		Players: config.PlayersConfig{
			Paired: []config.PlayerConfig{{
				ID:        "player-1",
				Name:      "Original Xbox",
				TokenHash: hashTaterSecret("bumper-token"),
			}},
		},
	}

	handler := NewTaterBumperStreamHandler(func() *config.Config { return cfg }, nil).GetHTTPHandler()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/tater/bumpers/file/etches-tater-tube-logo.mp4?player_token=bumper-token&transcode=0",
		nil,
	)
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected bumper response 200, got %d: %s", res.Code, res.Body.String())
	}
	if contentType := res.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "video/") {
		t.Fatalf("expected video content type, got %q", contentType)
	}
	if res.Body.Len() == 0 {
		t.Fatal("expected bumper body bytes")
	}
}

func TestTaterBumperFileURLIncludesPlayerToken(t *testing.T) {
	u := taterBumperFileURL("http://tube.local", "etches-tater-tube-logo.mp4", "secret")
	if !strings.Contains(u, "/api/tater/bumpers/file/etches-tater-tube-logo.mp4") {
		t.Fatalf("expected built-in bumper path, got %q", u)
	}
	if !strings.Contains(u, "player_token=secret") {
		t.Fatalf("expected player token query, got %q", u)
	}
}
