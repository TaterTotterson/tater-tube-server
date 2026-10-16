package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

// serveBumperHLSPlaylist gives standalone Tater Bumpers the same finite,
// range-capable HLS transport used by local and Discovery playback. AVPlayer
// cannot consume the open-ended progressive transcode used by other clients.
func (h *TaterBumperStreamHandler) serveBumperHLSPlaylist(
	w http.ResponseWriter,
	r *http.Request,
	cfg *config.Config,
	player *config.PlayerConfig,
	playerToken string,
	bumper taterTVBumper,
	path string,
	info os.FileInfo,
) {
	session, err := h.prepareBumperHLSSession(r, cfg, player, playerToken, bumper, path, info)
	if err != nil {
		slog.ErrorContext(r.Context(), "Unable to prepare Tater Bumper HLS playback", "error", err)
		http.Error(w, "Unable to prepare Apple TV bumper playback", http.StatusServiceUnavailable)
		return
	}

	requiredSegments := taterLocalHLSStartupSegments(r)
	deadline := time.Now().Add(taterLocalHLSFirstWait)
	for time.Now().Before(deadline) {
		if session.playlistBuffered(requiredSegments) || session.finished() {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	playlist, err := session.playlist()
	if err != nil {
		if failure := session.failure(); failure != nil {
			slog.ErrorContext(r.Context(), "Tater Bumper HLS playback failed", "error", failure)
		}
		http.Error(w, "Apple TV bumper playback did not become ready", http.StatusServiceUnavailable)
		return
	}

	session.touch()
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(playlist)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(playlist)
}

func (h *TaterBumperStreamHandler) prepareBumperHLSSession(
	r *http.Request,
	cfg *config.Config,
	player *config.PlayerConfig,
	playerToken string,
	bumper taterTVBumper,
	path string,
	info os.FileInfo,
) (*taterLocalHLSSession, error) {
	if player == nil {
		return nil, fmt.Errorf("player is unavailable")
	}
	ffmpegPath := effectiveFFmpegPath(cfg.Transcoding.FFmpegPath)
	if _, err := exec.LookPath(ffmpegPath); err != nil {
		return nil, fmt.Errorf("ffmpeg is unavailable: %w", err)
	}

	key := taterLocalHLSKey(r, player.ID, path)
	if globalTaterLocalHLS.isSuperseded(key) {
		return nil, fmt.Errorf("HLS playback generation was superseded")
	}
	globalTaterTVHLS.unbindPlayer(player.ID)
	if existing := globalTaterLocalHLS.get(key); existing != nil {
		if !existing.finished() || (existing.failure() == nil && existing.playlistReady()) {
			existing.touch()
			return existing, nil
		}
		globalTaterLocalHLS.removeIfSame(key, existing)
		existing.stopAndCleanup()
	}

	root := filepath.Join(taterTVHLSRoot(cfg), fmt.Sprintf("bumper-%s-%d", key, time.Now().UnixNano()))
	if err := os.RemoveAll(root); err != nil {
		return nil, fmt.Errorf("clear bumper HLS working directory: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create bumper HLS working directory: %w", err)
	}
	playlistPath := filepath.Join(root, "index.m3u8")
	segmentPattern := filepath.Join(root, "segment-%06d.ts")
	command, err := buildTaterLocalHLSCommand(r, cfg, path, playlistPath, segmentPattern)
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	session := &taterLocalHLSSession{
		id:           key,
		slot:         taterLocalHLSSlot(player.ID),
		playerID:     player.ID,
		playerToken:  playerToken,
		playlistURL:  r.URL.Path,
		root:         root,
		playlistPath: playlistPath,
		cancel:       cancel,
		tracker:      h.streamTracker,
		accessed:     time.Now(),
	}
	session, created := globalTaterLocalHLS.addOrReplace(key, session)
	if !created {
		cancel()
		_ = os.RemoveAll(root)
		session.touch()
		return session, nil
	}

	if h.streamTracker != nil {
		session.stream = h.streamTracker.AddStream(
			bumper.Title,
			"Tater Bumper",
			taterPlayerDisplayName(player),
			r.RemoteAddr,
			r.UserAgent(),
			info.Size(),
		)
		h.streamTracker.SetPlayerID(session.stream.ID, player.ID)
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
			session.stream.ID,
			command.videoMode,
			command.audioMode,
			command.audioCodec,
			"Streaming HLS",
		)
		h.streamTracker.SetAudioChannelInfo(session.stream.ID, command.audioChannels)
		h.streamTracker.SetMediaInfo(session.stream.ID, bumper.Duration, 0)
		applyTaterRequestedDynamicRangeInfo(h.streamTracker, session.stream.ID, r)
		applyTaterRequestedResolutionInfo(h.streamTracker, session.stream.ID, r)
	}

	go session.run(ctx, ffmpegPath, command.args)
	go session.expireWhenIdle(ctx)
	return session, nil
}

func (h *TaterBumperStreamHandler) serveBumperHLSSegment(
	w http.ResponseWriter,
	r *http.Request,
	player *config.PlayerConfig,
) {
	sessionID := strings.TrimSpace(r.URL.Query().Get(taterLocalHLSSessionQuery))
	segmentName := filepath.Base(strings.TrimSpace(r.URL.Query().Get(taterLocalHLSSegmentQuery)))
	contentType, supported := taterHLSSegmentContentType(segmentName)
	if sessionID == "" || segmentName == "." || segmentName == "" || !supported {
		http.Error(w, "HLS segment not found", http.StatusNotFound)
		return
	}
	session := globalTaterLocalHLS.get(sessionID)
	if session == nil || player == nil || session.playerID != player.ID {
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

	session.markSegmentRequested(segmentName)
	session.touch()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("Accept-Ranges", "bytes")
	var writer http.ResponseWriter = w
	if session.tracker != nil && session.stream != nil {
		writer = &trackedResponseWriter{
			ResponseWriter: w,
			stream:         session.stream,
			streamTracker:  session.tracker,
		}
	}
	http.ServeContent(writer, r, segmentName, stat.ModTime(), file)
}
