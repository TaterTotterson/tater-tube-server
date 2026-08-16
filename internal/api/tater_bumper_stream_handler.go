package api

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/TaterTotterson/tater-tube-server/internal/nzbfilesystem"
)

// TaterBumperStreamHandler serves built-in Tater Tube bumpers to paired players.
type TaterBumperStreamHandler struct {
	configGetter  config.ConfigGetter
	streamTracker *StreamTracker
}

func NewTaterBumperStreamHandler(configGetter config.ConfigGetter, streamTracker *StreamTracker) *TaterBumperStreamHandler {
	return &TaterBumperStreamHandler{configGetter: configGetter, streamTracker: streamTracker}
}

func (h *TaterBumperStreamHandler) GetHTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.configGetter == nil {
			http.Error(w, "Configuration unavailable", http.StatusServiceUnavailable)
			return
		}
		cfg := h.configGetter()
		if cfg == nil {
			http.Error(w, "Configuration unavailable", http.StatusServiceUnavailable)
			return
		}

		token := strings.TrimSpace(r.URL.Query().Get("player_token"))
		if token == "" {
			token = bearerToken(r.Header.Get("Authorization"))
		}
		if token == "" {
			token = strings.TrimSpace(r.Header.Get("X-Tater-Player-Token"))
		}
		player, ok := findTaterPlayerByToken(cfg, token)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="Tater Bumper Stream"`)
			http.Error(w, "Unauthorized: valid player_token required", http.StatusUnauthorized)
			return
		}

		name := taterBumperNameFromPath(r.URL.Path)
		definition, ok := taterBumperDefinitionByName(name)
		if !ok {
			http.Error(w, "Bumper not found", http.StatusNotFound)
			return
		}
		path, err := taterTVEnsureBuiltInBumperFile(cfg, definition.Name)
		if err != nil {
			http.Error(w, "Bumper unavailable", http.StatusServiceUnavailable)
			return
		}

		file, err := os.Open(path)
		if err != nil {
			http.Error(w, "Bumper unavailable", http.StatusNotFound)
			return
		}
		defer file.Close()

		info, err := file.Stat()
		if err != nil || info.IsDir() {
			http.Error(w, "Bumper unavailable", http.StatusNotFound)
			return
		}

		streamReq := r
		var cleanup func()
		var streamWriter http.ResponseWriter = w
		var stream *nzbfilesystem.ActiveStream
		transcoder := &StreamHandler{configGetter: h.configGetter, streamTracker: h.streamTracker}
		if h.streamTracker != nil {
			streamCtx, cancel := context.WithCancel(r.Context())
			streamReq = r.WithContext(streamCtx)
			playerName := taterPlayerDisplayName(player)
			stream = h.streamTracker.AddStream(definition.Title, "Tater Bumper", playerName, r.RemoteAddr, r.UserAgent(), info.Size())
			h.streamTracker.SetPlayerID(stream.ID, player.ID)
			h.streamTracker.SetCancelFunc(stream.ID, cancel)
			h.streamTracker.SetMediaInfo(stream.ID, definition.Duration, 0)
			cleanup = func() {
				cancel()
				h.streamTracker.Remove(stream.ID)
			}
			defer cleanup()
			streamWriter = &trackedResponseWriter{
				ResponseWriter: w,
				stream:         stream,
				streamTracker:  h.streamTracker,
			}
		}

		if transcoder.shouldTranscode(r, path) {
			transcoder.serveTranscoded(streamWriter, streamReq, streamReq.Context(), path, file)
			return
		}

		if mimeType := mime.TypeByExtension(filepath.Ext(path)); mimeType != "" {
			w.Header().Set("Content-Type", mimeType)
		} else {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		w.Header().Set("Content-Disposition", `inline; filename="`+filepath.Base(path)+`"`)
		var reader io.ReadSeeker = file
		if stream != nil {
			reader = &MonitoredFile{
				file:          file,
				stream:        stream,
				ctx:           streamReq.Context(),
				streamTracker: h.streamTracker,
			}
		}
		http.ServeContent(w, streamReq, filepath.Base(path), info.ModTime(), reader)
	})
}

func taterBumperNameFromPath(path string) string {
	rest := strings.TrimPrefix(path, "/api/tater/bumpers/file/")
	name, err := url.PathUnescape(rest)
	if err != nil {
		return ""
	}
	return taterTVSafeFileName(name)
}
