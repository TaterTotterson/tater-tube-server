package api

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
)

const taterMusicArtworkMaximumBytes = 12 * 1024 * 1024

type taterMusicArtworkCacheEntry struct {
	Size            int64
	ModTimeUnixNano int64
	JPEG            []byte
}

var taterMusicArtworkCache = struct {
	sync.Mutex
	Items map[string]taterMusicArtworkCacheEntry
}{
	Items: map[string]taterMusicArtworkCacheEntry{},
}

func (s *Server) handleTaterMusicArtwork(c *fiber.Ctx) error {
	if playerToken := strings.TrimSpace(c.Query("player_token")); playerToken != "" && strings.TrimSpace(c.Get(fiber.HeaderAuthorization)) == "" {
		c.Request().Header.Set(fiber.HeaderAuthorization, "Bearer "+playerToken)
	}
	cfg, _, ok := s.taterUsenetAuthorizedConfig(c)
	if !ok {
		return nil
	}
	if albumID := strings.TrimSpace(c.Query("album_id")); albumID != "" {
		return serveTaterIndexedMusicArtwork(c, cfg, albumID)
	}
	categoryID := strings.TrimSpace(c.Query("category_id"))
	sourceIndex := parseTaterInt(c.Query("source"), 0)
	relPath := cleanLocalRelativePath(c.Query("path"))
	cat, ok := taterLocalMediaCategory(cfg, categoryID)
	if !ok || !strings.EqualFold(strings.TrimSpace(cat.LibraryType), "music") {
		return RespondNotFound(c, "Music library", categoryID)
	}
	paths := taterLocalMediaCategoryPaths(cat)
	if sourceIndex < 0 || sourceIndex >= len(paths) {
		return RespondNotFound(c, "Music source", strconv.Itoa(sourceIndex))
	}
	if relPath == "" {
		return RespondValidationError(c, "Music track path is required", "path is empty")
	}
	path, err := safeLocalPath(paths[sourceIndex], relPath)
	if err != nil || (!isAudioExtension(filepath.Ext(path)) && !isTaterArtworkImageExtension(filepath.Ext(path))) {
		return RespondValidationError(c, "Music track path is invalid", "")
	}
	jpeg, err := taterLocalMusicArtworkForPath(c.Context(), cfg, path)
	if err != nil {
		if os.IsNotExist(err) {
			return RespondNotFound(c, "Embedded music artwork", "")
		}
		return RespondValidationError(c, "Failed to load embedded music artwork", err.Error())
	}
	c.Set(fiber.HeaderContentType, "image/jpeg")
	c.Set(fiber.HeaderCacheControl, "private, max-age=86400")
	return c.Send(jpeg)
}

func taterLocalMusicArtworkForPath(parent context.Context, cfg *config.Config, path string) ([]byte, error) {
	absPath, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absPath)
	if err != nil || info == nil || info.IsDir() {
		if err == nil {
			err = os.ErrNotExist
		}
		return nil, err
	}

	taterMusicArtworkCache.Lock()
	if cached, ok := taterMusicArtworkCache.Items[absPath]; ok &&
		cached.Size == info.Size() &&
		cached.ModTimeUnixNano == info.ModTime().UnixNano() &&
		len(cached.JPEG) > 0 {
		jpeg := append([]byte(nil), cached.JPEG...)
		taterMusicArtworkCache.Unlock()
		return jpeg, nil
	}
	taterMusicArtworkCache.Unlock()

	ffmpegPath := "ffmpeg"
	if cfg != nil {
		ffmpegPath = effectiveFFmpegPath(cfg.Transcoding.FFmpegPath)
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(
		ctx,
		ffmpegPath,
		"-v", "error",
		"-i", absPath,
		"-map", "0:v:0",
		"-frames:v", "1",
		"-vf", "scale=800:800:force_original_aspect_ratio=decrease",
		"-f", "image2pipe",
		"-vcodec", "mjpeg",
		"pipe:1",
	)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("embedded artwork extraction failed: %w", err)
	}
	if len(out) == 0 || len(out) > taterMusicArtworkMaximumBytes {
		return nil, fmt.Errorf("embedded artwork is empty or too large")
	}

	taterMusicArtworkCache.Lock()
	if len(taterMusicArtworkCache.Items) >= 256 {
		taterMusicArtworkCache.Items = map[string]taterMusicArtworkCacheEntry{}
	}
	taterMusicArtworkCache.Items[absPath] = taterMusicArtworkCacheEntry{
		Size:            info.Size(),
		ModTimeUnixNano: info.ModTime().UnixNano(),
		JPEG:            append([]byte(nil), out...),
	}
	taterMusicArtworkCache.Unlock()
	return out, nil
}
