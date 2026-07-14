package api

import (
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
)

type taterTVEpisodeGroup struct {
	Title    string            `json:"title"`
	Episodes []taterUsenetItem `json:"episodes"`
	Seen     map[string]bool   `json:"-"`
}

type taterTVSource struct {
	Title              string
	SourceType         string
	ChannelNumber      string
	CommercialCategory string
	LogoPath           string
	LogoTitle          string
	LogoPosition       string
	Programs           []taterUsenetItem
	Groups             []taterTVEpisodeGroup
	Seen               map[string]bool
}

type taterTVNumberedSource struct {
	Number string
	Source taterTVSource
}

type taterTVCommercial struct {
	Title         string  `json:"title"`
	CategoryID    string  `json:"categoryId"`
	Category      string  `json:"category"`
	Name          string  `json:"name"`
	URL           string  `json:"url,omitempty"`
	Kind          string  `json:"kind"`
	Local         bool    `json:"local"`
	Duration      float64 `json:"duration"`
	FullDuration  float64 `json:"fullDuration"`
	DurationKnown bool    `json:"durationKnown"`
}

type taterTVCommercialCategory struct {
	ID     string              `json:"id"`
	Title  string              `json:"title"`
	Count  int                 `json:"count"`
	Videos []taterTVCommercial `json:"videos"`
}

type taterTVChannel struct {
	Number        string           `json:"number"`
	Title         string           `json:"title"`
	StreamURL     string           `json:"streamUrl,omitempty"`
	LogoPath      string           `json:"logoPath,omitempty"`
	LogoTitle     string           `json:"logoTitle,omitempty"`
	LogoPosition  string           `json:"logoPosition,omitempty"`
	Schedule      []map[string]any `json:"schedule"`
	TotalDuration float64          `json:"totalDuration"`
}

type tubeTVLocalLibraryRow struct {
	ID          string `json:"id,omitempty"`
	Title       string `json:"title"`
	Detail      string `json:"detail,omitempty"`
	Type        string `json:"type,omitempty"`
	MediaType   string `json:"mediaType,omitempty"`
	CategoryID  string `json:"categoryId,omitempty"`
	SourceIndex int    `json:"sourceIndex"`
	Path        string `json:"path,omitempty"`
	Count       int    `json:"count,omitempty"`
	Selectable  bool   `json:"selectable"`
	Browsable   bool   `json:"browsable"`
}

type tubeTVLocalLibraryResponse struct {
	Title       string                     `json:"title"`
	CategoryID  string                     `json:"categoryId,omitempty"`
	SourceIndex int                        `json:"sourceIndex"`
	Path        string                     `json:"path,omitempty"`
	Source      *config.TubeTVCustomSource `json:"source,omitempty"`
	Rows        []tubeTVLocalLibraryRow    `json:"rows"`
}

type taterTVGuideCacheEntry struct {
	Channels     []taterTVChannel `json:"channels"`
	StartedAt    time.Time        `json:"startedAt"`
	GeneratedAt  time.Time        `json:"generatedAt"`
	UpdatedAt    time.Time        `json:"updatedAt"`
	PlannedUntil time.Time        `json:"plannedUntil"`
}

type tubeTVGuideResponse struct {
	Channels             []taterTVChannel `json:"channels"`
	StartedAt            time.Time        `json:"startedAt"`
	GeneratedAt          time.Time        `json:"generatedAt"`
	UpdatedAt            time.Time        `json:"updatedAt"`
	PlannedUntil         time.Time        `json:"plannedUntil"`
	HorizonHours         int              `json:"horizonHours"`
	RefillThresholdHours int              `json:"refillThresholdHours"`
}

const (
	taterTVGuideHorizon         = 12 * time.Hour
	taterTVGuideRefillThreshold = 2 * time.Hour
	taterTVGuidePlannerInterval = 5 * time.Minute
)

var (
	taterTVGuideMu    sync.Mutex
	taterTVGuideCache *taterTVGuideCacheEntry
)

func (s *Server) handleTaterTVLineup(c *fiber.Ctx) error {
	cfg, playerToken, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}
	if !taterTubeTVEnabled(cfg) {
		return RespondServiceUnavailable(c, "Tube TV is not enabled", "")
	}
	baseURL := resolveBaseURL(c, "")
	guide, err := taterTVEnsureGuide(cfg, baseURL, time.Now())
	if err != nil {
		return RespondServiceUnavailable(c, "Failed to build TV lineup", err.Error())
	}
	return RespondSuccess(c, fiber.Map{
		"channels":     taterTVPersonalizeChannels(guide.Channels, baseURL, playerToken),
		"startedAt":    guide.StartedAt,
		"plannedUntil": guide.PlannedUntil,
		"settings": fiber.Map{
			"enabled":               cfg.TubeTV.Enabled == nil || *cfg.TubeTV.Enabled,
			"auto_channels":         cfg.TubeTV.AutoChannels == nil || *cfg.TubeTV.AutoChannels,
			"commercials_enabled":   cfg.TubeTV.CommercialsEnabled == nil || *cfg.TubeTV.CommercialsEnabled,
			"midroll_commercials":   cfg.TubeTV.MidrollCommercials != nil && *cfg.TubeTV.MidrollCommercials,
			"commercial_categories": cfg.TubeTV.CommercialCategories,
		},
	})
}

func (s *Server) handleTubeTVGuide(c *fiber.Ctx) error {
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondInternalError(c, "Configuration not available", "CONFIG_NOT_FOUND")
	}
	if !taterTubeTVEnabled(cfg) {
		return RespondSuccess(c, tubeTVGuideResponse{
			Channels:             []taterTVChannel{},
			HorizonHours:         int(taterTVGuideHorizon / time.Hour),
			RefillThresholdHours: int(taterTVGuideRefillThreshold / time.Hour),
		})
	}
	guide, err := taterTVEnsureGuide(cfg, resolveBaseURL(c, ""), time.Now())
	if err != nil {
		return RespondServiceUnavailable(c, "Failed to build TV guide", err.Error())
	}
	return RespondSuccess(c, taterTVGuideResponse(guide, "", ""))
}

func (s *Server) handleTubeTVGuideRebuild(c *fiber.Ctx) error {
	taterTVResetGuide()
	return s.handleTubeTVGuide(c)
}

func (s *Server) handleTubeTVCommercialLibrary(c *fiber.Ctx) error {
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondInternalError(c, "Configuration not available", "CONFIG_NOT_FOUND")
	}
	return RespondSuccess(c, fiber.Map{
		"root":       taterTVCommercialRoot(cfg),
		"categories": taterTVCommercialCategories(cfg, "", ""),
	})
}

