package api

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

type taterLocalMusicMetadata struct {
	Title       string
	Artist      string
	AlbumArtist string
	Album       string
	Genres      []string
	Track       int
	Disc        int
	Year        string
	Duration    float64
	HasArtwork  bool
}

type taterLocalMusicMetadataCacheEntry struct {
	Size            int64
	ModTimeUnixNano int64
	Metadata        taterLocalMusicMetadata
}

var taterLocalMusicMetadataCache = struct {
	sync.Mutex
	Items map[string]taterLocalMusicMetadataCacheEntry
}{
	Items: map[string]taterLocalMusicMetadataCacheEntry{},
}

type taterFFProbeMusicPayload struct {
	Format struct {
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		CodecType   string            `json:"codec_type"`
		Duration    string            `json:"duration"`
		Tags        map[string]string `json:"tags"`
		Disposition struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
}

func taterLocalMusicMetadataForPath(cfg *config.Config, path string) taterLocalMusicMetadata {
	path = strings.TrimSpace(path)
	if path == "" {
		return taterLocalMusicMetadata{}
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = filepath.Clean(path)
	}
	info, err := os.Stat(absPath)
	if err != nil || info == nil || info.IsDir() {
		return taterLocalMusicMetadata{}
	}

	size := info.Size()
	modified := info.ModTime().UnixNano()
	taterLocalMusicMetadataCache.Lock()
	if cached, ok := taterLocalMusicMetadataCache.Items[absPath]; ok &&
		cached.Size == size &&
		cached.ModTimeUnixNano == modified {
		taterLocalMusicMetadataCache.Unlock()
		return cached.Metadata
	}
	taterLocalMusicMetadataCache.Unlock()

	ffmpegPath := "ffmpeg"
	if cfg != nil {
		ffmpegPath = effectiveFFmpegPath(cfg.Transcoding.FFmpegPath)
	}
	metadata, _ := probeTaterLocalMusicMetadata(context.Background(), ffmpegPath, absPath)

	taterLocalMusicMetadataCache.Lock()
	taterLocalMusicMetadataCache.Items[absPath] = taterLocalMusicMetadataCacheEntry{
		Size:            size,
		ModTimeUnixNano: modified,
		Metadata:        metadata,
	}
	taterLocalMusicMetadataCache.Unlock()
	return metadata
}

func probeTaterLocalMusicMetadata(
	parent context.Context,
	ffmpegPath string,
	path string,
) (taterLocalMusicMetadata, error) {
	ffprobePath := effectiveFFprobePath(ffmpegPath)
	if ffprobePath == "" {
		return taterLocalMusicMetadata{}, os.ErrNotExist
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()

	var stderr bytes.Buffer
	cmd := exec.CommandContext(
		ctx,
		ffprobePath,
		"-v", "error",
		"-show_entries",
		"format=duration:format_tags=title,artist,album_artist,albumartist,album,genre,track,tracknumber,disc,discnumber,date,year:"+
			"stream=codec_type,duration:stream_disposition=attached_pic:"+
			"stream_tags=title,artist,album_artist,albumartist,album,genre,track,tracknumber,disc,discnumber,date,year",
		"-of", "json",
		path,
	)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return taterLocalMusicMetadata{}, ctx.Err()
	}
	if err != nil {
		return taterLocalMusicMetadata{}, err
	}

	var payload taterFFProbeMusicPayload
	if err := json.Unmarshal(out, &payload); err != nil {
		return taterLocalMusicMetadata{}, err
	}
	tags := normalizeTaterMusicTags(payload.Format.Tags)
	for _, stream := range payload.Streams {
		for key, value := range normalizeTaterMusicTags(stream.Tags) {
			if tags[key] == "" {
				tags[key] = value
			}
		}
	}

	duration := parseTaterMusicDuration(payload.Format.Duration)
	hasArtwork := false
	for _, stream := range payload.Streams {
		if candidate := parseTaterMusicDuration(stream.Duration); candidate > duration {
			duration = candidate
		}
		if strings.EqualFold(strings.TrimSpace(stream.CodecType), "video") || stream.Disposition.AttachedPic > 0 {
			hasArtwork = true
		}
	}
	date := firstTaterMusicTag(tags, "date", "year")
	year := ""
	if match := localYearPattern.FindStringSubmatch(date); len(match) > 1 {
		year = match[1]
	}
	return taterLocalMusicMetadata{
		Title:       cleanTaterText(firstTaterMusicTag(tags, "title")),
		Artist:      cleanTaterText(firstTaterMusicTag(tags, "artist")),
		AlbumArtist: cleanTaterText(firstTaterMusicTag(tags, "album_artist", "albumartist")),
		Album:       cleanTaterText(firstTaterMusicTag(tags, "album")),
		Genres:      splitTaterMusicGenres(firstTaterMusicTag(tags, "genre")),
		Track:       parseTaterMusicNumber(firstTaterMusicTag(tags, "track", "tracknumber")),
		Disc:        parseTaterMusicNumber(firstTaterMusicTag(tags, "disc", "discnumber")),
		Year:        year,
		Duration:    duration,
		HasArtwork:  hasArtwork,
	}, nil
}

func normalizeTaterMusicTags(values map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(key))
		normalized = strings.ReplaceAll(normalized, "-", "_")
		if normalized == "" || strings.TrimSpace(value) == "" {
			continue
		}
		result[normalized] = strings.TrimSpace(value)
	}
	return result
}

func firstTaterMusicTag(tags map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(tags[strings.ToLower(strings.TrimSpace(key))]); value != "" {
			return value
		}
	}
	return ""
}

func parseTaterMusicDuration(value string) float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func parseTaterMusicNumber(value string) int {
	token := strings.TrimSpace(value)
	if before, _, ok := strings.Cut(token, "/"); ok {
		token = before
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(token))
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func splitTaterMusicGenres(value string) []string {
	parts := strings.FieldsFunc(value, func(char rune) bool {
		return char == ';' || char == ',' || char == '|'
	})
	return mergeTaterMusicGenres(nil, parts)
}

func mergeTaterMusicGenres(existing []string, additions []string) []string {
	result := make([]string, 0, len(existing)+len(additions))
	seen := map[string]bool{}
	for _, group := range [][]string{existing, additions} {
		for _, raw := range group {
			genre := cleanTaterText(raw)
			key := strings.ToLower(genre)
			if genre == "" || seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, genre)
		}
	}
	return result
}

func applyTaterLocalMusicMetadata(item *taterUsenetItem, metadata taterLocalMusicMetadata) {
	if item == nil {
		return
	}
	if metadata.Title != "" {
		item.Title = metadata.Title
	}
	if metadata.Artist != "" {
		item.Artist = metadata.Artist
	}
	if metadata.AlbumArtist != "" {
		item.AlbumArtist = metadata.AlbumArtist
		if item.Artist == "" {
			item.Artist = metadata.AlbumArtist
		}
	}
	if metadata.Album != "" {
		item.Album = metadata.Album
	}
	if len(metadata.Genres) > 0 {
		item.Genres = append([]string(nil), metadata.Genres...)
		item.Genre = strings.Join(metadata.Genres, ", ")
	}
	if metadata.Track > 0 {
		item.Index = metadata.Track
	}
	if metadata.Year != "" {
		item.Date = metadata.Year
	}
	attachTaterDuration(item, metadata.Duration)
}
