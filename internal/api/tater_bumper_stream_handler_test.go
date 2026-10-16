package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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

func TestTaterBumperStreamHandlerServesAppleTVHLSPlaylistAndRangeSegments(t *testing.T) {
	root := t.TempDir()
	ffmpegPath := filepath.Join(root, "fake-ffmpeg")
	ffmpegScript := `#!/bin/sh
pattern=""
playlist=""
previous=""
for argument in "$@"; do
  if [ "$previous" = "-hls_segment_filename" ]; then pattern="$argument"; fi
  previous="$argument"
  playlist="$argument"
done
segment=$(printf "$pattern" 0)
printf 'segment bytes' > "$segment"
name=$(basename "$segment")
printf '#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4.0,\n%s\n#EXT-X-ENDLIST\n' "$name" > "$playlist"
`
	if err := os.WriteFile(ffmpegPath, []byte(ffmpegScript), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Metadata:    config.MetadataConfig{RootPath: filepath.Join(root, "metadata")},
		Transcoding: config.TranscodingConfig{FFmpegPath: ffmpegPath},
		Players: config.PlayersConfig{Paired: []config.PlayerConfig{{
			ID:        "apple-tv",
			Name:      "Office TV",
			TokenHash: hashTaterSecret("bumper-token"),
		}}},
	}
	handler := NewTaterBumperStreamHandler(func() *config.Config { return cfg }, nil).GetHTTPHandler()
	playlistRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/tater/bumpers/file/etches-tater-tube-logo.mp4?player_token=bumper-token&transcode=audio&tater_output_container=hls&tater_audio_channels=2&tater_audio_codec=aac",
		nil,
	)
	playlistResponse := httptest.NewRecorder()
	handler.ServeHTTP(playlistResponse, playlistRequest)
	if playlistResponse.Code != http.StatusOK {
		t.Fatalf("expected HLS playlist, got %d: %s", playlistResponse.Code, playlistResponse.Body.String())
	}
	if playlistResponse.Header().Get("Content-Type") != "application/vnd.apple.mpegurl" {
		t.Fatalf("unexpected playlist content type %q", playlistResponse.Header().Get("Content-Type"))
	}

	var segmentURL string
	for _, line := range strings.Split(playlistResponse.Body.String(), "\n") {
		if strings.HasPrefix(line, "/api/tater/bumpers/file/") {
			segmentURL = line
			break
		}
	}
	if segmentURL == "" {
		t.Fatalf("expected rewritten bumper segment URL in %s", playlistResponse.Body.String())
	}
	parsed, err := url.Parse(segmentURL)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := parsed.Query().Get(taterLocalHLSSessionQuery)
	defer func() {
		if session := globalTaterLocalHLS.get(sessionID); session != nil {
			globalTaterLocalHLS.removeIfSame(sessionID, session)
			session.stopAndCleanup()
		}
	}()

	segmentRequest := httptest.NewRequest(http.MethodGet, segmentURL, nil)
	segmentRequest.Header.Set("Range", "bytes=0-6")
	segmentResponse := httptest.NewRecorder()
	handler.ServeHTTP(segmentResponse, segmentRequest)
	if segmentResponse.Code != http.StatusPartialContent {
		t.Fatalf("expected ranged HLS segment, got %d: %s", segmentResponse.Code, segmentResponse.Body.String())
	}
	if segmentResponse.Body.String() != "segment" {
		t.Fatalf("unexpected segment body %q", segmentResponse.Body.String())
	}
	if segmentResponse.Header().Get("Content-Length") == "" {
		t.Fatal("expected bumper HLS segment to advertise a content length")
	}
	if segmentResponse.Header().Get("Content-Range") != "bytes 0-6/13" {
		t.Fatalf("unexpected segment content range %q", segmentResponse.Header().Get("Content-Range"))
	}
}
