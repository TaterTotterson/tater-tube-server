package api

import (
	"context"
	"database/sql"
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
	TaterLink          bool `json:"taterLink"`
}

type taterPlayerHomeHero struct {
	Personalized bool      `json:"personalized"`
	Eyebrow      string    `json:"eyebrow"`
	Message      string    `json:"message"`
	Assistant    string    `json:"assistantName"`
	GeneratedAt  time.Time `json:"generatedAt"`
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
	Hero             *taterPlayerHomeHero        `json:"hero,omitempty"`
	Warnings         []string                    `json:"warnings,omitempty"`
}

type taterPlayerLibraryRow struct {
	Title string              `json:"title"`
	Entry taterUsenetCategory `json:"entry"`
	Items []taterUsenetItem   `json:"items"`
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
	response.Capabilities.TaterLink, response.Hero = s.taterPlayerLinkedHero(
		c.Context(), response.GeneratedAt,
	)

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

func (s *Server) taterPlayerLinkedHero(ctx context.Context, now time.Time) (bool, *taterPlayerHomeHero) {
	if s.queueRepo == nil {
		return false, nil
	}
	connections, err := s.queueRepo.ListTaterCoreConnections(ctx)
	if err != nil {
		return false, nil
	}
	connected := false
	for _, connection := range connections {
		if !connection.RevokedAt.Valid {
			connected = true
			break
		}
	}
	if !connected {
		return false, nil
	}

	batch, _, err := s.queueRepo.GetActiveTaterRecommendations(
		ctx, taterDefaultProfileID, now,
	)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil || batch == nil {
		return true, nil
	}
	message := cleanTaterText(batch.Summary)
	if message == "" {
		return true, nil
	}
	assistant := cleanTaterAssistantFirstName(batch.AssistantName)
	if assistant == "" {
		assistant = "Tater"
	}
	return true, &taterPlayerHomeHero{
		Personalized: true,
		Eyebrow:      "TATER LINK  •  PICKED FOR YOU",
		Message:      message,
		Assistant:    assistant,
		GeneratedAt:  batch.GeneratedAt,
	}
}

func (s *Server) handleTaterPlayerLibrary(c *fiber.Ctx) error {
	cfg, playerToken, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}
	rows := []taterPlayerLibraryRow{}
	if !taterLocalMediaEnabled(cfg) {
		return RespondSuccess(c, fiber.Map{"rows": rows})
	}

	baseURL := resolveBaseURL(c, "")
	continueWatching, err := taterContinueWatchingItems(cfg, baseURL, playerToken)
	if err == nil && len(continueWatching) > 0 {
		continueWatching = limitTaterPlayerHomeItems(continueWatching)
		decorateTaterPlayerHomeItems(cfg, baseURL, playerToken, continueWatching)
		rows = append(rows, taterPlayerLibraryRow{
			Title: "Continue Watching",
			Entry: taterUsenetCategory{Type: "continue", Title: "Continue Watching"},
			Items: continueWatching,
		})
	}

	allItems, err := taterLocalDiscoverLibraryItems(cfg, baseURL, playerToken)
	if err != nil {
		return RespondServiceUnavailable(c, "Failed to load player library", err.Error())
	}
	allItems = taterAttachLocalPlayStates(cfg, allItems)
	for _, def := range taterLocalDiscoverDefinitions() {
		items := taterFilterLocalDiscoverItems(allItems, def.ID)
		if len(items) == 0 {
			continue
		}
		items = limitTaterPlayerHomeItems(items)
		decorateTaterPlayerHomeItems(cfg, baseURL, playerToken, items)
		rows = append(rows, taterPlayerLibraryRow{
			Title: def.Title,
			Entry: taterUsenetCategory{
				ID: def.ID, Type: "localDiscover", Title: def.Title, Detail: def.Detail,
			},
			Items: items,
		})
	}

	for _, category := range cfg.LocalMedia.Categories {
		if !taterLocalLibraryEnabled(category) ||
			strings.EqualFold(strings.TrimSpace(category.LibraryType), "music") {
			continue
		}
		categoryID := strings.TrimSpace(category.ID)
		title := cleanTaterText(category.Name)
		if categoryID == "" || title == "" {
			continue
		}
		items := []taterUsenetItem{}
		for _, item := range allItems {
			if taterRawLocalCategoryID(item.CategoryID) == categoryID {
				items = append(items, item)
			}
		}
		if len(items) == 0 {
			continue
		}
		items = limitTaterPlayerHomeItems(items)
		decorateTaterPlayerHomeItems(cfg, baseURL, playerToken, items)
		rows = append(rows, taterPlayerLibraryRow{
			Title: title,
			Entry: taterUsenetCategory{
				ID: "local:" + categoryID, Type: "local", Title: title, Detail: "LOCAL",
			},
			Items: items,
		})
	}

	return RespondSuccess(c, fiber.Map{"rows": rows})
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
		item := &items[index]
		if cfg == nil {
			item.HasArtwork = strings.TrimSpace(item.Poster) != ""
			continue
		}
		category, categoryFound := taterLocalMediaCategory(cfg, taterRawLocalCategoryID(item.CategoryID))
		isTV := categoryFound && strings.EqualFold(strings.TrimSpace(category.LibraryType), "tv")
		if isTV {
			item.SeriesPoster = taterPlayerAvailableLocalArtworkURL(
				cfg, baseURL, playerToken, item.CategoryID, item.SourceIndex, item.Path, "series-poster",
			)
			item.Backdrop = taterPlayerAvailableLocalArtworkURL(
				cfg, baseURL, playerToken, item.CategoryID, item.SourceIndex, item.Path, "backdrop",
			)
			mediaType := strings.ToLower(strings.TrimSpace(item.MediaType))
			if mediaType == "season" || mediaType == "episode" {
				item.SeasonPoster = taterPlayerAvailableLocalArtworkURL(
					cfg, baseURL, playerToken, item.CategoryID, item.SourceIndex, item.Path, "season-poster",
				)
			}
			if mediaType == "episode" {
				item.EpisodeStill = taterPlayerAvailableLocalArtworkURL(
					cfg, baseURL, playerToken, item.CategoryID, item.SourceIndex, item.Path, "episode-still",
				)
			}
			if strings.TrimSpace(item.Poster) == "" {
				for _, candidate := range []string{item.EpisodeStill, item.SeasonPoster, item.SeriesPoster} {
					if strings.TrimSpace(candidate) != "" {
						item.Poster = candidate
						break
					}
				}
			}
		}
		if strings.TrimSpace(item.Poster) == "" {
			item.Poster = taterPlayerAvailableLocalArtworkURL(
				cfg, baseURL, playerToken, item.CategoryID, item.SourceIndex, item.Path, "poster",
			)
		}
		items[index].HasArtwork = items[index].Poster != ""
	}
}