func (s *Server) handleTubeTVLocalLibrary(c *fiber.Ctx) error {
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondInternalError(c, "Configuration not available", "CONFIG_NOT_FOUND")
	}
	if !taterLocalMediaEnabled(cfg) {
		return RespondSuccess(c, tubeTVLocalLibraryResponse{
			Title:       "Local Library",
			SourceIndex: -1,
			Rows:        []tubeTVLocalLibraryRow{},
		})
	}

	categoryID := strings.TrimSpace(c.Query("category_id"))
	sourceIndex := parseTaterInt(c.Query("source"), -1)
	relPath := cleanLocalRelativePath(c.Query("path"))
	if categoryID == "" {
		return RespondSuccess(c, tubeTVLocalLibraryRoot(cfg))
	}
	if strings.HasPrefix(categoryID, "local-discover:") {
		items, err := taterLocalDiscoverItems(cfg, "", "", categoryID)
		if err != nil {
			return RespondValidationError(c, "Failed to load Local discovery", err.Error())
		}
		title := tubeTVLocalDiscoverTitle(categoryID)
		source := config.TubeTVCustomSource{
			CategoryID:  categoryID,
			SourceIndex: -1,
			Title:       title,
		}
		return RespondSuccess(c, tubeTVLocalLibraryResponse{
			Title:       title,
			CategoryID:  categoryID,
			SourceIndex: -1,
			Source:      &source,
			Rows:        tubeTVLocalLibraryItemRows(items),
		})
	}

	localID := strings.TrimPrefix(categoryID, "local:")
	cat, ok := taterLocalMediaCategory(cfg, localID)
	if !ok {
		return RespondValidationError(c, "Local media category not found", categoryID)
	}
	items, err := taterLocalMediaItems(cfg, "", "", localID, sourceIndex, relPath)
	if err != nil {
		return RespondValidationError(c, "Failed to load Local media", err.Error())
	}
	title := cleanTaterText(cat.Name)
	if relPath != "" {
		title = cleanLocalTitle(filepath.Base(relPath))
	}
	if title == "" {
		title = "Local"
	}
	source := config.TubeTVCustomSource{
		CategoryID:  localID,
		SourceIndex: sourceIndex,
		Path:        relPath,
		Title:       title,
		MediaType:   strings.ToLower(strings.TrimSpace(cat.LibraryType)),
	}
	return RespondSuccess(c, tubeTVLocalLibraryResponse{
		Title:       title,
		CategoryID:  localID,
		SourceIndex: sourceIndex,
		Path:        relPath,
		Source:      &source,
		Rows:        tubeTVLocalLibraryItemRows(items),
	})
}

func (s *Server) handleTubeTVCreateCommercialCategory(c *fiber.Ctx) error {
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondInternalError(c, "Configuration not available", "CONFIG_NOT_FOUND")
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := c.BodyParser(&body); err != nil {
		return RespondValidationError(c, "Invalid commercial category request", err.Error())
	}
	name := taterTVCategoryID(body.Name, "commercials")
	if err := os.MkdirAll(filepath.Join(taterTVCommercialRoot(cfg), name), 0755); err != nil {
		return RespondInternalError(c, "Failed to create commercial category", err.Error())
	}
	taterTVResetGuide()
	return s.handleTubeTVCommercialLibrary(c)
}

func (s *Server) handleTubeTVUploadCommercials(c *fiber.Ctx) error {
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondInternalError(c, "Configuration not available", "CONFIG_NOT_FOUND")
	}
	category := taterTVCategoryID(c.FormValue("category"), "commercials")
	form, err := c.MultipartForm()
	if err != nil {
		return RespondValidationError(c, "Commercial upload invalid", err.Error())
	}
	files := form.File["files"]
	if len(files) == 0 {
		return RespondValidationError(c, "No commercial files uploaded", "files field is empty")
	}
	dir := filepath.Join(taterTVCommercialRoot(cfg), category)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return RespondInternalError(c, "Failed to create commercial category", err.Error())
	}
	for _, header := range files {
		name := taterTVSafeFileName(header.Filename)
		if !isMediaExtension(filepath.Ext(name)) {
			continue
		}
		src, err := header.Open()
		if err != nil {
			return RespondInternalError(c, "Failed to open uploaded commercial", err.Error())
		}
		dst, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			_ = src.Close()
			return RespondInternalError(c, "Failed to save commercial", err.Error())
		}
		_, copyErr := io.Copy(dst, src)
		closeErr := dst.Close()
		_ = src.Close()
		if copyErr != nil {
			return RespondInternalError(c, "Failed to save commercial", copyErr.Error())
		}
		if closeErr != nil {
			return RespondInternalError(c, "Failed to finish commercial upload", closeErr.Error())
		}
	}
	taterTVResetGuide()
	return s.handleTubeTVCommercialLibrary(c)
}

func (s *Server) handleTubeTVDeleteCommercialFile(c *fiber.Ctx) error {
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondInternalError(c, "Configuration not available", "CONFIG_NOT_FOUND")
	}
	category := taterTVCategoryID(c.Query("category"), "")
	name := taterTVSafeFileName(c.Query("name"))
	if category == "" || name == "" {
		return RespondValidationError(c, "Commercial file is required", "category and name are required")
	}
	if err := os.Remove(filepath.Join(taterTVCommercialRoot(cfg), category, name)); err != nil && !os.IsNotExist(err) {
		return RespondInternalError(c, "Failed to delete commercial", err.Error())
	}
	taterTVResetGuide()
	return s.handleTubeTVCommercialLibrary(c)
}

func (s *Server) handleTubeTVDeleteCommercialCategory(c *fiber.Ctx) error {
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondInternalError(c, "Configuration not available", "CONFIG_NOT_FOUND")
	}
	category := taterTVCategoryID(c.Query("category"), "")
	if category == "" {
		return RespondValidationError(c, "Commercial category is required", "category is empty")
	}
	if err := os.RemoveAll(filepath.Join(taterTVCommercialRoot(cfg), category)); err != nil {
		return RespondInternalError(c, "Failed to delete commercial category", err.Error())
	}
	taterTVResetGuide()
	return s.handleTubeTVCommercialLibrary(c)
}

func (s *Server) handleTaterTVCommercialFile(c *fiber.Ctx) error {
	cfg, _, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}
	category := taterTVCategoryID(c.Query("category"), "")
	name := taterTVSafeFileName(c.Query("name"))
	if category == "" || name == "" {
		return RespondValidationError(c, "Commercial file is required", "category and name are required")
	}
	path := filepath.Join(taterTVCommercialRoot(cfg), category, name)
	if !isMediaExtension(filepath.Ext(path)) {
		return RespondValidationError(c, "Commercial file type is not supported", filepath.Ext(path))
	}
	if _, err := os.Stat(path); err != nil {
		return RespondNotFound(c, "Commercial file not found", err.Error())
	}
	c.Set("Accept-Ranges", "bytes")
	return c.SendFile(path, false)
}

