package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

type blockingResponseWriter struct {
	header  http.Header
	wrote   chan struct{}
	release chan struct{}
	once    sync.Once
	status  int
}

func newBlockingResponseWriter() *blockingResponseWriter {
	return &blockingResponseWriter{
		header:  make(http.Header),
		wrote:   make(chan struct{}),
		release: make(chan struct{}),
		status:  http.StatusOK,
	}
}

func (w *blockingResponseWriter) Header() http.Header {
	return w.header
}

func (w *blockingResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
}

func (w *blockingResponseWriter) Write(p []byte) (int, error) {
	w.once.Do(func() {
		close(w.wrote)
	})
	<-w.release
	return len(p), nil
}

func TestLocalStreamHandlerTracksActiveDirectStreams(t *testing.T) {
	root := t.TempDir()
	mediaPath := filepath.Join(root, "movie.mp4")
	if err := os.WriteFile(mediaPath, []byte("local media bytes"), 0644); err != nil {
		t.Fatal(err)
	}

	enabled := true
	cfg := &config.Config{
		LocalMedia: config.LocalMediaConfig{
			Enabled: &enabled,
			Categories: []config.LocalMediaCategory{
				{
					ID:          "movies",
					Name:        "Movies",
					LibraryType: "movies",
					Paths:       []string{root},
					Enabled:     &enabled,
				},
			},
		},
		Players: config.PlayersConfig{
			Paired: []config.PlayerConfig{
				{
					ID:        "player-1",
					Name:      "Living Room",
					TokenHash: hashTaterSecret("local-token"),
				},
			},
		},
	}

	tracker := NewStreamTracker(nil)
	defer tracker.Stop()
	handler := NewLocalStreamHandler(func() *config.Config { return cfg }, tracker).GetHTTPHandler()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/tater/local/stream?category_id=movies&source=0&path=movie.mp4&player_token=local-token",
		nil,
	)
	res := newBlockingResponseWriter()
	done := make(chan struct{})

	go func() {
		handler.ServeHTTP(res, req)
		close(done)
	}()

	select {
	case <-res.wrote:
	case <-time.After(2 * time.Second):
		t.Fatal("local stream did not start writing")
	}

	streams := tracker.GetAll()
	if len(streams) != 1 {
		t.Fatalf("expected one active local stream, got %d: %#v", len(streams), streams)
	}
	if streams[0].Source != "Local" {
		t.Fatalf("expected Local source, got %q", streams[0].Source)
	}
	if streams[0].UserName != "Living Room" {
		t.Fatalf("expected player name in active stream, got %q", streams[0].UserName)
	}
	if streams[0].PlayerID != "player-1" {
		t.Fatalf("expected player ID in active stream, got %q", streams[0].PlayerID)
	}
	if streams[0].BytesSent <= 0 {
		t.Fatalf("expected bytes sent to be tracked, got %d", streams[0].BytesSent)
	}

	close(res.release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("local stream did not finish")
	}
	if active := tracker.GetAll(); len(active) != 0 {
		t.Fatalf("expected local stream cleanup, got %#v", active)
	}
}