func taterPlayerLocalArtworkURL(baseURL, playerToken, categoryID string, sourceIndex int, relPath string) string {
	return taterPlayerLocalArtworkURLForKind(baseURL, playerToken, categoryID, sourceIndex, relPath, "poster")
}

func taterPlayerLocalArtworkURLForKind(baseURL, playerToken, categoryID string, sourceIndex int, relPath, kind string) string {
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/api/v1/player/artwork/local")
	if err != nil {
		return ""
	}
	query := u.Query()
	query.Set("category_id", taterRawLocalCategoryID(categoryID))
	query.Set("source", strconv.Itoa(sourceIndex))
	query.Set("path", cleanLocalRelativePath(relPath))
	query.Set("player_token", playerToken)
	if normalizedKind := taterPlayerArtworkKind(kind); normalizedKind != "poster" {
		query.Set("kind", normalizedKind)
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func taterPlayerLocalArtworkPath(cfg *config.Config, categoryID string, sourceIndex int, relPath string) (string, bool) {
	return taterPlayerLocalArtworkPathForKind(cfg, categoryID, sourceIndex, relPath, "poster")
}

func taterPlayerArtworkKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "backdrop", "series-poster", "season-poster", "episode-still":
		return strings.ToLower(strings.TrimSpace(kind))
	default:
		return "poster"
	}
}