func taterBuildTVLineup(cfg *config.Config, baseURL, playerToken string) ([]taterTVChannel, error) {
	return taterBuildTVLineupUntil(cfg, baseURL, playerToken, taterTVGuideHorizon.Seconds(), nil)
}

func taterBuildTVLineupUntil(cfg *config.Config, baseURL, playerToken string, minDurationSeconds float64, existing []taterTVChannel) ([]taterTVChannel, error) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	ordered, err := taterTVOrderedSources(cfg, baseURL, playerToken)
	if err != nil {
		return nil, err
	}
	if len(ordered) == 0 {
		return []taterTVChannel{}, nil
	}

	commercials := taterTVCommercialCategories(cfg, baseURL, playerToken)
	channels := []taterTVChannel{}
	existingByNumber := make(map[string]taterTVChannel, len(existing))
	for _, channel := range existing {
		existingByNumber[channel.Number] = channel
	}
	for _, numbered := range taterTVNumberSources(ordered) {
		number := numbered.Number
		source := numbered.Source
		var schedule []map[string]any
		total := 0.0
		if existingChannel, ok := existingByNumber[number]; ok &&
			strings.EqualFold(existingChannel.Title, source.Title) {
			schedule = append(schedule, existingChannel.Schedule...)
			total = existingChannel.TotalDuration
		}
		added, nextTotal := taterTVBuildScheduleUntil(cfg, source, commercials, rng, total, minDurationSeconds)
		schedule = append(schedule, added...)
		total = nextTotal
		if len(schedule) == 0 || total <= 0 {
			continue
		}
		channels = append(channels, taterTVChannel{
			Number:        number,
			Title:         source.Title,
			StreamURL:     taterTVChannelStreamURL(baseURL, number, playerToken),
			LogoPath:      source.LogoPath,
			LogoTitle:     source.LogoTitle,
			LogoPosition:  config.NormalizeTubeTVLogoPosition(source.LogoPosition),
			Schedule:      schedule,
			TotalDuration: total,
		})
	}
	return channels, nil
}

func taterTVNumberSources(sources []taterTVSource) []taterTVNumberedSource {
	reserved := make(map[int]taterTVSource)
	unnumbered := make([]taterTVSource, 0, len(sources))
	for _, source := range sources {
		number, ok := parseTaterTVChannelNumber(source.ChannelNumber)
		if ok {
			if _, exists := reserved[number]; !exists {
				reserved[number] = source
				continue
			}
		}
		unnumbered = append(unnumbered, source)
	}

	numbered := make([]taterTVNumberedSource, 0, len(sources))
	unnumberedIndex := 0
	for channelNumber := 2; channelNumber <= 99 && (unnumberedIndex < len(unnumbered) || len(reserved) > 0); channelNumber++ {
		if source, ok := reserved[channelNumber]; ok {
			numbered = append(numbered, taterTVNumberedSource{
				Number: fmt.Sprintf("%02d", channelNumber),
				Source: source,
			})
			delete(reserved, channelNumber)
			continue
		}
		if unnumberedIndex < len(unnumbered) {
			numbered = append(numbered, taterTVNumberedSource{
				Number: fmt.Sprintf("%02d", channelNumber),
				Source: unnumbered[unnumberedIndex],
			})
			unnumberedIndex++
		}
	}
	return numbered
}

func parseTaterTVChannelNumber(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 2 || number > 99 {
		return 0, false
	}
	return number, true
}

func taterTVOrderedSources(cfg *config.Config, baseURL, playerToken string) ([]taterTVSource, error) {
	sources := []taterTVSource{}
	custom, err := taterTVCustomSources(cfg, baseURL, playerToken)
	if err != nil {
		return nil, err
	}
	sources = append(sources, custom...)
	if cfg.TubeTV.AutoChannels == nil || *cfg.TubeTV.AutoChannels {
		auto, err := taterTVAutoSources(cfg, baseURL, playerToken)
		if err != nil {
			return nil, err
		}
		sources = append(sources, auto...)
	}
	if len(sources) == 0 {
		return []taterTVSource{}, nil
	}

	ordered := []taterTVSource{}
	for _, source := range sources {
		ordered = append(ordered, source)
		if source.SourceType != "custom" {
			ordered = append(ordered, taterTVThemedSources(source)...)
		}
	}
	return ordered, nil
}

func taterTVAutoSources(cfg *config.Config, baseURL, playerToken string) ([]taterTVSource, error) {
	sources := []taterTVSource{}
	for _, cat := range cfg.LocalMedia.Categories {
		if cat.Enabled != nil && !*cat.Enabled {
			continue
		}
		if strings.ToLower(strings.TrimSpace(cat.LibraryType)) == "music" {
			continue
		}
		source := taterTVSource{
			Title:      cleanTaterText(cat.Name),
			SourceType: "auto",
			Seen:       map[string]bool{},
		}
		if source.Title == "" {
			source.Title = "LOCAL"
		}
		refs := []config.TubeTVCustomSource{{CategoryID: cat.ID, SourceIndex: -1}}
		for _, ref := range refs {
			if err := taterTVAddRefToSource(cfg, baseURL, playerToken, &source, ref); err != nil {
				continue
			}
		}
		if len(source.Programs) > 0 || len(source.Groups) > 0 {
			sources = append(sources, source)
		}
	}
	return sources, nil
}

func taterTVCustomSources(cfg *config.Config, baseURL, playerToken string) ([]taterTVSource, error) {
	sources := []taterTVSource{}
	for _, channel := range cfg.TubeTV.CustomChannels {
		source := taterTVSource{
			Title:              cleanTaterText(channel.Title),
			SourceType:         "custom",
			ChannelNumber:      channel.ChannelNumber,
			CommercialCategory: channel.CommercialCategory,
			LogoPath:           config.SanitizeTubeTVLogoPath(channel.LogoPath),
			LogoTitle:          cleanTaterText(channel.LogoTitle),
			LogoPosition:       config.NormalizeTubeTVLogoPosition(channel.LogoPosition),
			Seen:               map[string]bool{},
		}
		if source.Title == "" {
			source.Title = "CUSTOM"
		}
		for _, ref := range channel.Sources {
			if err := taterTVAddRefToSource(cfg, baseURL, playerToken, &source, ref); err != nil {
				continue
			}
		}
		if len(source.Programs) > 0 || len(source.Groups) > 0 {
			sources = append(sources, source)
		}
	}
	return sources, nil
}

