package api

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/TaterTotterson/tater-tube-server/internal/version"
	"github.com/gofiber/fiber/v2"
)

const (
	taterPlayerHomeProtocolVersion = "1"
	taterPlayerHomeItemLimit       = 12
	taterPlayerArtworkMaximumBytes = 20 * 1024 * 1024
)

type taterPlayerHomeCapabilities struct {
	LocalMedia         bool `json:"localMedia"`
	Newznab            bool `json:"newznab"`
	TubeTV             bool `json:"tubeTV"`
	Commercials        bool `json:"commercials"`
	MidrollCommercials bool `json:"midrollCommercials"`
}

type taterPlayerHomeProgram struct {
	Title           string    `json:"title"`
	Kind            string    `json:"kind,omitempty"`
	MediaType       string    `json:"mediaType,omitempty"`
	Poster          string    `json:"poster,omitempty"`
	StartsAt        time.Time `json:"startsAt"`
	EndsAt          time.Time `json:"endsAt"`
	ProgressPercent float64   `json:"progressPercent,omitempty"`
}

type taterPlayerHomeChannel struct {
	Number    string                  `json:"number"`
	Title     string                  `json:"title"`
	StreamURL string                  `json:"streamUrl,omitempty"`
	LogoPath  string                  `json:"logoPath,omitempty"`
	Now       *taterPlayerHomeProgram `json:"now,omitempty"`
	Next      *taterPlayerHomeProgram `json:"next,omitempty"`
}

type taterPlayerHomeResponse struct {
	ProtocolVersion  string                      `json:"protocolVersion"`
	ServerName       string                      `json:"serverName"`
	ServerVersion    string                      `json:"serverVersion"`
	GeneratedAt      time.Time                   `json:"generatedAt"`
	Capabilities     taterPlayerHomeCapabilities `json:"capabilities"`
	ContinueWatching []taterUsenetItem           `json:"continueWatching"`
	RecentlyAdded    []taterUsenetItem           `json:"recentlyAdded"`
	LiveChannels     []taterPlayerHomeChannel    `json:"liveChannels"`
	Libraries        []taterUsenetCategory       `json:"libraries"`
	Warnings         []string                    `json:"warnings,omitempty"`
}

func (s *Server) handleTaterPlayerHome(c *fiber.Ctx) error {
	cfg, playerToken, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}

	baseURL := resolveBaseURL(c, "")
	response := taterPlayerHomeResponse{
		ProtocolVersion:  taterPlayerHomeProtocolVersion,
		ServerName:       "Tater Tube Server",
		ServerVersion:    version.Version,
		GeneratedAt:      time.Now().UTC(),
		ContinueWatching: []taterUsenetItem{},
		RecentlyAdded:    []taterUsenetItem{},
		LiveChannels:     []taterPlayerHomeChannel{},
		Libraries:        []taterUsenetCategory{},
		Warnings:         []string{},
	}
	response.Capabilities = taterPlayerCapabilities(cfg)

	continueWatching, err := taterContinueWatchingItems(cfg, baseURL, playerToken)
	if err != nil {
		response.Warnings = append(response.Warnings, "Continue Watching is temporarily unavailable")
	} else {
		response.ContinueWatching = limitTaterPlayerHomeItems(continueWatching)
		decorateTaterPlayerHomeItems(cfg, baseURL, playerToken, response.ContinueWatching)
	}

	if response.Capabilities.LocalMedia {
		recentlyAdded, recentErr := taterLocalDiscoverItems(cfg, baseURL, playerToken, "local-discover:recent")
		if recentErr != nil {
			response.Warnings = append(response.Warnings, "Recently Added is temporarily unavailable")
		} else {
			recentlyAdded = taterAttachLocalPlayStates(cfg, recentlyAdded)
			response.RecentlyAdded = limitTaterPlayerHomeItems(recentlyAdded)
			decorateTaterPlayerHomeItems(cfg, baseURL, playerToken, response.RecentlyAdded)
		}
		response.Libraries = append(response.Libraries, taterLocalRootRow(cfg).Children...)
	}

	if response.Capabilities.TubeTV {
		channels, channelErr := taterPlayerHomeChannels(cfg, baseURL, playerToken, response.GeneratedAt)
		if channelErr != nil {
			response.Warnings = append(response.Warnings, "Tube TV is temporarily unavailable")
		} else {
			response.LiveChannels = channels
		}
	}

	return RespondSuccess(c, response)
}