func taterPlayerAvailableLocalArtworkURL(
	cfg *config.Config,
	baseURL, playerToken, categoryID string,
	sourceIndex int,
	relPath, kind string,
) string {
	if cfg == nil {
		return ""
	}
	normalizedKind := taterPlayerArtworkKind(kind)
	found := false
	if normalizedKind == "poster" || normalizedKind == "series-poster" {
		if category, ok := taterLocalMediaCategory(cfg, taterRawLocalCategoryID(categoryID)); ok {
			_, found = taterStoredVideoArtworkPath(cfg, category, sourceIndex, relPath)
		}
	}
	if !found {
		_, found = taterPlayerLocalArtworkPathForKind(cfg, categoryID, sourceIndex, relPath, normalizedKind)
	}
	if !found {
		return ""
	}
	return taterPlayerLocalArtworkURLForKind(baseURL, playerToken, categoryID, sourceIndex, relPath, normalizedKind)
}

func taterPlayerLocalArtworkPathForKind(cfg *config.Config, categoryID string, sourceIndex int, relPath, kind string) (string, bool) {
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
	kind = taterPlayerArtworkKind(kind)
	directory := target
	base := filepath.Base(target)
	if !info.IsDir() {
		directory = filepath.Dir(target)
		base = strings.TrimSuffix(filepath.Base(target), filepath.Ext(target))
	}
	parts := strings.Split(cleanLocalRelativePath(relPath), "/")
	showDirectory := directory
	if strings.EqualFold(strings.TrimSpace(category.LibraryType), "tv") && len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
		if resolved, showErr := safeLocalPath(paths[sourceIndex], parts[0]); showErr == nil {
			showDirectory = resolved
		}
	}
	switch kind {
	case "backdrop":
		return taterPlayerArtworkNamedInDirectory(showDirectory, []string{
			"backdrop", "fanart", "background", "landscape", "thumb", "banner",
		})
	case "series-poster":
		return taterPlayerArtworkInDirectory(showDirectory, filepath.Base(showDirectory))
	case "season-poster":
		seasonDirectory := directory
		if len(parts) >= 2 {
			if resolved, seasonErr := safeLocalPath(paths[sourceIndex], filepath.ToSlash(filepath.Join(parts[0], parts[1]))); seasonErr == nil {
				seasonDirectory = resolved
			}
		}
		return taterPlayerArtworkInDirectory(seasonDirectory, filepath.Base(seasonDirectory))
	case "episode-still":
		if info.IsDir() {
			return "", false
		}
		return taterPlayerArtworkNamedInDirectory(directory, []string{
			strings.ToLower(base) + "-thumb",
			strings.ToLower(base) + "-landscape",
			strings.ToLower(base) + "-still",
			strings.ToLower(base),
		})
	}
	if path, found := taterPlayerArtworkInDirectory(directory, base); found {
		return path, true
	}
	if strings.EqualFold(strings.TrimSpace(category.LibraryType), "tv") {
		if showDirectory != directory {
			if path, found := taterPlayerArtworkInDirectory(showDirectory, filepath.Base(showDirectory)); found {
				return path, true
			}
		}
	}
	return "", false
}

func taterPlayerArtworkInDirectory(directory, base string) (string, bool) {
	desired := []string{
		strings.ToLower(base),
		strings.ToLower(base) + "-poster",
		strings.ToLower(base) + "-cover",
		strings.ToLower(base) + "-default",
		strings.ToLower(base) + "-movie",
		strings.ToLower(base) + "-show",
		"poster",
		"folder",
		"cover",
		"default",
		"movie",
		"show",
	}
	return taterPlayerArtworkNamedInDirectory(directory, desired)
}

func taterPlayerArtworkNamedInDirectory(directory string, desired []string) (string, bool) {
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
	kind := taterPlayerArtworkKind(c.Query("kind"))
	artworkPath := ""
	found := false
	if kind == "poster" || kind == "series-poster" {
		category, categoryFound := taterLocalMediaCategory(cfg, taterRawLocalCategoryID(categoryID))
		if categoryFound {
			artworkPath, found = taterStoredVideoArtworkPath(cfg, category, sourceIndex, relPath)
		}
	}
	if !found {
		artworkPath, found = taterPlayerLocalArtworkPathForKind(cfg, categoryID, sourceIndex, relPath, kind)
	}
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
