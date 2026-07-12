package api

import (
	"context"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/TaterTotterson/tater-tube-server/internal/nzbfilesystem"
)

// LocalStreamHandler serves configured host media folders to paired Tater Tube players.
type LocalStreamHandler struct {
	configGetter  config.ConfigGetter
	streamTracker *StreamTracker
}

func NewLocalStreamHandler(configGetter config.ConfigGetter, streamTracker *StreamTracker) *LocalStreamHandler {
	return &LocalStreamHandler{configGetter: configGetter, streamTracker: streamTracker}
}

func (h *LocalStreamHandler) GetHTTPHandler() http.Handler {
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
			w.Header().Set("WWW-Authenticate", `Bearer realm="Tater Local Stream"`)
			http.Error(w, "Unauthorized: valid player_token required", http.StatusUnauthorized)
			return
		}

		if !taterLocalMediaEnabled(cfg) {
			http.Error(w, "Local media is not configured", http.StatusServiceUnavailable)
			return
		}

		categoryID := strings.TrimSpace(r.URL.Query().Get("category_id"))
		sourceIndex := parseTaterInt(r.URL.Query().Get("source"), 0)
		relPath := cleanLocalRelativePath(r.URL.Query().Get("path"))
		cat, ok := taterLocalMediaCategory(cfg, categoryID)
		if !ok {
			http.Error(w, "Local media category not found", http.StatusNotFound)
			return
		}
		paths := taterLocalMediaCategoryPaths(cat)
		if sourceIndex < 0 || sourceIndex >= len(paths) {
			http.Error(w, "Local media source not found", http.StatusNotFound)
			return
		}
		if relPath == "" {
			http.Error(w, "Path parameter required", http.StatusBadRequest)
			return
		}

		path, err := safeLocalPath(paths[sourceIndex], relPath)
		if err != nil {
			http.Error(w, "Invalid local media path", http.StatusBadRequest)
			return
		}
		if !isLocalStreamExtension(filepath.Ext(path)) {
			http.Error(w, "Unsupported media file", http.StatusBadRequest)
			return
		}

		file, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "File not found", http.StatusNotFound)
				return
			}
			http.Error(w, "Failed to open file", http.StatusInternalServerError)
			return
		}
		defer file.Close()

		info, err := file.Stat()
		if err != nil {
			http.Error(w, "Failed to stat file", http.StatusInternalServerError)
			return
		}
		if info.IsDir() {
			http.Error(w, "Cannot stream directory", http.StatusBadRequest)
			return
		}

		playerName := taterPlayerDisplayName(player)
		streamReq := r
		var cleanup func()
		var streamWriter http.ResponseWriter = w
		var stream *nzbfilesystem.ActiveStream
		transcoder := &StreamHandler{configGetter: h.configGetter, streamTracker: h.streamTracker}
		if h.streamTracker != nil {
			streamCtx, cancel := context.WithCancel(r.Context())
			streamReq = r.WithContext(streamCtx)
			stream = h.streamTracker.AddStream(path, "Local", playerName, r.RemoteAddr, r.UserAgent(), info.Size())
			h.streamTracker.SetCancelFunc(stream.ID, cancel)
			transcoder.setStreamMediaInfoFromPath(streamReq.Context(), stream.ID, path, 0)
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