func taterTVAddRefToSource(cfg *config.Config, baseURL, playerToken string, source *taterTVSource, ref config.TubeTVCustomSource) error {
	rawCategoryID := strings.TrimSpace(ref.CategoryID)
	if strings.HasPrefix(rawCategoryID, "local-discover:") {
		rows, err := taterLocalDiscoverItems(cfg, baseURL, playerToken, rawCategoryID)
		if err != nil {
			return err
		}
		return taterTVAddRowsToSource(cfg, baseURL, playerToken, source, rawCategoryID, rows, cleanTaterText(ref.Title))
	}
	categoryID := strings.TrimPrefix(rawCategoryID, "local:")
	if categoryID == "" {
		return nil
	}
	if item, ok := taterTVLocalFileFromRef(cfg, baseURL, playerToken, categoryID, ref); ok {
		taterTVAddFile(source, item, cleanTaterText(ref.Title))
		return nil
	}
	sourceIndex := ref.SourceIndex
	rows, err := taterLocalMediaItems(cfg, baseURL, playerToken, categoryID, sourceIndex, ref.Path)
	if err != nil {
		return err
	}
	return taterTVAddRowsToSource(cfg, baseURL, playerToken, source, categoryID, rows, cleanTaterText(ref.Title))
}

func taterTVLocalFileFromRef(cfg *config.Config, baseURL, playerToken, categoryID string, ref config.TubeTVCustomSource) (taterUsenetItem, bool) {
	relPath := cleanLocalRelativePath(ref.Path)
	if relPath == "" || ref.SourceIndex < 0 {
		return taterUsenetItem{}, false
	}
	cat, ok := taterLocalMediaCategory(cfg, categoryID)
	if !ok {
		return taterUsenetItem{}, false
	}
	paths := taterLocalMediaCategoryPaths(cat)
	if ref.SourceIndex >= len(paths) {
		return taterUsenetItem{}, false
	}
	path, err := safeLocalPath(paths[ref.SourceIndex], relPath)
	if err != nil {
		return taterUsenetItem{}, false
	}
	info, err := os.Stat(path)
	if err != nil || info == nil || info.IsDir() || !isLocalStreamExtension(filepath.Ext(path)) {
		return taterUsenetItem{}, false
	}
	mediaType := strings.ToLower(strings.TrimSpace(ref.MediaType))
	if mediaType == "" {
		switch strings.ToLower(strings.TrimSpace(cat.LibraryType)) {
		case "tv":
			mediaType = "episode"
		case "movies":
			mediaType = "movie"
		default:
			mediaType = "video"
		}
	}
	title := cleanTaterText(ref.Title)
	if title == "" {
		switch mediaType {
		case "episode":
			title = cleanEpisodeTitle(strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath)))
		default:
			title = cleanMovieTitleFromName(strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath)))
		}
	}
	item := taterUsenetItem{
		Title:       title,
		Type:        "localFile",
		MediaType:   mediaType,
		CategoryID:  "local:" + categoryID,
		SourceIndex: ref.SourceIndex,
		Path:        relPath,
		StreamURL:   taterLocalStreamURL(baseURL, categoryID, ref.SourceIndex, relPath, playerToken),
		SeekMode:    taterLocalSeekMode(cfg, filepath.Ext(path)),
		SizeBytes:   info.Size(),
	}
	if item.SizeBytes > 0 {
		item.SizeText = formatTaterBytes(item.SizeBytes)
	}
	attachTaterLocalDuration(cfg, path, &item)
	return item, true
}

func taterTVAddRowsToSource(cfg *config.Config, baseURL, playerToken string, source *taterTVSource, categoryID string, rows []taterUsenetItem, showTitle string) error {
	for _, row := range rows {
		switch strings.ToLower(row.Type) {
		case "localfile":
			taterTVAddFile(source, row, showTitle)
		case "localfolder":
			nextShowTitle := showTitle
			if strings.ToLower(row.MediaType) == "show" {
				nextShowTitle = row.Title
			}
			nextRows, err := taterLocalMediaItems(cfg, baseURL, playerToken, strings.TrimPrefix(row.CategoryID, "local:"), row.SourceIndex, row.Path)
			if err != nil {
				continue
			}
			if err := taterTVAddRowsToSource(cfg, baseURL, playerToken, source, categoryID, nextRows, nextShowTitle); err != nil {
				return err
			}
		}
	}
	return nil
}

func taterTVAddFile(source *taterTVSource, item taterUsenetItem, showTitle string) {
	if source.Seen == nil {
		source.Seen = map[string]bool{}
	}
	key := taterTVMediaKey(item)
	if key != "" && source.Seen[key] {
		return
	}
	if key != "" {
		source.Seen[key] = true
	}
	if strings.ToLower(item.MediaType) == "episode" {
		title := cleanTaterText(showTitle)
		if title == "" {
			title = cleanShowTitle(taterSeriesKeyFromPath(item.Path))
		}
		for i := range source.Groups {
			if source.Groups[i].Title == title {
				source.Groups[i].Episodes = append(source.Groups[i].Episodes, item)
				return
			}
		}
		source.Groups = append(source.Groups, taterTVEpisodeGroup{Title: title, Episodes: []taterUsenetItem{item}})
		return
	}
	source.Programs = append(source.Programs, item)
}

