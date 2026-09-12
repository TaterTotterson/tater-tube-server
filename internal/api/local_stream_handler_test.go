package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

func TestConvertTaterFFmpegArgsToHLSUsesSegmentedOutput(t *testing.T) {
	args := buildFFmpegAudioOnlyVideoArgsWithTrackAndContainer("192k", "/media/movie.mkv", 0, 0, "mpegts")
	args = convertTaterFFmpegArgsToHLS(args, "/tmp/local/index.m3u8", "/tmp/local/segment-%06d.ts", false, "copy")
	joined := strings.Join(args, " ")

	if strings.Contains(joined, "-f mpegts pipe:1") {
		t.Fatalf("expected progressive MPEG-TS output to be replaced: %s", joined)
	}
	for _, expected := range []string{
		"-readrate 1 -readrate_initial_burst 8 -i /media/movie.mkv",
		"-f hls",
		"-hls_time 4",
		"-hls_playlist_type event",
		"-hls_segment_filename /tmp/local/segment-%06d.ts",
		"/tmp/local/index.m3u8",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected %q in HLS args: %s", expected, joined)
		}
	}
}

func TestTaterLocalHLSPlaylistUsesAuthenticatedSegmentURLs(t *testing.T) {
	root := t.TempDir()
	playlistPath := filepath.Join(root, "index.m3u8")
	if err := os.WriteFile(playlistPath, []byte("#EXTM3U\n#EXTINF:4.0,\nsegment-000001.ts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := &taterLocalHLSSession{
		id: "session-one", playerToken: "paired token", root: root, playlistPath: playlistPath,
	}
	playlist, err := session.playlist()
	if err != nil {
		t.Fatal(err)
	}
	text := string(playlist)
	if !strings.Contains(text, "/api/tater/local/stream?") ||
		!strings.Contains(text, "player_token=paired+token") ||
		!strings.Contains(text, "tater_hls_session=session-one") ||
		!strings.Contains(text, "tater_hls_segment=segment-000001.ts") {
		t.Fatalf("expected authenticated local HLS segment URL, got %s", text)
	}
}

func TestLocalStreamHandlerServesAppleTVHLSPlaylistAndSegments(t *testing.T) {
	root := t.TempDir()
	mediaPath := filepath.Join(root, "movie.mkv")
	if err := os.WriteFile(mediaPath, []byte("local media bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
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

	enabled := true
	cfg := &config.Config{
		Metadata:    config.MetadataConfig{RootPath: filepath.Join(root, "metadata")},
		Transcoding: config.TranscodingConfig{FFmpegPath: ffmpegPath},
		LocalMedia: config.LocalMediaConfig{
			Enabled: &enabled,
			Categories: []config.LocalMediaCategory{{
				ID: "movies", Name: "Movies", LibraryType: "movies", Paths: []string{root}, Enabled: &enabled,
			}},
		},
		Players: config.PlayersConfig{Paired: []config.PlayerConfig{{
			ID: "apple-tv", Name: "Living Room", TokenHash: hashTaterSecret("local-token"),
		}}},
	}
	handler := NewLocalStreamHandler(func() *config.Config { return cfg }, nil).GetHTTPHandler()
	playlistRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/tater/local/stream?category_id=movies&source=0&path=movie.mkv&player_token=local-token&transcode=remux&tater_output_container=hls",
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
		if strings.HasPrefix(line, "/api/tater/local/stream?") {
			segmentURL = line
			break
		}
	}
	if segmentURL == "" {
		t.Fatalf("expected rewritten segment URL in %s", playlistResponse.Body.String())
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
		t.Fatalf("expected HLS segment, got %d: %s", segmentResponse.Code, segmentResponse.Body.String())
	}
	if segmentResponse.Body.String() != "segment" {
		t.Fatalf("unexpected HLS segment body %q", segmentResponse.Body.String())
	}
	if segmentResponse.Header().Get("Content-Length") == "" {
		t.Fatal("expected HLS segment to advertise a content length")
	}
	if segmentResponse.Header().Get("Content-Range") != "bytes 0-6/13" {
		t.Fatalf("unexpected HLS content range %q", segmentResponse.Header().Get("Content-Range"))
	}
}

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

func TestTaterTVItemHandlerServesScheduledMediaDirectly(t *testing.T) {
	taterTVResetGuide()
	root := t.TempDir()
	mediaPath := filepath.Join(root, "Scheduled Movie.mp4")
	if err := os.WriteFile(mediaPath, []byte("scheduled media bytes"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig(root)
	cfg.Transcoding.FFmpegPath = fakeFFmpegWithProbe(t, root, "#!/bin/sh\nprintf '120.000\\n'\n")
	cfg.LocalMedia.Enabled = boolPtr(true)
	cfg.LocalMedia.Categories = []config.LocalMediaCategory{{
		ID:          "movies",
		Name:        "Movies",
		LibraryType: "movies",
		Paths:       []string{root},
		Enabled:     boolPtr(true),
	}}
	cfg.TubeTV.AutoChannels = boolPtr(false)
	cfg.TubeTV.CustomChannels = []config.TubeTVCustomChannel{{
		ID:    "movies",
		Title: "Movie Channel",
		Sources: []config.TubeTVCustomSource{{
			CategoryID:  "movies",
			SourceIndex: -1,
		}},
	}}
	cfg.Players.Paired = []config.PlayerConfig{{
		ID:        "player-1",
		Name:      "Living Room",
		TokenHash: hashTaterSecret("item-token"),
	}}
	defer taterTVResetGuideForConfig(cfg)

	handler := NewTaterTVStreamHandler(func() *config.Config { return cfg }, nil).GetHTTPHandler()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/tater/tv/channel/02/item/0?player_token=item-token&transcode=0",
		nil,
	)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected scheduled item response 200, got %d: %s", res.Code, res.Body.String())
	}
	if res.Body.String() != "scheduled media bytes" {
		t.Fatalf("unexpected scheduled item body: %q", res.Body.String())
	}
}