func taterPlayerCapabilities(cfg *config.Config) taterPlayerHomeCapabilities {
	capabilities := taterPlayerHomeCapabilities{
		LocalMedia: taterLocalMediaEnabled(cfg),
		Newznab:    taterNewznabEnabled(cfg),
		TubeTV:     taterTubeTVEnabled(cfg),
	}
	if cfg != nil {
		capabilities.Commercials = capabilities.TubeTV &&
			(cfg.TubeTV.CommercialsEnabled == nil || *cfg.TubeTV.CommercialsEnabled)
		capabilities.MidrollCommercials = capabilities.Commercials &&
			cfg.TubeTV.MidrollCommercials != nil && *cfg.TubeTV.MidrollCommercials
	}
	return capabilities
}

func limitTaterPlayerHomeItems(items []taterUsenetItem) []taterUsenetItem {
	if len(items) > taterPlayerHomeItemLimit {
		return items[:taterPlayerHomeItemLimit]
	}
	return items
}

func taterPlayerHomeChannels(cfg *config.Config, baseURL, playerToken string, now time.Time) ([]taterPlayerHomeChannel, error) {
	guide, ok := taterTVCachedGuide(cfg)
	if !ok {
		return nil, fmt.Errorf("tube TV guide is being prepared")
	}
	elapsed := now.Sub(guide.StartedAt).Seconds()
	channels := make([]taterPlayerHomeChannel, 0, len(guide.Channels))
	for _, channel := range guide.Channels {
		row := taterPlayerHomeChannel{
			Number:    channel.Number,
			Title:     channel.Title,
			StreamURL: taterTVChannelStreamURL(baseURL, channel.Number, playerToken),
			LogoPath:  channel.LogoPath,
		}
		currentIndex := -1
		nextIndex := -1
		for index, program := range channel.Schedule {
			start := rowFloat(program, "start")
			end := rowFloat(program, "end")
			if start <= elapsed && elapsed < end {
				currentIndex = index
				if index+1 < len(channel.Schedule) {
					nextIndex = index + 1
				}
				break
			}
			if start > elapsed {
				nextIndex = index
				break
			}
		}
		if currentIndex >= 0 {
			row.Now = taterPlayerHomeProgramFromSchedule(cfg, baseURL, playerToken, guide.StartedAt, channel.Schedule[currentIndex], elapsed)
		}
		if nextIndex >= 0 {
			row.Next = taterPlayerHomeProgramFromSchedule(cfg, baseURL, playerToken, guide.StartedAt, channel.Schedule[nextIndex], elapsed)
		}
		channels = append(channels, row)
		if len(channels) == taterPlayerHomeItemLimit {
			break
		}
	}
	return channels, nil
}

func taterPlayerHomeProgramFromSchedule(cfg *config.Config, baseURL, playerToken string, guideStart time.Time, row map[string]any, elapsed float64) *taterPlayerHomeProgram {
	if row == nil {
		return nil
	}
	start := rowFloat(row, "start")
	end := rowFloat(row, "end")
	program := &taterPlayerHomeProgram{
		Title:     rowString(row, "title"),
		Kind:      rowString(row, "kind"),
		MediaType: rowString(row, "mediaType"),
		StartsAt:  guideStart.Add(time.Duration(start * float64(time.Second))).UTC(),
		EndsAt:    guideStart.Add(time.Duration(end * float64(time.Second))).UTC(),
	}
	if program.Title == "" {
		program.Title = "Tater Tube"
	}
	if end > start && elapsed >= start && elapsed < end {
		program.ProgressPercent = ((elapsed - start) / (end - start)) * 100
	}
	item := taterUsenetItem{
		Poster:      rowString(row, "poster"),
		CategoryID:  rowString(row, "categoryId"),
		SourceIndex: int(rowFloat(row, "sourceIndex")),
		Path:        rowString(row, "path"),
	}
	items := []taterUsenetItem{item}
	decorateTaterPlayerHomeItems(cfg, baseURL, playerToken, items)
	program.Poster = items[0].Poster
	return program
}