func taterTVThemedSources(source taterTVSource) []taterTVSource {
	out := []taterTVSource{}
	addMovieTheme := func(title string, terms []string, min int) {
		programs := []taterUsenetItem{}
		for _, item := range source.Programs {
			if taterTVItemHasAnyTerm(item, terms) {
				programs = append(programs, item)
			}
		}
		if len(programs) >= min {
			out = append(out, taterTVSource{Title: taterTVThemedTitle(source.Title, title), SourceType: "auto_theme", CommercialCategory: source.CommercialCategory, Programs: programs})
		}
	}
	addTVTheme := func(title string, terms []string, min int) {
		groups := []taterTVEpisodeGroup{}
		for _, group := range source.Groups {
			if taterTVGroupHasAnyTerm(group, terms) {
				groups = append(groups, group)
			}
		}
		if len(groups) >= min {
			out = append(out, taterTVSource{Title: taterTVThemedTitle(source.Title, title), SourceType: "auto_theme", CommercialCategory: source.CommercialCategory, Groups: groups})
		}
	}

	addMovieTheme("ACTION MOVIES", []string{"action", "adventure", "thriller"}, 2)
	addMovieTheme("COMEDY MOVIES", []string{"comedy"}, 2)
	addMovieTheme("HORROR MOVIES", []string{"horror"}, 2)
	addMovieTheme("SCI-FI MOVIES", []string{"science fiction", "sci-fi", "sci fi", "scifi"}, 2)
	addMovieTheme("FAMILY MOVIES", []string{"family", "children", "kids", "disney", "pixar"}, 2)
	addMovieTheme("CARTOON MOVIES", []string{"animation", "animated", "anime", "cartoon"}, 2)
	addMovieTheme("DOCUMENTARY MOVIES", []string{"documentary", "docu"}, 2)
	addMovieTheme("DRAMA MOVIES", []string{"drama"}, 2)
	addMovieTheme("CRIME MOVIES", []string{"crime", "mystery"}, 2)

	addTVTheme("CARTOON CHANNEL", []string{"animation", "animated", "anime", "cartoon", "children", "kids", "looney"}, 1)
	addTVTheme("COMEDY CHANNEL", []string{"comedy"}, 1)
	addTVTheme("DRAMA CHANNEL", []string{"drama"}, 1)
	addTVTheme("SCI-FI CHANNEL", []string{"science fiction", "sci-fi", "sci fi", "scifi"}, 1)
	addTVTheme("ACTION CHANNEL", []string{"action", "adventure"}, 1)
	addTVTheme("CRIME CHANNEL", []string{"crime", "mystery"}, 1)
	addTVTheme("DOCUMENTARY CHANNEL", []string{"documentary", "docu"}, 1)
	for _, decade := range []int{1950, 1960, 1970, 1980, 1990, 2000, 2010, 2020} {
		movies := []taterUsenetItem{}
		for _, item := range source.Programs {
			year := taterLocalDiscoverYear(item)
			if year >= decade && year < decade+10 {
				movies = append(movies, item)
			}
		}
		if len(movies) >= 2 {
			out = append(out, taterTVSource{Title: taterTVThemedTitle(source.Title, taterTVDecadeLabel(decade)+" MOVIES"), SourceType: "auto_theme", Programs: movies})
		}
		groups := []taterTVEpisodeGroup{}
		for _, group := range source.Groups {
			year := taterTVGroupYear(group)
			if year >= decade && year < decade+10 {
				groups = append(groups, group)
			}
		}
		if len(groups) >= 1 {
			out = append(out, taterTVSource{Title: taterTVThemedTitle(source.Title, taterTVDecadeLabel(decade)+" TV"), SourceType: "auto_theme", Groups: groups})
		}
	}
	return taterTVDedupeSources(out)
}

func taterTVBuildSchedule(cfg *config.Config, source taterTVSource, commercialCategories []taterTVCommercialCategory, rng *rand.Rand) ([]map[string]any, float64) {
	return taterTVBuildSchedulePass(cfg, source, commercialCategories, rng, 0)
}

func taterTVBuildScheduleUntil(cfg *config.Config, source taterTVSource, commercialCategories []taterTVCommercialCategory, rng *rand.Rand, start, minEnd float64) ([]map[string]any, float64) {
	schedule := []map[string]any{}
	total := start
	for total < minEnd {
		before := total
		var added []map[string]any
		added, total = taterTVBuildSchedulePass(cfg, source, commercialCategories, rng, total)
		schedule = append(schedule, added...)
		if total <= before {
			break
		}
	}
	if len(schedule) == 0 && start <= 0 {
		return taterTVBuildSchedulePass(cfg, source, commercialCategories, rng, 0)
	}
	return schedule, total
}

func taterTVBuildSchedulePass(cfg *config.Config, source taterTVSource, commercialCategories []taterTVCommercialCategory, rng *rand.Rand, start float64) ([]map[string]any, float64) {
	commercials := taterTVCommercialPool(cfg, commercialCategories, source.CommercialCategory)
	deck := []taterTVCommercial{}
	schedule := []map[string]any{}
	total := start
	if len(source.Groups) > 0 && len(source.Programs) == 0 {
		total = taterTVAppendEpisodeSchedule(cfg, schedule, &schedule, total, source.Groups, commercials, &deck, rng)
	} else if len(source.Groups) > 0 {
		total = taterTVAppendMixedSchedule(cfg, schedule, &schedule, total, source.Programs, source.Groups, commercials, &deck, rng)
	} else {
		total = taterTVAppendMovieSchedule(cfg, schedule, &schedule, total, source.Programs, commercials, &deck, rng)
	}
	return schedule, total
}

func taterTVAppendMovieSchedule(cfg *config.Config, _ []map[string]any, schedule *[]map[string]any, start float64, programs []taterUsenetItem, commercials []taterTVCommercial, deck *[]taterTVCommercial, rng *rand.Rand) float64 {
	total := start
	for _, item := range taterTVShuffleItems(programs, rng) {
		total = taterTVAppendProgram(cfg, schedule, total, item, "movie", commercials, deck, rng)
		total = taterTVAppendCommercialBreak(cfg, schedule, total, commercials, deck, rng)
	}
	return total
}

func taterTVAppendEpisodeSchedule(cfg *config.Config, _ []map[string]any, schedule *[]map[string]any, start float64, groups []taterTVEpisodeGroup, commercials []taterTVCommercial, deck *[]taterTVCommercial, rng *rand.Rand) float64 {
	total := start
	states := make([][]taterUsenetItem, 0, len(groups))
	totalEpisodes := 0
	for _, group := range groups {
		episodes := append([]taterUsenetItem{}, group.Episodes...)
		sort.SliceStable(episodes, func(i, j int) bool {
			return taterEpisodeSortKey(episodes[i].Path) < taterEpisodeSortKey(episodes[j].Path)
		})
		if len(episodes) == 0 {
			continue
		}
		states = append(states, episodes)
		totalEpisodes += len(episodes)
	}
	next := make([]int, len(states))
	for i := range next {
		next[i] = rng.Intn(len(states[i]))
	}
	lastKey := ""
	for slot := 0; slot < totalEpisodes; slot++ {
		if len(states) == 0 {
			break
		}
		stateIndex := rng.Intn(len(states))
		episode := states[stateIndex][next[stateIndex]%len(states[stateIndex])]
		next[stateIndex]++
		if len(states[stateIndex]) > 1 && taterTVMediaKey(episode) == lastKey {
			episode = states[stateIndex][next[stateIndex]%len(states[stateIndex])]
			next[stateIndex]++
		}
		lastKey = taterTVMediaKey(episode)
		total = taterTVAppendProgram(cfg, schedule, total, episode, "episode", commercials, deck, rng)
		total = taterTVAppendCommercialBreak(cfg, schedule, total, commercials, deck, rng)
	}
	return total
}

