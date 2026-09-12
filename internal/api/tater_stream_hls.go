package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

// serveStreamHLSPlaylist gives prepared NZB media the same segmented HLS
// transport used by local media. Apple TV cannot reliably consume an
// open-ended, chunked MPEG-TS response, but it can consume these finite
// range-capable segments.
func (h *StreamHandler) serveStreamHLSPlaylist(
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
	path string,
	userName string,
	playerID string,
) {
	if h.configGetter == nil || h.nzbFilesystem == nil || playerID == "" {
		http.Error(w, "HLS playback is unavailable", http.StatusServiceUnavailable)
		return
	}
	cfg := h.configGetter()
	if cfg == nil {
		http.Error(w, "HLS playback is unavailable", http.StatusServiceUnavailable)
		return
	}
	playerToken := taterStreamPlayerToken(r)
	if playerToken == "" {
		http.Error(w, "HLS playback authorization is unavailable", http.StatusUnauthorized)
		return
	}
	info, err := h.nzbFilesystem.Stat(ctx, path)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to get file information", http.StatusInternalServerError)
		return
	}
	if info.IsDir() {
		http.Error(w, "Cannot stream directory", http.StatusBadRequest)
		return
	}

	session, err := h.prepareStreamHLSSession(r, cfg, path, info.Size(), userName, playerID, playerToken)
	if err != nil {
		http.Error(w, "Unable to prepare Apple TV playback", http.StatusServiceUnavailable)
		return
	}

	deadline := time.Now().Add(taterLocalHLSFirstWait)
	for time.Now().Before(deadline) {
		if session.playlistReady() || session.finished() {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	playlist, err := session.playlist()
	if err != nil {
		http.Error(w, "Apple TV playback did not become ready", http.StatusServiceUnavailable)
		return
	}
	session.touch()
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(playlist)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(playlist)
}

func (h *StreamHandler) prepareStreamHLSSession(
	r *http.Request,
	cfg *config.Config,
	path string,
	size int64,
	userName string,
	playerID string,
	playerToken string,
) (*taterLocalHLSSession, error) {
	ffmpegPath := effectiveFFmpegPath(cfg.Transcoding.FFmpegPath)
	if _, err := exec.LookPath(ffmpegPath); err != nil {
		return nil, fmt.Errorf("ffmpeg is unavailable: %w", err)
	}
	inputURL, err := taterStreamHLSInputURL(r, cfg, path, playerToken)
	if err != nil {
		return nil, err
	}

	key := taterLocalHLSKey(r, playerID, path)
	if existing := globalTaterLocalHLS.get(key); existing != nil {
		if !existing.finished() || (existing.failure() == nil && existing.playlistReady()) {
			existing.touch()
			return existing, nil
		}
		globalTaterLocalHLS.removeIfSame(key, existing)
		existing.stopAndCleanup()
	}

	root := filepath.Join(taterTVHLSRoot(cfg), fmt.Sprintf("stream-%s-%d", key, time.Now().UnixNano()))
	if err := os.RemoveAll(root); err != nil {
		return nil, fmt.Errorf("clear HLS working directory: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create HLS working directory: %w", err)
	}
	playlistPath := filepath.Join(root, "index.m3u8")
	segmentPattern := filepath.Join(root, "segment-%06d.ts")
	command, err := buildTaterLocalHLSCommand(r, cfg, inputURL, playlistPath, segmentPattern)
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}

	sessionCtx, cancel := context.WithCancel(context.Background())
	session := &taterLocalHLSSession{
		id:           key,
		playerID:     playerID,
		playerToken:  playerToken,
		playlistURL:  r.URL.Path,
		root:         root,
		playlistPath: playlistPath,
		cancel:       cancel,
		tracker:      h.streamTracker,
		accessed:     time.Now(),
	}
	session, created := globalTaterLocalHLS.addOrGet(key, session)
	if !created {
		cancel()
		_ = os.RemoveAll(root)
		session.touch()
		return session, nil
	}

	if h.streamTracker != nil {
		session.stream = h.streamTracker.AddStream(path, "Discovery", userName, r.RemoteAddr, r.UserAgent(), size)
		h.streamTracker.SetPlayerID(session.stream.ID, playerID)
		h.streamTracker.SetCancelFunc(session.stream.ID, cancel)
		h.streamTracker.SetTranscodingInfo(
			session.stream.ID,
			command.profileID,
			command.profileName,
			command.effectiveAccel,
			command.hardwareDevice,
			command.videoCodec,
			command.effectiveAccel != "" && command.effectiveAccel != "none",
		)
		h.streamTracker.SetTrackProcessingInfo(
			session.stream.ID, command.videoMode, command.audioMode, command.audioCodec, "Streaming HLS",
		)
		applyTaterRequestedDynamicRangeInfo(h.streamTracker, session.stream.ID, r)
		applyTaterRequestedResolutionInfo(h.streamTracker, session.stream.ID, r)
	}

	go session.run(sessionCtx, ffmpegPath, command.args)
	go session.expireWhenIdle(sessionCtx)
	return session, nil
}

func (h *StreamHandler) serveStreamHLSSegment(w http.ResponseWriter, r *http.Request, playerID string) {
	sessionID := strings.TrimSpace(r.URL.Query().Get(taterLocalHLSSessionQuery))
	segmentName := filepath.Base(strings.TrimSpace(r.URL.Query().Get(taterLocalHLSSegmentQuery)))
	if sessionID == "" || segmentName == "." || segmentName == "" || !strings.HasSuffix(strings.ToLower(segmentName), ".ts") {
		http.Error(w, "HLS segment not found", http.StatusNotFound)
		return
	}
	session := globalTaterLocalHLS.get(sessionID)
	if session == nil || playerID == "" || session.playerID != playerID {
		http.Error(w, "HLS session not found", http.StatusNotFound)
		return
	}
	segmentPath, err := safeLocalPath(session.root, segmentName)
	if err != nil {
		http.Error(w, "Invalid HLS segment", http.StatusBadRequest)
		return
	}
	file, err := os.Open(segmentPath)
	if err != nil {
		http.Error(w, "HLS segment not found", http.StatusNotFound)
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || stat.IsDir() {
		http.Error(w, "HLS segment not found", http.StatusNotFound)
		return
	}

	session.touch()
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("Accept-Ranges", "bytes")
	var writer http.ResponseWriter = w
	if session.tracker != nil && session.stream != nil {
		writer = &trackedResponseWriter{ResponseWriter: w, stream: session.stream, streamTracker: session.tracker}
	}
	http.ServeContent(writer, r, segmentName, stat.ModTime(), file)
}

func taterStreamPlayerToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	if token := strings.TrimSpace(r.URL.Query().Get("player_token")); token != "" {
		return token
	}
	if token := bearerToken(r.Header.Get("Authorization")); token != "" {
		return token
	}
	return strings.TrimSpace(r.Header.Get("X-Tater-Player-Token"))
}

func taterStreamHLSInputURL(r *http.Request, cfg *config.Config, path, playerToken string) (string, error) {
	if r == nil || r.URL == nil || cfg == nil || cfg.Server.Port <= 0 {
		return "", fmt.Errorf("server playback address is unavailable")
	}
	u := url.URL{
		Scheme: "http",
		Host:   "127.0.0.1:" + strconv.Itoa(cfg.Server.Port),
		Path:   r.URL.Path,
	}
	query := u.Query()
	query.Set("path", path)
	query.Set("player_token", playerToken)
	query.Set("direct", "1")
	query.Set(taterInternalTranscodeInputQuery, "1")
	u.RawQuery = query.Encode()
	return u.String(), nil
}