func decorateTaterPlayerHomeItems(cfg *config.Config, baseURL, playerToken string, items []taterUsenetItem) {
	for index := range items {
		if strings.TrimSpace(items[index].Poster) != "" {
			continue
		}
		if _, ok := taterPlayerLocalArtworkPath(cfg, items[index].CategoryID, items[index].SourceIndex, items[index].Path); !ok {
			continue
		}
		items[index].Poster = taterPlayerLocalArtworkURL(baseURL, playerToken, items[index].CategoryID, items[index].SourceIndex, items[index].Path)
		items[index].HasArtwork = items[index].Poster != ""
	}
}

func taterPlayerLocalArtworkURL(baseURL, playerToken, categoryID string, sourceIndex int, relPath string) string {
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/api/v1/player/artwork/local")
	if err != nil {
		return ""
	}
	query := u.Query()
	query.Set("category_id", taterRawLocalCategoryID(categoryID))
	query.Set("source", strconv.Itoa(sourceIndex))
	query.Set("path", cleanLocalRelativePath(relPath))
	query.Set("player_token", playerToken)
	u.RawQuery = query.Encode()
	return u.String()
}

func taterPlayerLocalArtworkPath(cfg *config.Config, categoryID string, sourceIndex int, relPath string) (string, bool) {
	if cfg == nil || strings.TrimSpace(categoryID) == "" {
		return "", false
	}
	category, ok := taterLocalMediaCategory(cfg, taterRawLocalCategoryID(categoryID))
	if !ok {
		return "", false
	}
	paths := taterLocalMediaCategoryPaths(category)
	if sourceIndex < 0 || sourceIndex >= len(paths) {
		return "", false
	}
	target, err := safeLocalPath(paths[sourceIndex], relPath)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(target)
	if err != nil || info == nil {
		return "", false
	}
	directory := target
	base := filepath.Base(target)
	if !info.IsDir() {
		directory = filepath.Dir(target)
		base = strings.TrimSuffix(filepath.Base(target), filepath.Ext(target))
	}
	desired := []string{
		strings.ToLower(base),
		strings.ToLower(base) + "-poster",
		"poster",
		"folder",
		"cover",
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", false
	}
	for _, name := range desired {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			extension := strings.ToLower(filepath.Ext(entry.Name()))
			if !isTaterArtworkImageExtension(extension) || strings.ToLower(strings.TrimSuffix(entry.Name(), extension)) != name {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			artworkInfo, statErr := os.Stat(path)
			if statErr == nil && artworkInfo != nil && artworkInfo.Mode().IsRegular() && artworkInfo.Size() <= taterPlayerArtworkMaximumBytes {
				return path, true
			}
		}
	}
	return "", false
}

func (s *Server) handleTaterPlayerLocalArtwork(c *fiber.Ctx) error {
	if playerToken := strings.TrimSpace(c.Query("player_token")); playerToken != "" && strings.TrimSpace(c.Get(fiber.HeaderAuthorization)) == "" {
		c.Request().Header.Set(fiber.HeaderAuthorization, "Bearer "+playerToken)
	}
	cfg, _, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}
	categoryID := strings.TrimSpace(c.Query("category_id"))
	sourceIndex := parseTaterInt(c.Query("source"), -1)
	relPath := cleanLocalRelativePath(c.Query("path"))
	artworkPath, found := taterPlayerLocalArtworkPath(cfg, categoryID, sourceIndex, relPath)
	if !found {
		return RespondNotFound(c, "Local media artwork", fmt.Sprintf("%s:%d:%s", categoryID, sourceIndex, relPath))
	}
	switch strings.ToLower(filepath.Ext(artworkPath)) {
	case ".jpg", ".jpeg":
		c.Set(fiber.HeaderContentType, "image/jpeg")
	case ".png":
		c.Set(fiber.HeaderContentType, "image/png")
	case ".webp":
		c.Set(fiber.HeaderContentType, "image/webp")
	}
	c.Set(fiber.HeaderCacheControl, "private, max-age=86400")
	return c.SendFile(artworkPath, false)
}