func taterTVAppendMixedSchedule(cfg *config.Config, _ []map[string]any, schedule *[]map[string]any, start float64, programs []taterUsenetItem, groups []taterTVEpisodeGroup, commercials []taterTVCommercial, deck *[]taterTVCommercial, rng *rand.Rand) float64 {
	total := start
	movies := taterTVShuffleItems(programs, rng)
	movieIndex := 0
	for movieIndex < len(movies) || len(groups) > 0 {
		if len(groups) > 0 && (movieIndex >= len(movies) || rng.Float64() < 0.55) {
			total = taterTVAppendEpisodeSchedule(cfg, nil, schedule, total, groups, commercials, deck, rng)
			groups = nil
			continue
		}
		if movieIndex < len(movies) {
			total = taterTVAppendProgram(cfg, schedule, total, movies[movieIndex], "movie", commercials, deck, rng)
			total = taterTVAppendCommercialBreak(cfg, schedule, total, commercials, deck, rng)
			movieIndex++
		}
	}
	return total
}

func taterTVAppendProgram(cfg *config.Config, schedule *[]map[string]any, start float64, item taterUsenetItem, kind string, commercials []taterTVCommercial, deck *[]taterTVCommercial, rng *rand.Rand) float64 {
	duration := taterTVDuration(item, kind)
	if cfg.TubeTV.MidrollCommercials == nil || !*cfg.TubeTV.MidrollCommercials || len(commercials) == 0 || duration < 1200 {
		return taterTVAppendScheduleItem(schedule, item, kind, start, duration, 0, false)
	}
	count := 1
	if duration >= 7200 {
		count = 3
	} else if duration >= 3600 {
		count = 2
	}
	total := start
	cursor := 0.0
	for i := 1; i <= count; i++ {
		offset := duration * float64(i) / float64(count+1)
		segment := math.Max(5, offset-cursor)
		total = taterTVAppendScheduleItem(schedule, item, kind, total, segment, cursor, true)
		total = taterTVAppendCommercialBreak(cfg, schedule, total, commercials, deck, rng)
		cursor = offset
	}
	if duration-cursor >= 5 {
		total = taterTVAppendScheduleItem(schedule, item, kind, total, duration-cursor, cursor, false)
	}
	return total
}

func taterTVAppendScheduleItem(schedule *[]map[string]any, item taterUsenetItem, kind string, start, duration, mediaOffset float64, forceAdvance bool) float64 {
	if item.StreamURL == "" {
		return start
	}
	row := taterTVMediaMap(item)
	row["kind"] = kind
	row["duration"] = duration
	row["durationKnown"] = item.DurationSeconds > 0
	row["fullDuration"] = taterTVDuration(item, kind)
	row["mediaOffset"] = mediaOffset
	row["forceAdvance"] = forceAdvance
	row["start"] = start
	row["end"] = start + duration
	*schedule = append(*schedule, row)
	return start + duration
}

func taterTVAppendCommercialBreak(cfg *config.Config, schedule *[]map[string]any, start float64, commercials []taterTVCommercial, deck *[]taterTVCommercial, rng *rand.Rand) float64 {
	if cfg.TubeTV.CommercialsEnabled != nil && !*cfg.TubeTV.CommercialsEnabled {
		return start
	}
	if len(commercials) == 0 {
		return start
	}
	total := start
	count := 2 + rng.Intn(3)
	for i := 0; i < count; i++ {
		commercial := taterTVNextCommercial(commercials, deck, rng)
		row := map[string]any{
			"title":         commercial.Title,
			"kind":          "commercial",
			"categoryId":    commercial.CategoryID,
			"category":      commercial.Category,
			"name":          commercial.Name,
			"url":           commercial.URL,
			"local":         true,
			"duration":      commercial.Duration,
			"durationKnown": commercial.DurationKnown,
			"fullDuration":  commercial.FullDuration,
			"mediaOffset":   0,
			"forceAdvance":  false,
			"start":         total,
			"end":           total + commercial.Duration,
		}
		*schedule = append(*schedule, row)
		total += commercial.Duration
	}
	return total
}

func taterTVCommercialCategories(cfg *config.Config, baseURL, playerToken string) []taterTVCommercialCategory {
	root := taterTVCommercialRoot(cfg)
	entries, err := os.ReadDir(root)
	if err != nil {
		return []taterTVCommercialCategory{}
	}
	categories := []taterTVCommercialCategory{}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		id := taterTVCategoryID(entry.Name(), "")
		if id == "" {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		files, _ := os.ReadDir(dir)
		videos := []taterTVCommercial{}
		for _, file := range files {
			if file.IsDir() || strings.HasPrefix(file.Name(), ".") || !isMediaExtension(filepath.Ext(file.Name())) {
				continue
			}
			title := cleanMovieTitleFromName(strings.TrimSuffix(file.Name(), filepath.Ext(file.Name())))
			if title == "" {
				title = "Commercial"
			}
			path := filepath.Join(dir, file.Name())
			duration, durationKnown := taterTVCommercialDuration(cfg, path)
			videos = append(videos, taterTVCommercial{
				Title:         title,
				CategoryID:    id,
				Category:      cleanTaterText(entry.Name()),
				Name:          file.Name(),
				URL:           taterTVCommercialURL(baseURL, id, file.Name(), playerToken),
				Kind:          "commercial",
				Local:         false,
				Duration:      duration,
				FullDuration:  duration,
				DurationKnown: durationKnown,
			})
		}
		sort.SliceStable(videos, func(i, j int) bool {
			return strings.ToLower(videos[i].Title) < strings.ToLower(videos[j].Title)
		})
		categories = append(categories, taterTVCommercialCategory{
			ID:     id,
			Title:  cleanTaterText(entry.Name()),
			Count:  len(videos),
			Videos: videos,
		})
	}
	sort.SliceStable(categories, func(i, j int) bool {
		return strings.ToLower(categories[i].Title) < strings.ToLower(categories[j].Title)
	})
	return categories
}

func taterTVCommercialDuration(cfg *config.Config, path string) (float64, bool) {
	duration := taterLocalDurationSeconds(cfg, path)
	if duration > 0 && !math.IsNaN(duration) && !math.IsInf(duration, 0) {
		return math.Max(1, duration), true
	}
	return 30, false
}

func taterTVCommercialPool(cfg *config.Config, categories []taterTVCommercialCategory, channelCategory string) []taterTVCommercial {
	selected := map[string]bool{}
	for _, id := range cfg.TubeTV.CommercialCategories {
		selected[taterTVCategoryID(id, "")] = true
	}
	channelCategory = taterTVCategoryID(channelCategory, "")
	if channelCategory != "" {
		selected = map[string]bool{channelCategory: true}
	}
	pool := []taterTVCommercial{}
	for _, category := range categories {
		if len(selected) > 0 && !selected[category.ID] {
			continue
		}
		pool = append(pool, category.Videos...)
	}
	return pool
}

func taterTVNextCommercial(pool []taterTVCommercial, deck *[]taterTVCommercial, rng *rand.Rand) taterTVCommercial {
	if len(*deck) == 0 {
		*deck = append([]taterTVCommercial{}, pool...)
		rng.Shuffle(len(*deck), func(i, j int) {
			(*deck)[i], (*deck)[j] = (*deck)[j], (*deck)[i]
		})
	}
	next := (*deck)[0]
	*deck = (*deck)[1:]
	return next
}

func taterTVCommercialRoot(cfg *config.Config) string {
	if cfg == nil || strings.TrimSpace(cfg.TubeTV.CommercialsPath) == "" {
		return filepath.Join(os.TempDir(), "tater-tube-commercials")
	}
	return filepath.Clean(cfg.TubeTV.CommercialsPath)
}

func taterTVCommercialURL(baseURL, category, name, playerToken string) string {
	if strings.TrimSpace(baseURL) == "" {
		return ""
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/api/tater/tv/commercials/file")
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("category", taterTVCategoryID(category, ""))
	q.Set("name", taterTVSafeFileName(name))
	q.Set("player_token", playerToken)
	u.RawQuery = q.Encode()
	return u.String()
}

func tubeTVLocalLibraryRoot(cfg *config.Config) tubeTVLocalLibraryResponse {
	rows := []tubeTVLocalLibraryRow{}
	for _, row := range taterLocalDiscoverRows(cfg) {
		rows = append(rows, tubeTVLocalLibraryRow{
			ID:          row.ID,
			Title:       row.Title,
			Detail:      row.Detail,
			Type:        "localDiscover",
			MediaType:   tubeTVDiscoverMediaType(row.ID),
			CategoryID:  row.ID,
			SourceIndex: -1,
			Count:       row.Count,
			Selectable:  true,
			Browsable:   true,
		})
	}
	for _, cat := range cfg.LocalMedia.Categories {
		if cat.Enabled != nil && !*cat.Enabled {
			continue
		}
		if strings.ToLower(strings.TrimSpace(cat.LibraryType)) == "music" {
			continue
		}
		id := strings.TrimSpace(cat.ID)
		title := cleanTaterText(cat.Name)
		if id == "" || title == "" || len(taterLocalMediaCategoryPaths(cat)) == 0 {
			continue
		}
		rows = append(rows, tubeTVLocalLibraryRow{
			ID:          id,
			Title:       title,
			Detail:      strings.ToUpper(strings.TrimSpace(cat.LibraryType)),
			Type:        "localCategory",
			MediaType:   strings.ToLower(strings.TrimSpace(cat.LibraryType)),
			CategoryID:  id,
			SourceIndex: -1,
			Count:       len(taterLocalMediaCategoryPaths(cat)),
			Selectable:  true,
			Browsable:   true,
		})
	}
	return tubeTVLocalLibraryResponse{
		Title:       "Local Library",
		SourceIndex: -1,
		Rows:        rows,
	}
}

func tubeTVLocalDiscoverTitle(id string) string {
	for _, def := range taterLocalDiscoverDefinitions() {
		if def.ID == id {
			return def.Title
		}
	}
	return cleanTaterText(strings.TrimPrefix(id, "local-discover:"))
}

func tubeTVDiscoverMediaType(id string) string {
	key := strings.TrimPrefix(id, "local-discover:")
	switch {
	case key == "movies" || strings.HasPrefix(key, "genre:") || strings.HasPrefix(key, "decade:"):
		return "movie"
	case key == "series":
		return "show"
	default:
		return ""
	}
}

func tubeTVLocalLibraryItemRows(items []taterUsenetItem) []tubeTVLocalLibraryRow {
	rows := make([]tubeTVLocalLibraryRow, 0, len(items))
	for _, item := range items {
		itemType := strings.ToLower(strings.TrimSpace(item.Type))
		mediaType := strings.ToLower(strings.TrimSpace(item.MediaType))
		browsable := itemType == "localfolder"
		selectable := itemType == "localfile" || itemType == "localfolder"
		detail := strings.TrimSpace(item.SizeText)
		if detail == "" {
			detail = strings.TrimSpace(item.Category)
		}
		if item.Date != "" && detail != "" && !strings.Contains(detail, item.Date) {
			detail = item.Date + " / " + detail
		} else if item.Date != "" {
			detail = item.Date
		}
		rows = append(rows, tubeTVLocalLibraryRow{
			ID:          taterTVMediaKey(item),
			Title:       item.Title,
			Detail:      detail,
			Type:        item.Type,
			MediaType:   mediaType,
			CategoryID:  item.CategoryID,
			SourceIndex: item.SourceIndex,
			Path:        item.Path,
			Selectable:  selectable,
			Browsable:   browsable,
		})
	}
	return rows
}

func taterTVChannelStreamURL(baseURL, number, playerToken string) string {
	if strings.TrimSpace(baseURL) == "" {
		return ""
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/api/tater/tv/channel/" + url.PathEscape(number) + "/playlist.m3u8")
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("player_token", playerToken)
	u.RawQuery = q.Encode()
	return u.String()
}

func taterTVEnsureGuide(cfg *config.Config, baseURL string, now time.Time) (taterTVGuideCacheEntry, error) {
	taterTVGuideMu.Lock()
	defer taterTVGuideMu.Unlock()

	if now.IsZero() {
		now = time.Now()
	}
	if taterTVGuideCache == nil || taterTVGuideCache.StartedAt.IsZero() || now.After(taterTVGuideCache.PlannedUntil.Add(-taterTVGuideRefillThreshold)) {
		targetEndSeconds := taterTVGuideHorizon.Seconds()
		var existing []taterTVChannel
		startedAt := now.Truncate(time.Second)
		generatedAt := now
		if taterTVGuideCache != nil && !taterTVGuideCache.StartedAt.IsZero() {
			startedAt = taterTVGuideCache.StartedAt
			generatedAt = taterTVGuideCache.GeneratedAt
			existing = taterTVGuideCache.Channels
			elapsed := math.Max(0, now.Sub(startedAt).Seconds())
			targetEndSeconds = elapsed + taterTVGuideHorizon.Seconds()
		}
		channels, err := taterBuildTVLineupUntil(cfg, baseURL, "", targetEndSeconds, existing)
		if err != nil {
			return taterTVGuideCacheEntry{}, err
		}
		plannedUntil := startedAt
		if len(channels) > 0 {
			minDuration := channels[0].TotalDuration
			for _, channel := range channels[1:] {
				if channel.TotalDuration < minDuration {
					minDuration = channel.TotalDuration
				}
			}
			plannedUntil = startedAt.Add(time.Duration(minDuration * float64(time.Second)))
		}
		taterTVGuideCache = &taterTVGuideCacheEntry{
			Channels:     channels,
			StartedAt:    startedAt,
			GeneratedAt:  generatedAt,
			UpdatedAt:    now,
			PlannedUntil: plannedUntil,
		}
	}
	return taterTVCloneGuide(*taterTVGuideCache), nil
}

func taterTVResetGuide() {
	taterTVGuideMu.Lock()
	defer taterTVGuideMu.Unlock()
	taterTVGuideCache = nil
}

func taterTVGuideResponse(entry taterTVGuideCacheEntry, baseURL, playerToken string) tubeTVGuideResponse {
	return tubeTVGuideResponse{
		Channels:             taterTVPersonalizeChannels(entry.Channels, baseURL, playerToken),
		StartedAt:            entry.StartedAt,
		GeneratedAt:          entry.GeneratedAt,
		UpdatedAt:            entry.UpdatedAt,
		PlannedUntil:         entry.PlannedUntil,
		HorizonHours:         int(taterTVGuideHorizon / time.Hour),
		RefillThresholdHours: int(taterTVGuideRefillThreshold / time.Hour),
	}
}

func taterTVPersonalizeChannels(channels []taterTVChannel, baseURL, playerToken string) []taterTVChannel {
	out := make([]taterTVChannel, 0, len(channels))
	for _, channel := range channels {
		next := channel
		next.StreamURL = taterTVChannelStreamURL(baseURL, channel.Number, playerToken)
		next.Schedule = cloneTaterTVSchedule(channel.Schedule)
		out = append(out, next)
	}
	return out
}

func taterTVCloneGuide(entry taterTVGuideCacheEntry) taterTVGuideCacheEntry {
	entry.Channels = taterTVPersonalizeChannels(entry.Channels, "", "")
	return entry
}

func cloneTaterTVSchedule(schedule []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(schedule))
	for _, row := range schedule {
		next := make(map[string]any, len(row))
		for key, value := range row {
			next[key] = value
		}
		out = append(out, next)
	}
	return out
}

func taterTVGuideConfigChanged(oldConfig, newConfig *config.Config) bool {
	if oldConfig == nil || newConfig == nil {
		return true
	}
	return !reflect.DeepEqual(oldConfig.TubeTV, newConfig.TubeTV) ||
		!reflect.DeepEqual(oldConfig.LocalMedia, newConfig.LocalMedia)
}

func taterTVMediaMap(item taterUsenetItem) map[string]any {
	return map[string]any{
		"title":           item.Title,
		"type":            item.Type,
		"mediaType":       item.MediaType,
		"categoryId":      item.CategoryID,
		"sourceIndex":     item.SourceIndex,
		"path":            item.Path,
		"streamUrl":       item.StreamURL,
		"seekMode":        item.SeekMode,
		"date":            item.Date,
		"description":     item.Description,
		"category":        item.Category,
		"duration":        item.Duration,
		"durationSeconds": item.DurationSeconds,
		"sizeText":        item.SizeText,
	}
}

func taterTVDuration(item taterUsenetItem, kind string) float64 {
	if item.DurationSeconds > 0 {
		return math.Max(5, item.DurationSeconds)
	}
	if item.Duration > 0 {
		return math.Max(5, float64(item.Duration))
	}
	if kind == "movie" {
		return 5400
	}
	return 600
}

func taterTVShuffleItems(items []taterUsenetItem, rng *rand.Rand) []taterUsenetItem {
	out := append([]taterUsenetItem{}, items...)
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func taterTVMediaKey(item taterUsenetItem) string {
	for _, value := range []string{item.StreamURL, item.RatingKey, item.Key, item.PartKey, item.Path, item.Title} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func taterTVText(item taterUsenetItem) string {
	return taterLocalDiscoverNormalize(strings.Join([]string{
		item.Title,
		item.Path,
		item.Category,
		item.Description,
		item.Date,
		item.MediaType,
	}, " "))
}

func taterTVItemHasAnyTerm(item taterUsenetItem, terms []string) bool {
	text := taterTVText(item)
	for _, term := range terms {
		if strings.Contains(text, taterLocalDiscoverNormalize(term)) {
			return true
		}
	}
	return false
}

func taterTVGroupHasAnyTerm(group taterTVEpisodeGroup, terms []string) bool {
	title := taterLocalDiscoverNormalize(group.Title)
	for _, term := range terms {
		if strings.Contains(title, taterLocalDiscoverNormalize(term)) {
			return true
		}
	}
	for _, item := range group.Episodes {
		if taterTVItemHasAnyTerm(item, terms) {
			return true
		}
	}
	return false
}

func taterTVGroupYear(group taterTVEpisodeGroup) int {
	if match := localYearPattern.FindStringSubmatch(group.Title); len(match) > 1 {
		year, _ := strconv.Atoi(match[1])
		return year
	}
	for _, item := range group.Episodes {
		if year := taterLocalDiscoverYear(item); year > 0 {
			return year
		}
	}
	return 0
}

func taterTVThemedTitle(sourceTitle, title string) string {
	source := strings.ToUpper(strings.TrimSpace(sourceTitle))
	clean := strings.ToUpper(strings.TrimSpace(title))
	switch source {
	case "", "LOCAL", "MOVIES", "MOVIE", "TV", "TELEVISION", "SHOWS", "SERIES", "VIDEO":
		return clean
	}
	if strings.HasPrefix(clean, source) {
		return clean
	}
	return source + " " + clean
}

func taterTVDecadeLabel(decade int) string {
	if decade >= 2000 {
		return fmt.Sprintf("%dS", decade)
	}
	return fmt.Sprintf("%02dS", (decade/10)%100)
}

func taterTVDedupeSources(sources []taterTVSource) []taterTVSource {
	seen := map[string]bool{}
	out := []taterTVSource{}
	for _, source := range sources {
		keys := []string{source.Title}
		for _, item := range source.Programs {
			keys = append(keys, taterTVMediaKey(item))
		}
		for _, group := range source.Groups {
			keys = append(keys, group.Title)
		}
		sort.Strings(keys)
		key := strings.Join(keys, "|")
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, source)
	}
	return out
}

func taterTVSafeName(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	value = strings.ReplaceAll(value, "/", " ")
	value = strings.ReplaceAll(value, "\\", " ")
	value = cleanTaterText(value)
	if value == "" {
		return ""
	}
	return value
}

func taterTVCategoryID(value, fallback string) string {
	id := taterTVSlug(value)
	if id == "" {
		id = taterTVSlug(fallback)
	}
	return id
}

func taterTVSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		isAllowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAllowed {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(b.String(), "-")
	if len(result) > 64 {
		result = strings.TrimRight(result[:64], "-")
	}
	return result
}

func taterTVSafeFileName(value string) string {
	name := filepath.Base(strings.TrimSpace(value))
	name = strings.ReplaceAll(name, "/", "")
	name = strings.ReplaceAll(name, "\\", "")
	if name == "." || name == "" {
		return "commercial.mp4"
	}
	return name
}
