package api

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/TaterTotterson/tater-tube-server/internal/database"
	"github.com/TaterTotterson/tater-tube-server/internal/httpclient"
	"github.com/TaterTotterson/tater-tube-server/internal/importer/parser/fileinfo"
	"github.com/TaterTotterson/tater-tube-server/internal/importer/utils/nzbtrim"
	"github.com/gofiber/fiber/v2"
)

const taterUsenetMaxNzbFetchSize = 100 * 1024 * 1024

type taterUsenetCategory struct {
	ID        string                `json:"id,omitempty"`
	Title     string                `json:"title"`
	Detail    string                `json:"detail,omitempty"`
	FullTitle string                `json:"fullTitle,omitempty"`
	Type      string                `json:"type,omitempty"`
	Category  string                `json:"category,omitempty"`
	Time      string                `json:"time,omitempty"`
	Group     string                `json:"group,omitempty"`
	IsGroup   bool                  `json:"isGroup,omitempty"`
	IsSubcat  bool                  `json:"isSubcat,omitempty"`
	Count     int                   `json:"count,omitempty"`
	Children  []taterUsenetCategory `json:"children,omitempty"`
}

type taterUsenetItem struct {
	Title           string  `json:"title"`
	Key             string  `json:"key,omitempty"`
	RatingKey       string  `json:"ratingKey,omitempty"`
	PartKey         string  `json:"partKey,omitempty"`
	NzbURL          string  `json:"nzbUrl"`
	Type            string  `json:"type,omitempty"`
	MediaType       string  `json:"mediaType,omitempty"`
	Artist          string  `json:"artist,omitempty"`
	Album           string  `json:"album,omitempty"`
	SearchQuery     string  `json:"searchQuery,omitempty"`
	CategoryID      string  `json:"categoryId,omitempty"`
	SourceIndex     int     `json:"sourceIndex,omitempty"`
	Path            string  `json:"path,omitempty"`
	StreamURL       string  `json:"streamUrl,omitempty"`
	SeekMode        string  `json:"seekMode,omitempty"`
	GUID            string  `json:"guid,omitempty"`
	Date            string  `json:"date,omitempty"`
	Description     string  `json:"description,omitempty"`
	Category        string  `json:"category,omitempty"`
	Poster          string  `json:"poster,omitempty"`
	Files           string  `json:"files,omitempty"`
	Grabs           string  `json:"grabs,omitempty"`
	Index           int     `json:"index,omitempty"`
	Duration        int64   `json:"duration,omitempty"`
	DurationSeconds float64 `json:"durationSeconds,omitempty"`
	PlayStateID     string  `json:"playStateId,omitempty"`
	SeriesStateID   string  `json:"seriesStateId,omitempty"`
	ViewOffset      int64   `json:"viewOffset,omitempty"`
	ViewOffsetSec   float64 `json:"viewOffsetSeconds,omitempty"`
	ProgressPercent float64 `json:"progressPercent,omitempty"`
	LeafCount       int     `json:"leafCount,omitempty"`
	SizeBytes       int64   `json:"sizeBytes,omitempty"`
	SizeText        string  `json:"sizeText,omitempty"`
	DurationDisplay string  `json:"durationDisplay,omitempty"`
	ModuleID        string  `json:"moduleId,omitempty"`
	ChannelNumber   string  `json:"channelNumber,omitempty"`
	ChannelName     string  `json:"channelName,omitempty"`
}

type taterUsenetPlayRequest struct {
	NzbURL   string `json:"nzb_url" form:"nzb_url"`
	Title    string `json:"title" form:"title"`
	Category string `json:"category" form:"category"`
	Timeout  int    `json:"timeout" form:"timeout"`
}

type newznabCaps struct {
	Error      *newznabError     `xml:"error"`
	Categories []newznabCategory `xml:"categories>category"`
}

type newznabCategory struct {
	ID      string          `xml:"id,attr"`
	Name    string          `xml:"name,attr"`
	Subcats []newznabSubcat `xml:"subcat"`
}

type newznabSubcat struct {
	ID   string `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

type newznabRSS struct {
	Error   *newznabError `xml:"error"`
	Channel struct {
		Items []newznabRSSItem `xml:"item"`
	} `xml:"channel"`
}

type newznabRSSItem struct {
	Title       string        `xml:"title"`
	Link        string        `xml:"link"`
	GUID        string        `xml:"guid"`
	PubDate     string        `xml:"pubDate"`
	Description string        `xml:"description"`
	Enclosure   newznabEncl   `xml:"enclosure"`
	Attrs       []newznabAttr `xml:"attr"`
}

type newznabEncl struct {
	URL    string `xml:"url,attr"`
	Length string `xml:"length,attr"`
}

type newznabAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type newznabError struct {
	Code        string `xml:"code,attr"`
	Description string `xml:"description,attr"`
}

type cinemetaCatalogResponse struct {
	Metas []cinemetaMeta `json:"metas"`
}

type cinemetaMeta struct {
	ID          string   `json:"id"`
	IMDBID      string   `json:"imdb_id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Year        string   `json:"year"`
	ReleaseInfo string   `json:"releaseInfo"`
	Released    string   `json:"released"`
	Description string   `json:"description"`
	Poster      string   `json:"poster"`
	Genre       []string `json:"genre"`
	Genres      []string `json:"genres"`
}

func (s *Server) handleTaterUsenetStatus(c *fiber.Ctx) error {
	cfg, _, ok := s.taterUsenetAuthorizedConfig(c)
	if !ok {
		return nil
	}
	return RespondSuccess(c, fiber.Map{
		"configured": taterTubeTVEnabled(cfg) || taterNewznabEnabled(cfg) || taterLocalMediaEnabled(cfg),
	})
}

func (s *Server) handleTaterUsenetCatalog(c *fiber.Ctx) error {
	cfg, _, ok := s.taterUsenetAuthorizedConfig(c)
	if !ok {
		return nil
	}
	if !taterTubeTVEnabled(cfg) && !taterNewznabEnabled(cfg) && !taterLocalMediaEnabled(cfg) {
		return RespondServiceUnavailable(c, "Stream catalog is not configured", "")
	}

	categories := []taterUsenetCategory{}
	if taterTubeTVEnabled(cfg) {
		categories = append(categories, taterUsenetCategory{
			ID:     "tube-tv",
			Title:  "Tube TV",
			Detail: "SERVER",
			Type:   "tubeTv",
		})
	}
	if taterNewznabEnabled(cfg) {
		body, err := taterFetchNewznab(c.Context(), cfg, map[string]string{"t": "caps"})
		if err != nil {
			return RespondServiceUnavailable(c, "Failed to load Newznab categories", err.Error())
		}
		children, err := parseTaterNewznabCategories(body)
		if err != nil {
			return RespondServiceUnavailable(c, "Failed to parse Newznab categories", err.Error())
		}
		streamChildren := []taterUsenetCategory{}
		streamChildren = append(streamChildren,
			taterUsenetCategory{Type: "search", Title: "Search", Detail: "ALL MEDIA"},
			taterUsenetCategory{ID: "watch-again", Type: "watchAgain", Title: "Watch Again", Detail: "RECENT STREAMS"},
		)
		streamChildren = append(streamChildren,
			taterUsenetCategory{Type: "discoverRoot", Title: "Discover", Detail: "GUIDE", Children: taterDiscoverRows()},
			taterUsenetCategory{Type: "trendingRoot", Title: "Trending", Detail: "PROVIDER", Children: taterTrendingRows()},
		)
		streamChildren = append(streamChildren, children...)
		categories = append(categories, taterUsenetCategory{
			ID:       "stream",
			Title:    "Stream",
			Detail:   "NZB",
			Type:     "group",
			IsGroup:  true,
			Children: streamChildren,
			Count:    len(streamChildren),
		})
	}
	if taterLocalMediaEnabled(cfg) {
		categories = append(categories, taterLocalRootRow(cfg))
	}

	return RespondSuccess(c, fiber.Map{
		"categories":    categories,
		"tater_bumpers": taterBumperSettingsMap(cfg),
	})
}

func (s *Server) handleTaterUsenetItems(c *fiber.Ctx) error {
	cfg, playerToken, ok := s.taterUsenetAuthorizedConfig(c)
	if !ok {
		return nil
	}

	categoryID := strings.TrimSpace(c.Query("category_id"))
	if categoryID == "" {
		return RespondValidationError(c, "Category is required", "category_id is empty")
	}
	if strings.HasPrefix(categoryID, "local:") {
		if !taterLocalMediaEnabled(cfg) {
			return RespondServiceUnavailable(c, "Local media is not configured", "")
		}
		sourceIndex := parseTaterInt(c.Query("source"), -1)
		items, err := taterLocalMediaItems(cfg, resolveBaseURL(c, ""), playerToken, strings.TrimPrefix(categoryID, "local:"), sourceIndex, c.Query("path"))
		if err != nil {
			return RespondValidationError(c, "Failed to load local media", err.Error())
		}
		items = taterAttachLocalPlayStates(cfg, items)
		title := strings.TrimSpace(c.Query("title"))
		if title == "" {
			title = "Local"
		}
		return RespondSuccess(c, fiber.Map{"title": title, "items": items})
	}
	if strings.HasPrefix(categoryID, "local-discover:") {
		if !taterLocalMediaEnabled(cfg) {
			return RespondServiceUnavailable(c, "Local media is not configured", "")
		}
		items, err := taterLocalDiscoverItems(cfg, resolveBaseURL(c, ""), playerToken, categoryID)
		if err != nil {
			return RespondValidationError(c, "Failed to load local discovery", err.Error())
		}
		items = taterAttachLocalPlayStates(cfg, items)
		title := strings.TrimSpace(c.Query("title"))
		if title == "" {
			title = "Local Discover"
		}
		return RespondSuccess(c, fiber.Map{"title": title, "items": items})
	}

	if !taterNewznabEnabled(cfg) {
		return RespondServiceUnavailable(c, "Newznab Stream catalog is not configured", "")
	}
	title := strings.TrimSpace(c.Query("title"))
	if title == "" {
		title = "Stream"
	}
	if categoryID == "watch-again" {
		items, err := taterNzbWatchAgainRows(cfg)
		if err != nil {
			return RespondServiceUnavailable(c, "Failed to load Watch Again", err.Error())
		}
		return RespondSuccess(c, fiber.Map{"title": "Watch Again", "items": items})
	}

	body, err := taterFetchNewznab(c.Context(), cfg, map[string]string{
		"t":        "search",
		"cat":      categoryID,
		"extended": "1",
		"limit":    strconv.Itoa(taterBrowseLimit(cfg)),
	})
	if err != nil {
		return RespondServiceUnavailable(c, "Failed to load Newznab items", err.Error())
	}
	items, err := parseTaterNewznabItems(body, cfg)
	if err != nil {
		return RespondServiceUnavailable(c, "Failed to parse Newznab items", err.Error())
	}
	taterAnnotateNzbMediaTypes(items, categoryID, "")
	return RespondSuccess(c, fiber.Map{"title": title, "items": items})
}

func (s *Server) handleTaterUsenetSearch(c *fiber.Ctx) error {
	cfg, _, ok := s.taterUsenetAuthorizedConfig(c)
	if !ok {
		return nil
	}
	if !taterNewznabEnabled(cfg) {
		return RespondServiceUnavailable(c, "Newznab Stream catalog is not configured", "")
	}

	query := cleanTaterText(c.Query("q"))
	if len(query) < 3 {
		return RespondValidationError(c, "Enter at least 3 characters", "")
	}

	body, err := taterFetchNewznab(c.Context(), cfg, map[string]string{
		"t":        "search",
		"q":        query,
		"cat":      "2000,3000,5000",
		"extended": "1",
		"limit":    strconv.Itoa(taterBrowseLimit(cfg)),
	})
	if err != nil {
		return RespondServiceUnavailable(c, "Search failed", err.Error())
	}
	items, err := parseTaterNewznabItems(body, cfg)
	if err != nil {
		return RespondServiceUnavailable(c, "Failed to parse search results", err.Error())
	}
	taterAnnotateNzbMediaTypes(items, "", "")
	return RespondSuccess(c, fiber.Map{
		"title": "Search: " + query,
		"items": items,
	})
}

func (s *Server) handleTaterUsenetDiscover(c *fiber.Ctx) error {
	cfg, _, ok := s.taterUsenetAuthorizedConfig(c)
	if !ok {
		return nil
	}
	if !taterNewznabEnabled(cfg) {
		return RespondServiceUnavailable(c, "Newznab Stream catalog is not configured", "")
	}

	catalog := strings.ToLower(strings.TrimSpace(c.Query("catalog")))
	mediaType, catalogID, genre, title, err := taterDiscoverCatalog(catalog)
	if err != nil {
		return RespondValidationError(c, err.Error(), "")
	}

	body, err := taterFetchURL(c.Context(), cfg, taterCinemetaCatalogURL(mediaType, catalogID, genre))
	if err != nil {
		return RespondServiceUnavailable(c, "Discover load failed", err.Error())
	}
	items, err := parseTaterCinemetaItems(body, mediaType)
	if err != nil {
		return RespondServiceUnavailable(c, "Failed to parse Discover feed", err.Error())
	}

	return RespondSuccess(c, fiber.Map{
		"title": title,
		"items": items,
	})
}

func (s *Server) handleTaterUsenetTrending(c *fiber.Ctx) error {
	cfg, _, ok := s.taterUsenetAuthorizedConfig(c)
	if !ok {
		return nil
	}
	if !taterNewznabEnabled(cfg) {
		return RespondServiceUnavailable(c, "Newznab Stream catalog is not configured", "")
	}
	if !taterIsOMGHost(taterNewznabHost(cfg.Newznab.URL)) {
		return RespondServiceUnavailable(c, "Trending is not available for this provider", "")
	}
	if strings.TrimSpace(cfg.Newznab.Username) == "" {
		return RespondValidationError(c, "Trending username is required", "")
	}

	category := strings.ToLower(strings.TrimSpace(c.Query("category")))
	if category != "movie" && category != "tv" {
		return RespondValidationError(c, "Trending category is invalid", "")
	}
	period := strings.ToLower(strings.TrimSpace(c.Query("period")))
	if period != "today" && period != "week" && period != "month" && period != "year" {
		return RespondValidationError(c, "Trending period is invalid", "")
	}

	body, err := taterFetchURL(c.Context(), cfg, taterOmgTrendingURL(cfg, category, period))
	if err != nil {
		return RespondServiceUnavailable(c, "Trending load failed", err.Error())
	}
	items, err := parseTaterNewznabItems(body, cfg)
	if err != nil {
		return RespondServiceUnavailable(c, "Failed to parse trending feed", err.Error())
	}
	taterAnnotateNzbMediaTypes(items, "", category)
	return RespondSuccess(c, fiber.Map{
		"title": fmt.Sprintf("%s %s", strings.ToUpper(category), strings.ToUpper(period)),
		"items": items,
	})
}

func (s *Server) handleTaterUsenetPlay(c *fiber.Ctx) error {
	ctx := c.Context()
	cfg, playerToken, ok := s.taterUsenetAuthorizedConfig(c)
	if !ok {
		return nil
	}
	if !taterNewznabEnabled(cfg) {
		return RespondServiceUnavailable(c, "Newznab Stream catalog is not configured", "")
	}

	var req taterUsenetPlayRequest
	if err := c.BodyParser(&req); err != nil {
		return RespondValidationError(c, "Invalid play request", err.Error())
	}
	if req.NzbURL == "" {
		req.NzbURL = c.FormValue("nzb_url")
	}
	req.NzbURL = strings.TrimSpace(req.NzbURL)
	req.Title = cleanTaterText(req.Title)
	if req.NzbURL == "" {
		return RespondValidationError(c, "NZB URL is required", "")
	}
	if req.Timeout <= 0 {
		req.Timeout = 300
	}
	if req.Timeout < 60 {
		req.Timeout = 60
	}
	if req.Timeout > 900 {
		req.Timeout = 900
	}

	downloadURL := taterEnsureNewznabKey(req.NzbURL, cfg)
	nzbData, err := taterFetchURL(ctx, cfg, downloadURL)
	if err != nil {
		return RespondServiceUnavailable(c, "NZB download failed", err.Error())
	}
	if errMsg := taterNewznabError(bodyTrimForXML(nzbData)); errMsg != "" {
		return RespondServiceUnavailable(c, errMsg, "")
	}

	watchTitle := req.Title
	safeFilename := stableTaterNzbFilename(req.Title, downloadURL)
	if fileinfo.IsProbablyObfuscated(nzbtrim.TrimNzbExtension(safeFilename)) {
		if derived := deriveNzbNameFromContent(nzbData); derived != "" {
			safeFilename = stableTaterNzbFilename(derived, downloadURL)
			if watchTitle == "" || fileinfo.IsProbablyObfuscated(watchTitle) {
				watchTitle = derived
			}
		}
	}
	nzbName := nzbtrim.TrimNzbExtension(safeFilename)
	if watchTitle == "" {
		watchTitle = nzbName
	}
	baseURL := resolveBaseURL(c, "")
	category := strings.TrimSpace(req.Category)
	if category == "" {
		category = "tater-tube"
	}

	rawID, sfErr, _ := s.stremioPlayGroup.Do(safeFilename, func() (interface{}, error) {
		workCtx := context.WithoutCancel(ctx)
		completedStatus := database.QueueStatusCompleted
		ttlHours := cfg.Stremio.NzbTTLHours

		if existing, e := s.queueRepo.ListQueueItems(workCtx, &completedStatus, safeFilename, "", 1, 0, "updated_at", "desc"); e == nil && len(existing) > 0 {
			prev := existing[0]
			cacheValid := prev.StoragePath != nil && *prev.StoragePath != ""
			if cacheValid && ttlHours > 0 && prev.CompletedAt != nil {
				cacheValid = time.Since(*prev.CompletedAt) < time.Duration(ttlHours)*time.Hour
			}
			if cacheValid {
				return prev.ID, nil
			}
		}

		if activeItems, e := s.queueRepo.ListQueueItems(workCtx, nil, safeFilename, "", 1, 0, "updated_at", "desc"); e == nil && len(activeItems) > 0 {
			it := activeItems[0]
			switch it.Status {
			case database.QueueStatusPending, database.QueueStatusProcessing, database.QueueStatusPaused:
				return it.ID, nil
			}
		}

		if s.importerService == nil {
			return nil, fmt.Errorf("importer service not available")
		}
		uploadDir := filepath.Join(os.TempDir(), "tater-tube-server-uploads")
		if err := os.MkdirAll(uploadDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create upload directory: %w", err)
		}
		stageDir, err := os.MkdirTemp(uploadDir, "tater-play-*")
		if err != nil {
			return nil, fmt.Errorf("failed to create staging directory: %w", err)
		}
		defer os.RemoveAll(stageDir)

		tempPath := filepath.Join(stageDir, safeFilename)
		if err := os.WriteFile(tempPath, nzbData, 0644); err != nil {
			return nil, fmt.Errorf("failed to save NZB file: %w", err)
		}

		var basePath *string
		if completeDir := cfg.SABnzbd.CompleteDir; completeDir != "" {
			basePath = &completeDir
		}
		priority := database.QueuePriorityHigh
		item, err := s.importerService.AddToQueue(workCtx, tempPath, basePath, &category, &priority, nil, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to add NZB to queue: %w", err)
		}

		slog.InfoContext(workCtx, "NZB queued for Tater Tube Stream playback",
			"queue_id", item.ID,
			"nzb_name", nzbName,
			"timeout_secs", req.Timeout)

		return item.ID, nil
	})
	if sfErr != nil {
		return RespondServiceUnavailable(c, "Failed to prepare NZB stream", sfErr.Error())
	}

	itemID, ok := rawID.(int64)
	if !ok {
		return RespondInternalError(c, "Unexpected stream result", "")
	}
	if err := s.waitAndRespondWithStreamAuth(c, itemID, baseURL, "player_token", playerToken, nzbName, nil, req.Timeout); err != nil {
		return err
	}
	if err := taterRecordNzbWatchAgain(cfg, watchTitle, req.NzbURL, req.Category); err != nil {
		slog.WarnContext(ctx, "Failed to record Tater Tube Watch Again item", "error", err)
	}
	return nil
}

func (s *Server) handleTaterMusicLibraries(c *fiber.Ctx) error {
	cfg, _, ok := s.taterUsenetAuthorizedConfig(c)
	if !ok {
		return nil
	}
	return RespondSuccess(c, fiber.Map{
		"libraries": taterLocalMusicLibraries(cfg),
	})
}

func (s *Server) handleTaterMusicAlbums(c *fiber.Ctx) error {
	cfg, playerToken, ok := s.taterUsenetAuthorizedConfig(c)
	if !ok {
		return nil
	}
	categoryID := strings.TrimSpace(c.Query("category_id"))
	if categoryID == "" {
		return RespondValidationError(c, "Music library is required", "category_id is empty")
	}
	albums, err := taterLocalMusicAlbums(cfg, resolveBaseURL(c, ""), playerToken, categoryID)
	if err != nil {
		return RespondValidationError(c, "Failed to load music albums", err.Error())
	}
	return RespondSuccess(c, fiber.Map{
		"albums": albums,
	})
}

func (s *Server) handleTaterMusicTracks(c *fiber.Ctx) error {
	cfg, playerToken, ok := s.taterUsenetAuthorizedConfig(c)
	if !ok {
		return nil
	}
	albumID := strings.TrimSpace(c.Query("album_id"))
	if albumID == "" {
		return RespondValidationError(c, "Music album is required", "album_id is empty")
	}
	tracks, err := taterLocalMusicTracks(cfg, resolveBaseURL(c, ""), playerToken, albumID)
	if err != nil {
		return RespondValidationError(c, "Failed to load music tracks", err.Error())
	}
	return RespondSuccess(c, fiber.Map{
		"tracks": tracks,
	})
}

func (s *Server) taterUsenetAuthorizedConfig(c *fiber.Ctx) (*config.Config, string, bool) {
	return s.taterAuthorizedConfig(c)
}

func taterNewznabEnabled(cfg *config.Config) bool {
	return cfg != nil &&
		cfg.Newznab.Enabled != nil &&
		*cfg.Newznab.Enabled &&
		strings.TrimSpace(cfg.Newznab.URL) != "" &&
		strings.TrimSpace(cleanTaterAPIKey(cfg.Newznab.APIKey)) != ""
}

func taterLocalMediaEnabled(cfg *config.Config) bool {
	if cfg == nil || cfg.LocalMedia.Enabled == nil || !*cfg.LocalMedia.Enabled {
		return false
	}
	for _, cat := range cfg.LocalMedia.Categories {
		if cat.Enabled != nil && !*cat.Enabled {
			continue
		}
		if strings.TrimSpace(cat.ID) != "" && strings.TrimSpace(cat.Name) != "" && len(cat.Paths) > 0 {
			return true
		}
	}
	return false
}

func taterTubeTVEnabled(cfg *config.Config) bool {
	return cfg != nil &&
		(cfg.TubeTV.Enabled == nil || *cfg.TubeTV.Enabled) &&
		taterLocalMediaEnabled(cfg)
}

func taterLocalRootRow(cfg *config.Config) taterUsenetCategory {
	children := []taterUsenetCategory{{
		Type:      "continue",
		Title:     "Continue Watching",
		Detail:    "LOCAL",
		Group:     "Local",
		FullTitle: "Local / Continue Watching",
	}}
	if discoverRows := taterLocalDiscoverRows(cfg); len(discoverRows) > 0 {
		children = append(children, taterUsenetCategory{
			Type:      "localDiscoverRoot",
			Title:     "Discover",
			Detail:    "LOCAL",
			Group:     "Local",
			FullTitle: "Local / Discover",
			IsGroup:   true,
			Count:     len(discoverRows),
			Children:  discoverRows,
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
		name := cleanTaterText(cat.Name)
		if id == "" || name == "" || len(cat.Paths) == 0 {
			continue
		}
		children = append(children, taterUsenetCategory{
			ID:        "local:" + id,
			Type:      "local",
			Title:     name,
			Detail:    "LOCAL",
			Group:     "Local",
			FullTitle: "Local / " + name,
			Category:  id,
		})
	}
	return taterUsenetCategory{
		Type:     "localRoot",
		Title:    "Local",
		Detail:   "SERVER",
		Group:    "Local",
		IsGroup:  true,
		Count:    len(children),
		Children: children,
	}
}

func taterLocalMediaItems(cfg *config.Config, baseURL, playerToken, categoryID string, sourceIndex int, relPath string) ([]taterUsenetItem, error) {
	cat, ok := taterLocalMediaCategory(cfg, categoryID)
	if !ok {
		return nil, fmt.Errorf("local media category not found")
	}
	paths := taterLocalMediaCategoryPaths(cat)
	if len(paths) == 0 {
		return nil, fmt.Errorf("local media category has no folders")
	}
	switch strings.ToLower(strings.TrimSpace(cat.LibraryType)) {
	case "tv":
		return taterLocalTVItems(cfg, cat, paths, baseURL, playerToken, sourceIndex, relPath)
	case "music":
		return taterLocalMusicAlbums(cfg, baseURL, playerToken, cat.ID)
	case "folders":
		return taterLocalFolderItems(cfg, cat, paths, baseURL, playerToken, sourceIndex, relPath)
	default:
		return taterLocalMovieItems(cfg, cat, paths, baseURL, playerToken)
	}
}

type taterLocalDiscoverDefinition struct {
	ID     string
	Title  string
	Detail string
}

type taterLocalDiscoverGenre struct {
	ID       string
	Title    string
	Keywords []string
}

type taterLocalNFO struct {
	Title     string   `xml:"title"`
	Plot      string   `xml:"plot"`
	Outline   string   `xml:"outline"`
	Year      string   `xml:"year"`
	Premiered string   `xml:"premiered"`
	Genres    []string `xml:"genre"`
}

func taterLocalDiscoverDefinitions() []taterLocalDiscoverDefinition {
	rows := []taterLocalDiscoverDefinition{
		{ID: "local-discover:recent", Title: "Recently Added", Detail: "LOCAL"},
		{ID: "local-discover:movies", Title: "Movies", Detail: "LOCAL"},
		{ID: "local-discover:series", Title: "Series", Detail: "LOCAL"},
	}
	for _, genre := range taterLocalDiscoverGenres() {
		rows = append(rows, taterLocalDiscoverDefinition{
			ID:     "local-discover:genre:" + genre.ID,
			Title:  genre.Title,
			Detail: "GENRE",
		})
	}
	for _, decade := range []int{1930, 1940, 1950, 1960, 1970, 1980, 1990, 2000, 2010, 2020} {
		rows = append(rows, taterLocalDiscoverDefinition{
			ID:     fmt.Sprintf("local-discover:decade:%d", decade),
			Title:  fmt.Sprintf("%ds", decade),
			Detail: "DECADE",
		})
	}
	return rows
}

func taterLocalDiscoverGenres() []taterLocalDiscoverGenre {
	return []taterLocalDiscoverGenre{
		{ID: "animation", Title: "Animation & Cartoons", Keywords: []string{"animation", "animated", "cartoon", "cartoons", "looney", "tom and jerry", "disney", "pixar"}},
		{ID: "action", Title: "Action", Keywords: []string{"action", "mission", "martial", "kung fu", "explosion", "commando", "rampage"}},
		{ID: "comedy", Title: "Comedy", Keywords: []string{"comedy", "stand up", "standup", "funny", "sitcom"}},
		{ID: "horror", Title: "Horror", Keywords: []string{"horror", "haunting", "ghost", "zombie", "vampire", "frankenstein", "dracula", "scream", "slasher", "terror", "evil dead", "halloween"}},
		{ID: "scifi", Title: "Sci-Fi", Keywords: []string{"sci fi", "sci-fi", "scifi", "science fiction", "space", "alien", "star trek", "star wars"}},
		{ID: "crime", Title: "Crime", Keywords: []string{"crime", "detective", "murder", "mafia", "gangster", "police"}},
		{ID: "thriller", Title: "Thriller", Keywords: []string{"thriller", "suspense", "mystery"}},
		{ID: "documentary", Title: "Documentary", Keywords: []string{"documentary", "docu", "history", "nature"}},
		{ID: "family", Title: "Family", Keywords: []string{"family", "kids", "children", "holiday special"}},
		{ID: "fantasy", Title: "Fantasy", Keywords: []string{"fantasy", "magic", "dragon", "wizard"}},
		{ID: "holiday", Title: "Holiday", Keywords: []string{"christmas", "xmas", "halloween", "thanksgiving", "holiday"}},
		{ID: "drama", Title: "Drama", Keywords: []string{"drama"}},
	}
}

func taterLocalDiscoverRows(cfg *config.Config) []taterUsenetCategory {
	items, err := taterLocalDiscoverLibraryItems(cfg, "", "")
	if err != nil || len(items) == 0 {
		return []taterUsenetCategory{}
	}
	rows := []taterUsenetCategory{}
	for _, def := range taterLocalDiscoverDefinitions() {
		count := len(taterFilterLocalDiscoverItems(items, def.ID))
		if count == 0 {
			continue
		}
		rows = append(rows, taterUsenetCategory{
			ID:        def.ID,
			Type:      "localDiscover",
			Title:     def.Title,
			Detail:    def.Detail,
			Group:     "Local",
			FullTitle: "Local / Discover / " + def.Title,
			Count:     count,
		})
	}
	return rows
}

func taterLocalDiscoverItems(cfg *config.Config, baseURL, playerToken, discoverID string) ([]taterUsenetItem, error) {
	items, err := taterLocalDiscoverLibraryItems(cfg, baseURL, playerToken)
	if err != nil {
		return nil, err
	}
	rows := taterFilterLocalDiscoverItems(items, discoverID)
	if len(rows) > 200 {
		rows = rows[:200]
	}
	return rows, nil
}

func taterLocalDiscoverLibraryItems(cfg *config.Config, baseURL, playerToken string) ([]taterUsenetItem, error) {
	if !taterLocalMediaEnabled(cfg) {
		return []taterUsenetItem{}, nil
	}
	rows := []taterUsenetItem{}
	for _, cat := range cfg.LocalMedia.Categories {
		if cat.Enabled != nil && !*cat.Enabled {
			continue
		}
		id := strings.TrimSpace(cat.ID)
		if id == "" || strings.ToLower(strings.TrimSpace(cat.LibraryType)) == "music" {
			continue
		}
		paths := taterLocalMediaCategoryPaths(cat)
		if len(paths) == 0 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(cat.LibraryType)) {
		case "tv":
			shows, err := taterLocalTVItems(cfg, cat, paths, baseURL, playerToken, -1, "")
			if err != nil {
				continue
			}
			for i := range shows {
				taterApplyLocalDiscoverInfo(&shows[i], paths)
			}
			rows = append(rows, shows...)
		case "folders":
			videos, err := taterLocalDiscoverVideoItems(cfg, cat, paths, baseURL, playerToken, "video")
			if err != nil {
				continue
			}
			rows = append(rows, videos...)
		default:
			movies, err := taterLocalDiscoverVideoItems(cfg, cat, paths, baseURL, playerToken, "movie")
			if err != nil {
				continue
			}
			rows = append(rows, movies...)
		}
	}
	return rows, nil
}

func taterLocalDiscoverVideoItems(cfg *config.Config, cat config.LocalMediaCategory, paths []string, baseURL, playerToken, mediaType string) ([]taterUsenetItem, error) {
	items := []taterUsenetItem{}
	for sourceIndex, root := range paths {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				if entry.IsDir() && path != root {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() || !isMediaExtension(filepath.Ext(name)) {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			titleSource := movieTitleSource(root, rel)
			title, year := cleanMovieTitleAndYear(titleSource)
			if title == "" {
				title = cleanMovieTitleFromName(strings.TrimSuffix(name, filepath.Ext(name)))
			}
			item := taterUsenetItem{
				Title:       title,
				Type:        "localFile",
				MediaType:   mediaType,
				CategoryID:  "local:" + cat.ID,
				SourceIndex: sourceIndex,
				Path:        rel,
				StreamURL:   taterLocalStreamURL(baseURL, cat.ID, sourceIndex, rel, playerToken),
				SeekMode:    taterLocalSeekMode(cfg, filepath.Ext(name)),
				Date:        year,
				SizeText:    localMovieDetail(year),
			}
			if info, statErr := entry.Info(); statErr == nil && info != nil {
				item.SizeBytes = info.Size()
				item.Index = int(info.ModTime().Unix())
			}
			if mediaType == "video" {
				item.SizeText = "VIDEO"
			}
			taterApplyLocalMetadata(path, &item)
			if item.Category == "" {
				if genre := taterLocalDiscoverGenreLabel(item); genre != "" {
					item.Category = genre
				}
			}
			items = append(items, item)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
	return items, nil
}

func taterApplyLocalMetadata(mediaPath string, item *taterUsenetItem) {
	if item == nil {
		return
	}
	dir := mediaPath
	base := ""
	if filepath.Ext(mediaPath) != "" {
		dir = filepath.Dir(mediaPath)
		base = strings.TrimSuffix(filepath.Base(mediaPath), filepath.Ext(mediaPath))
	}
	meta, ok := taterReadLocalMetadata(dir, base)
	if !ok {
		return
	}
	if title := cleanTaterText(meta.Title); title != "" {
		item.Title = title
	}
	if description := cleanTaterText(meta.Plot); description != "" {
		item.Description = description
	} else if description := cleanTaterText(meta.Outline); description != "" {
		item.Description = description
	}
	if year := taterLocalMetadataYear(meta); year != "" {
		item.Date = year
		if item.MediaType == "movie" {
			item.SizeText = localMovieDetail(year)
		}
	}
	if genres := taterLocalMetadataGenres(meta); len(genres) > 0 {
		item.Category = strings.Join(genres, ", ")
	}
}

func taterReadLocalMetadata(dir, base string) (taterLocalNFO, bool) {
	candidates := []string{}
	if base != "" {
		candidates = append(candidates, filepath.Join(dir, base+".nfo"))
	}
	candidates = append(candidates,
		filepath.Join(dir, "movie.nfo"),
		filepath.Join(dir, "tvshow.nfo"),
		filepath.Join(dir, "series.nfo"),
	)
	if matches, err := filepath.Glob(filepath.Join(dir, "*.nfo")); err == nil {
		sort.Strings(matches)
		candidates = append(candidates, matches...)
	}

	seen := map[string]bool{}
	for _, candidate := range candidates {
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		data, err := os.ReadFile(candidate)
		if err != nil || len(data) == 0 {
			continue
		}
		var meta taterLocalNFO
		if err := xml.Unmarshal(data, &meta); err != nil {
			continue
		}
		if cleanTaterText(meta.Title) == "" &&
			cleanTaterText(meta.Plot) == "" &&
			cleanTaterText(meta.Outline) == "" &&
			taterLocalMetadataYear(meta) == "" &&
			len(taterLocalMetadataGenres(meta)) == 0 {
			continue
		}
		return meta, true
	}
	return taterLocalNFO{}, false
}

func taterLocalMetadataYear(meta taterLocalNFO) string {
	for _, value := range []string{meta.Year, meta.Premiered} {
		if match := localYearPattern.FindStringSubmatch(value); len(match) > 1 {
			return match[1]
		}
	}
	return ""
}

func taterLocalMetadataGenres(meta taterLocalNFO) []string {
	genres := []string{}
	seen := map[string]bool{}
	for _, raw := range meta.Genres {
		for _, part := range strings.Split(raw, "/") {
			for _, value := range strings.Split(part, ",") {
				genre := cleanTaterText(value)
				key := strings.ToLower(genre)
				if genre == "" || seen[key] {
					continue
				}
				seen[key] = true
				genres = append(genres, genre)
			}
		}
	}
	return genres
}

func taterApplyLocalDiscoverInfo(item *taterUsenetItem, roots []string) {
	if item == nil {
		return
	}
	if item.SourceIndex >= 0 && item.SourceIndex < len(roots) && item.Path != "" {
		absPath := filepath.Join(roots[item.SourceIndex], filepath.FromSlash(item.Path))
		if info, err := os.Stat(absPath); err == nil && info != nil {
			item.Index = int(info.ModTime().Unix())
		}
		taterApplyLocalMetadata(absPath, item)
	}
	year := taterLocalDiscoverYear(*item)
	if item.Date == "" && year > 0 {
		item.Date = strconv.Itoa(year)
	}
	if item.Category == "" {
		if genre := taterLocalDiscoverGenreLabel(*item); genre != "" {
			item.Category = genre
		}
	}
}

func taterFilterLocalDiscoverItems(items []taterUsenetItem, discoverID string) []taterUsenetItem {
	key := strings.TrimPrefix(strings.TrimSpace(discoverID), "local-discover:")
	rows := []taterUsenetItem{}
	for _, item := range items {
		if taterLocalDiscoverMatches(item, key) {
			rows = append(rows, item)
		}
	}
	taterSortLocalDiscoverItems(rows, key)
	return rows
}

func taterLocalDiscoverMatches(item taterUsenetItem, key string) bool {
	switch {
	case key == "recent":
		return item.Type == "localFile" || item.MediaType == "show"
	case key == "movies":
		return item.MediaType == "movie"
	case key == "series":
		return item.MediaType == "show"
	case strings.HasPrefix(key, "genre:"):
		return taterLocalDiscoverMatchesGenre(item, strings.TrimPrefix(key, "genre:"))
	case strings.HasPrefix(key, "decade:"):
		decade, err := strconv.Atoi(strings.TrimPrefix(key, "decade:"))
		if err != nil {
			return false
		}
		year := taterLocalDiscoverYear(item)
		return year >= decade && year < decade+10
	default:
		return false
	}
}

func taterSortLocalDiscoverItems(items []taterUsenetItem, key string) {
	if key == "recent" {
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Index != items[j].Index {
				return items[i].Index > items[j].Index
			}
			return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
		})
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		leftYear := taterLocalDiscoverYear(items[i])
		rightYear := taterLocalDiscoverYear(items[j])
		if leftYear != rightYear && strings.HasPrefix(key, "decade:") {
			return leftYear < rightYear
		}
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
}

func taterLocalDiscoverMatchesGenre(item taterUsenetItem, genreID string) bool {
	for _, genre := range taterLocalDiscoverGenres() {
		if genre.ID != genreID {
			continue
		}
		text := taterLocalDiscoverText(item)
		for _, keyword := range genre.Keywords {
			if strings.Contains(text, taterLocalDiscoverNormalize(keyword)) {
				return true
			}
		}
		return false
	}
	return false
}

func taterLocalDiscoverGenreLabel(item taterUsenetItem) string {
	labels := []string{}
	for _, genre := range taterLocalDiscoverGenres() {
		if taterLocalDiscoverMatchesGenre(item, genre.ID) {
			labels = append(labels, genre.Title)
		}
	}
	return strings.Join(labels, ", ")
}

func taterLocalDiscoverText(item taterUsenetItem) string {
	return taterLocalDiscoverNormalize(strings.Join([]string{
		item.Title,
		item.Path,
		item.Category,
		item.Description,
	}, " "))
}

func taterLocalDiscoverNormalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", " ")
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, ".", " ")
	value = localSeparatorPattern.ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(value), " ")
}

func taterLocalDiscoverYear(item taterUsenetItem) int {
	for _, value := range []string{item.Date, item.Title, item.Path} {
		if match := localYearPattern.FindStringSubmatch(value); len(match) > 1 {
			year, _ := strconv.Atoi(match[1])
			if year > 0 {
				return year
			}
		}
	}
	return 0
}

func taterLocalFolderItems(cfg *config.Config, cat config.LocalMediaCategory, paths []string, baseURL, playerToken string, sourceIndex int, relPath string) ([]taterUsenetItem, error) {
	if sourceIndex < 0 && strings.TrimSpace(relPath) == "" && len(paths) > 1 {
		items := make([]taterUsenetItem, 0, len(paths))
		for i, root := range paths {
			title := cleanTaterText(filepath.Base(root))
			if title == "." || title == string(filepath.Separator) || title == "" {
				title = fmt.Sprintf("Folder %d", i+1)
			}
			items = append(items, taterUsenetItem{
				Title:       title,
				Type:        "localFolder",
				MediaType:   "folder",
				CategoryID:  "local:" + cat.ID,
				SourceIndex: i,
				Path:        "",
				SizeText:    "FOLDER",
			})
		}
		return items, nil
	}

	if sourceIndex < 0 {
		sourceIndex = 0
	}
	if sourceIndex >= len(paths) {
		return nil, fmt.Errorf("local media source not found")
	}
	root := paths[sourceIndex]
	cleanRel := cleanLocalRelativePath(relPath)
	dirPath, err := safeLocalPath(root, cleanRel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	items := make([]taterUsenetItem, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		childRel := filepath.ToSlash(filepath.Join(cleanRel, name))
		if entry.IsDir() {
			items = append(items, taterUsenetItem{
				Title:       cleanLocalTitle(name),
				Type:        "localFolder",
				MediaType:   "folder",
				CategoryID:  "local:" + cat.ID,
				SourceIndex: sourceIndex,
				Path:        childRel,
				SizeText:    "FOLDER",
			})
			continue
		}
		if !isMediaExtension(filepath.Ext(name)) {
			continue
		}
		info, _ := entry.Info()
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		item := taterUsenetItem{
			Title:       cleanMovieTitleFromName(strings.TrimSuffix(name, filepath.Ext(name))),
			Type:        "localFile",
			MediaType:   "video",
			CategoryID:  "local:" + cat.ID,
			SourceIndex: sourceIndex,
			Path:        childRel,
			StreamURL:   taterLocalStreamURL(baseURL, cat.ID, sourceIndex, childRel, playerToken),
			SeekMode:    taterLocalSeekMode(cfg, filepath.Ext(name)),
			SizeBytes:   size,
		}
		if size > 0 {
			item.SizeText = formatTaterBytes(size)
		}
		attachTaterLocalDuration(cfg, filepath.Join(dirPath, name), &item)
		items = append(items, item)
	}
	return items, nil
}

func taterLocalMovieItems(cfg *config.Config, cat config.LocalMediaCategory, paths []string, baseURL, playerToken string) ([]taterUsenetItem, error) {
	items := []taterUsenetItem{}
	for sourceIndex, root := range paths {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				if entry.IsDir() && path != root {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			if !isMediaExtension(filepath.Ext(name)) {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			info, _ := entry.Info()
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			titleSource := movieTitleSource(root, rel)
			title, year := cleanMovieTitleAndYear(titleSource)
			if title == "" {
				title = cleanMovieTitleFromName(strings.TrimSuffix(name, filepath.Ext(name)))
			}
			item := taterUsenetItem{
				Title:       title,
				Type:        "localFile",
				MediaType:   "movie",
				CategoryID:  "local:" + cat.ID,
				SourceIndex: sourceIndex,
				Path:        rel,
				StreamURL:   taterLocalStreamURL(baseURL, cat.ID, sourceIndex, rel, playerToken),
				SeekMode:    taterLocalSeekMode(cfg, filepath.Ext(name)),
				Date:        year,
				SizeBytes:   size,
				SizeText:    localMovieDetail(year),
			}
			attachTaterLocalDuration(cfg, path, &item)
			items = append(items, item)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
	return items, nil
}

func taterLocalTVItems(cfg *config.Config, cat config.LocalMediaCategory, paths []string, baseURL, playerToken string, sourceIndex int, relPath string) ([]taterUsenetItem, error) {
	if sourceIndex < 0 && strings.TrimSpace(relPath) == "" {
		items := []taterUsenetItem{}
		for i, root := range paths {
			entries, err := os.ReadDir(root)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				items = append(items, taterUsenetItem{
					Title:       cleanShowTitle(entry.Name()),
					Type:        "localFolder",
					MediaType:   "show",
					CategoryID:  "local:" + cat.ID,
					SourceIndex: i,
					Path:        filepath.ToSlash(entry.Name()),
					SizeText:    "SHOW",
				})
			}
		}
		sort.SliceStable(items, func(i, j int) bool {
			return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
		})
		return items, nil
	}
	if sourceIndex < 0 {
		sourceIndex = 0
	}
	if sourceIndex >= len(paths) {
		return nil, fmt.Errorf("local media source not found")
	}
	root := paths[sourceIndex]
	cleanRel := cleanLocalRelativePath(relPath)
	dirPath, err := safeLocalPath(root, cleanRel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	items := []taterUsenetItem{}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		childRel := filepath.ToSlash(filepath.Join(cleanRel, name))
		if entry.IsDir() {
			items = append(items, taterUsenetItem{
				Title:       cleanSeasonTitle(name),
				Type:        "localFolder",
				MediaType:   "season",
				CategoryID:  "local:" + cat.ID,
				SourceIndex: sourceIndex,
				Path:        childRel,
				SizeText:    "SEASON",
			})
			continue
		}
		if !isMediaExtension(filepath.Ext(name)) {
			continue
		}
		info, _ := entry.Info()
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		item := taterUsenetItem{
			Title:       cleanEpisodeTitle(strings.TrimSuffix(name, filepath.Ext(name))),
			Type:        "localFile",
			MediaType:   "episode",
			CategoryID:  "local:" + cat.ID,
			SourceIndex: sourceIndex,
			Path:        childRel,
			StreamURL:   taterLocalStreamURL(baseURL, cat.ID, sourceIndex, childRel, playerToken),
			SeekMode:    taterLocalSeekMode(cfg, filepath.Ext(name)),
			SizeBytes:   size,
			SizeText:    "EPISODE",
		}
		attachTaterLocalDuration(cfg, filepath.Join(dirPath, name), &item)
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
	return items, nil
}

func taterLocalMusicLibraries(cfg *config.Config) []taterUsenetItem {
	if cfg == nil || cfg.LocalMedia.Enabled == nil || !*cfg.LocalMedia.Enabled {
		return []taterUsenetItem{}
	}

	items := []taterUsenetItem{}
	for _, cat := range cfg.LocalMedia.Categories {
		if cat.Enabled != nil && !*cat.Enabled {
			continue
		}
		if strings.ToLower(strings.TrimSpace(cat.LibraryType)) != "music" {
			continue
		}
		id := strings.TrimSpace(cat.ID)
		name := cleanTaterText(cat.Name)
		if id == "" || name == "" || len(taterLocalMediaCategoryPaths(cat)) == 0 {
			continue
		}
		items = append(items, taterUsenetItem{
			Title:      name,
			Key:        id,
			RatingKey:  id,
			Type:       "musicLibrary",
			MediaType:  "music",
			CategoryID: "local:" + id,
			SizeText:   "MUSIC",
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
	return items
}

type taterMusicAlbumScan struct {
	ID          string
	CategoryID  string
	SourceIndex int
	RelPath     string
	Title       string
	Artist      string
	LeafCount   int
	SizeBytes   int64
}

func taterLocalMusicAlbums(cfg *config.Config, baseURL, playerToken, categoryID string) ([]taterUsenetItem, error) {
	cat, ok := taterLocalMediaCategory(cfg, categoryID)
	if !ok {
		return nil, fmt.Errorf("music category not found")
	}
	if strings.ToLower(strings.TrimSpace(cat.LibraryType)) != "music" {
		return nil, fmt.Errorf("local media category is not music")
	}
	paths := taterLocalMediaCategoryPaths(cat)
	if len(paths) == 0 {
		return nil, fmt.Errorf("music category has no folders")
	}

	albums := map[string]*taterMusicAlbumScan{}
	for sourceIndex, root := range paths {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				if entry.IsDir() && path != root {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() || !isAudioExtension(filepath.Ext(name)) {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			albumRel := cleanLocalRelativePath(filepath.ToSlash(filepath.Dir(rel)))
			id := taterMusicAlbumID(cat.ID, sourceIndex, albumRel)
			album, ok := albums[id]
			if !ok {
				title, artist := localMusicAlbumTitle(cat.Name, albumRel)
				album = &taterMusicAlbumScan{
					ID:          id,
					CategoryID:  cat.ID,
					SourceIndex: sourceIndex,
					RelPath:     albumRel,
					Title:       title,
					Artist:      artist,
				}
				albums[id] = album
			}
			if info, statErr := entry.Info(); statErr == nil && info != nil {
				album.SizeBytes += info.Size()
			}
			album.LeafCount++
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	items := make([]taterUsenetItem, 0, len(albums))
	for _, album := range albums {
		items = append(items, taterUsenetItem{
			Title:       album.Title,
			Key:         album.ID,
			RatingKey:   album.ID,
			Type:        "album",
			MediaType:   "album",
			Artist:      album.Artist,
			CategoryID:  "local:" + album.CategoryID,
			SourceIndex: album.SourceIndex,
			Path:        album.RelPath,
			LeafCount:   album.LeafCount,
			SizeBytes:   album.SizeBytes,
			SizeText:    musicAlbumDetail(album.LeafCount),
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		left := strings.ToLower(items[i].Artist + " " + items[i].Title)
		right := strings.ToLower(items[j].Artist + " " + items[j].Title)
		return left < right
	})
	return items, nil
}

func taterLocalMusicTracks(cfg *config.Config, baseURL, playerToken, albumID string) ([]taterUsenetItem, error) {
	categoryID, sourceIndex, albumRel, ok := parseTaterMusicAlbumID(albumID)
	if !ok {
		return nil, fmt.Errorf("music album id is invalid")
	}
	cat, ok := taterLocalMediaCategory(cfg, categoryID)
	if !ok {
		return nil, fmt.Errorf("music category not found")
	}
	if strings.ToLower(strings.TrimSpace(cat.LibraryType)) != "music" {
		return nil, fmt.Errorf("local media category is not music")
	}
	paths := taterLocalMediaCategoryPaths(cat)
	if sourceIndex < 0 || sourceIndex >= len(paths) {
		return nil, fmt.Errorf("music source not found")
	}

	root := paths[sourceIndex]
	albumPath, err := safeLocalPath(root, albumRel)
	if err != nil {
		return nil, err
	}
	albumTitle, artist := localMusicAlbumTitle(cat.Name, albumRel)
	tracks := []taterUsenetItem{}
	err = filepath.WalkDir(albumPath, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			if entry.IsDir() && path != albumPath {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !isAudioExtension(filepath.Ext(name)) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, _ := entry.Info()
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		index, title := cleanTrackTitleAndIndex(strings.TrimSuffix(name, filepath.Ext(name)))
		itemID := taterMusicTrackID(cat.ID, sourceIndex, rel)
		item := taterUsenetItem{
			Title:       title,
			Key:         itemID,
			RatingKey:   itemID,
			PartKey:     rel,
			Type:        "track",
			MediaType:   "audio",
			Artist:      artist,
			Album:       albumTitle,
			CategoryID:  "local:" + cat.ID,
			SourceIndex: sourceIndex,
			Path:        rel,
			StreamURL:   taterLocalStreamURL(baseURL, cat.ID, sourceIndex, rel, playerToken),
			Index:       index,
			SizeBytes:   size,
		}
		if size > 0 {
			item.SizeText = formatTaterBytes(size)
		}
		attachTaterLocalDuration(cfg, path, &item)
		tracks = append(tracks, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(tracks, func(i, j int) bool {
		if tracks[i].Index != tracks[j].Index {
			if tracks[i].Index == 0 {
				return false
			}
			if tracks[j].Index == 0 {
				return true
			}
			return tracks[i].Index < tracks[j].Index
		}
		return strings.ToLower(tracks[i].Title) < strings.ToLower(tracks[j].Title)
	})
	return tracks, nil
}

func taterLocalSeekMode(cfg *config.Config, ext string) string {
	return "client"
}

type taterLocalDurationCacheEntry struct {
	Size            int64
	ModTimeUnixNano int64
	DurationSeconds float64
}

var taterLocalDurationCache = struct {
	sync.Mutex
	Items map[string]taterLocalDurationCacheEntry
}{
	Items: map[string]taterLocalDurationCacheEntry{},
}

func attachTaterLocalDuration(cfg *config.Config, path string, item *taterUsenetItem) {
	if item == nil {
		return
	}
	attachTaterDuration(item, taterLocalDurationSeconds(cfg, path))
}

func attachTaterDuration(item *taterUsenetItem, durationSeconds float64) {
	if item == nil || durationSeconds <= 0 || math.IsNaN(durationSeconds) || math.IsInf(durationSeconds, 0) {
		return
	}
	item.DurationSeconds = math.Round(durationSeconds*1000) / 1000
	item.Duration = int64(math.Round(durationSeconds))
	item.DurationDisplay = formatTaterDuration(durationSeconds)
}

func taterLocalDurationSeconds(cfg *config.Config, path string) float64 {
	path = strings.TrimSpace(path)
	if path == "" {
		return 0
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = filepath.Clean(path)
	}
	info, err := os.Stat(absPath)
	if err != nil || info == nil || info.IsDir() {
		return 0
	}

	cacheKey := absPath
	size := info.Size()
	modTime := info.ModTime().UnixNano()
	taterLocalDurationCache.Lock()
	if cached, ok := taterLocalDurationCache.Items[cacheKey]; ok &&
		cached.Size == size &&
		cached.ModTimeUnixNano == modTime {
		taterLocalDurationCache.Unlock()
		return cached.DurationSeconds
	}
	taterLocalDurationCache.Unlock()

	ffmpegPath := "ffmpeg"
	if cfg != nil {
		ffmpegPath = effectiveFFmpegPath(cfg.Transcoding.FFmpegPath)
	}
	durationSeconds, probeErr := probeMediaDurationSecondsWithError(context.Background(), ffmpegPath, absPath)
	if probeErr != nil {
		slog.Warn("Unable to read local media duration",
			"path", absPath,
			"ffmpeg_path", ffmpegPath,
			"error", probeErr)
	}

	taterLocalDurationCache.Lock()
	taterLocalDurationCache.Items[cacheKey] = taterLocalDurationCacheEntry{
		Size:            size,
		ModTimeUnixNano: modTime,
		DurationSeconds: durationSeconds,
	}
	taterLocalDurationCache.Unlock()
	return durationSeconds
}

func formatTaterDuration(durationSeconds float64) string {
	if durationSeconds <= 0 || math.IsNaN(durationSeconds) || math.IsInf(durationSeconds, 0) {
		return ""
	}
	total := int64(math.Round(durationSeconds))
	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}

func taterLocalMediaCategory(cfg *config.Config, id string) (config.LocalMediaCategory, bool) {
	id = strings.TrimPrefix(strings.TrimSpace(id), "local:")
	for _, cat := range cfg.LocalMedia.Categories {
		if strings.TrimSpace(cat.ID) != id {
			continue
		}
		if cat.Enabled != nil && !*cat.Enabled {
			return config.LocalMediaCategory{}, false
		}
		return cat, true
	}
	return config.LocalMediaCategory{}, false
}

func taterLocalMediaCategoryPaths(cat config.LocalMediaCategory) []string {
	paths := make([]string, 0, len(cat.Paths))
	for _, raw := range cat.Paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		paths = append(paths, filepath.Clean(path))
	}
	return paths
}

func cleanLocalRelativePath(value string) string {
	value = strings.TrimSpace(filepath.ToSlash(value))
	if value == "" || value == "." || value == "/" {
		return ""
	}
	clean := filepath.ToSlash(filepath.Clean("/" + value))
	return strings.TrimPrefix(clean, "/")
}

var (
	localYearPattern        = regexp.MustCompile(`\b(19[0-9]{2}|20[0-9]{2})\b`)
	localQualityTail        = regexp.MustCompile(`(?i)\b(2160p|1080p|720p|480p|bluray|blu-ray|brrip|webrip|web-dl|webdl|hdtv|x264|x265|h264|h265|hevc|aac|dts|truehd|atmos|proper|repack|extended|remux)\b.*$`)
	localEpisodePattern     = regexp.MustCompile(`(?i)\bS([0-9]{1,2})E([0-9]{1,3})\b`)
	localSeasonPattern      = regexp.MustCompile(`(?i)^season[\s._-]*([0-9]{1,2})$|^s([0-9]{1,2})$`)
	localSeparatorPattern   = regexp.MustCompile(`[._]+`)
	localTrackPrefixPattern = regexp.MustCompile(`^\s*([0-9]{1,3})[\s._-]+`)
)

var localAudioExtensions = map[string]bool{
	".aac":  true,
	".aiff": true,
	".alac": true,
	".flac": true,
	".m4a":  true,
	".m4b":  true,
	".mp3":  true,
	".ogg":  true,
	".opus": true,
	".wav":  true,
	".wma":  true,
}

func isAudioExtension(ext string) bool {
	return localAudioExtensions[strings.ToLower(ext)]
}

func isLocalStreamExtension(ext string) bool {
	return isMediaExtension(ext) || isAudioExtension(ext)
}

func movieTitleSource(root, rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) > 1 {
		parent := parts[len(parts)-2]
		if parent != "" && parent != "." {
			return parent
		}
	}
	return strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
}

func cleanMovieTitleFromName(value string) string {
	title, _ := cleanMovieTitleAndYear(value)
	return title
}

func cleanMovieTitleAndYear(value string) (string, string) {
	value = localSeparatorPattern.ReplaceAllString(value, " ")
	value = strings.ReplaceAll(value, "-", " ")
	year := ""
	if match := localYearPattern.FindStringSubmatch(value); len(match) > 1 {
		year = match[1]
		value = value[:strings.Index(value, match[1])]
	}
	value = localQualityTail.ReplaceAllString(value, "")
	value = strings.TrimRight(value, " \t-_.([{")
	return cleanTaterText(value), year
}

func localMovieDetail(year string) string {
	if strings.TrimSpace(year) == "" {
		return "MOVIE"
	}
	return "MOVIE " + strings.TrimSpace(year)
}

func cleanLocalTitle(value string) string {
	value = localSeparatorPattern.ReplaceAllString(value, " ")
	value = strings.ReplaceAll(value, "-", " ")
	return cleanTaterText(value)
}

func cleanShowTitle(value string) string {
	title, _ := cleanMovieTitleAndYear(value)
	if title == "" {
		title = cleanLocalTitle(value)
	}
	return title
}

func cleanSeasonTitle(value string) string {
	value = cleanTaterText(localSeparatorPattern.ReplaceAllString(value, " "))
	if match := localSeasonPattern.FindStringSubmatch(value); len(match) > 0 {
		season := match[1]
		if season == "" && len(match) > 2 {
			season = match[2]
		}
		if n, err := strconv.Atoi(season); err == nil {
			return fmt.Sprintf("Season %d", n)
		}
	}
	return cleanLocalTitle(value)
}

func cleanEpisodeTitle(value string) string {
	clean := localSeparatorPattern.ReplaceAllString(value, " ")
	clean = strings.ReplaceAll(clean, "-", " ")
	if match := localEpisodePattern.FindStringSubmatch(clean); len(match) >= 3 {
		season, _ := strconv.Atoi(match[1])
		episode, _ := strconv.Atoi(match[2])
		idx := strings.Index(strings.ToLower(clean), strings.ToLower(match[0]))
		tail := ""
		if idx >= 0 {
			tail = cleanTaterText(clean[idx+len(match[0]):])
			tail = localQualityTail.ReplaceAllString(tail, "")
			tail = cleanTaterText(tail)
		}
		prefix := fmt.Sprintf("S%02dE%02d", season, episode)
		if tail != "" {
			return prefix + " " + tail
		}
		return prefix
	}
	title, _ := cleanMovieTitleAndYear(value)
	if title == "" {
		title = cleanLocalTitle(value)
	}
	return title
}

func localMusicAlbumTitle(categoryName, relPath string) (string, string) {
	cleanRel := cleanLocalRelativePath(relPath)
	if cleanRel == "" {
		name := cleanTaterText(categoryName)
		if name == "" {
			name = "Music"
		}
		return name, "UNKNOWN ARTIST"
	}
	parts := strings.Split(cleanRel, "/")
	album := cleanLocalTitle(parts[len(parts)-1])
	artist := "UNKNOWN ARTIST"
	if len(parts) > 1 {
		artist = cleanLocalTitle(parts[len(parts)-2])
	}
	if album == "" {
		album = cleanTaterText(categoryName)
	}
	return album, artist
}

func cleanTrackTitleAndIndex(value string) (int, string) {
	clean := localSeparatorPattern.ReplaceAllString(value, " ")
	clean = strings.ReplaceAll(clean, "-", " ")
	index := 0
	if match := localTrackPrefixPattern.FindStringSubmatch(clean); len(match) > 1 {
		if n, err := strconv.Atoi(match[1]); err == nil {
			index = n
		}
		clean = strings.TrimSpace(clean[len(match[0]):])
	}
	title := cleanTaterText(clean)
	if title == "" {
		title = cleanTaterText(value)
	}
	return index, title
}

func musicAlbumDetail(trackCount int) string {
	if trackCount == 1 {
		return "1 TRACK"
	}
	if trackCount > 1 {
		return fmt.Sprintf("%d TRACKS", trackCount)
	}
	return "ALBUM"
}

func taterMusicAlbumID(categoryID string, sourceIndex int, relPath string) string {
	return "music:" +
		base64.RawURLEncoding.EncodeToString([]byte(strings.TrimSpace(categoryID))) +
		":" + strconv.Itoa(sourceIndex) +
		":" + base64.RawURLEncoding.EncodeToString([]byte(cleanLocalRelativePath(relPath)))
}

func parseTaterMusicAlbumID(value string) (string, int, string, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 4 || parts[0] != "music" {
		return "", -1, "", false
	}
	categoryBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", -1, "", false
	}
	sourceIndex, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", -1, "", false
	}
	relBytes, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return "", -1, "", false
	}
	return string(categoryBytes), sourceIndex, cleanLocalRelativePath(string(relBytes)), true
}

func taterMusicTrackID(categoryID string, sourceIndex int, relPath string) string {
	return "track:" +
		base64.RawURLEncoding.EncodeToString([]byte(strings.TrimSpace(categoryID))) +
		":" + strconv.Itoa(sourceIndex) +
		":" + base64.RawURLEncoding.EncodeToString([]byte(cleanLocalRelativePath(relPath)))
}

func safeLocalPath(root, relPath string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(filepath.Join(rootAbs, filepath.FromSlash(cleanLocalRelativePath(relPath))))
	if err != nil {
		return "", err
	}
	if targetAbs != rootAbs && !strings.HasPrefix(targetAbs, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("local media path escapes category folder")
	}
	return targetAbs, nil
}

func taterLocalStreamURL(baseURL, categoryID string, sourceIndex int, relPath, playerToken string) string {
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/api/tater/local/stream")
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("category_id", categoryID)
	q.Set("source", strconv.Itoa(sourceIndex))
	q.Set("path", cleanLocalRelativePath(relPath))
	q.Set("player_token", playerToken)
	u.RawQuery = q.Encode()
	return u.String()
}

func taterBrowseLimit(cfg *config.Config) int {
	limit := cfg.Newznab.BrowseLimit
	if limit <= 0 {
		limit = 100
	}
	if limit < 10 {
		limit = 10
	}
	if limit > 500 {
		limit = 500
	}
	return limit
}

func taterFetchNewznab(ctx context.Context, cfg *config.Config, params map[string]string) ([]byte, error) {
	rawURL, err := taterNewznabURL(cfg, params)
	if err != nil {
		return nil, err
	}
	return taterFetchURL(ctx, cfg, rawURL)
}

func taterFetchURL(ctx context.Context, cfg *config.Config, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Tater Tube Server/1.0")
	client := httpclient.NewForExternal(httpclient.LongTimeout)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, taterUsenetMaxNzbFetchSize))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	if errMsg := taterNewznabError(bodyTrimForXML(body)); errMsg != "" {
		return nil, fmt.Errorf("%s", errMsg)
	}
	return body, nil
}

func taterNewznabURL(cfg *config.Config, params map[string]string) (string, error) {
	base := strings.TrimSpace(cfg.Newznab.URL)
	if base == "" {
		return "", fmt.Errorf("newznab URL is empty")
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		u, err = url.Parse("http://" + base)
		if err != nil {
			return "", err
		}
	}
	if taterIsOMGHost(u.Host) && !strings.EqualFold(u.Scheme, "https") {
		u.Scheme = "https"
	}
	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(strings.ToLower(path), "/api") {
		path += "/api"
	}
	u.Path = path
	q := u.Query()
	for key, value := range params {
		q.Set(key, value)
	}
	if !q.Has("apikey") && !q.Has("api") {
		q.Set("apikey", cleanTaterAPIKey(cfg.Newznab.APIKey))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func parseTaterNewznabCategories(data []byte) ([]taterUsenetCategory, error) {
	var caps newznabCaps
	if err := xml.Unmarshal(data, &caps); err != nil {
		return nil, fmt.Errorf("category XML invalid")
	}
	if caps.Error != nil {
		return nil, fmt.Errorf("newznab %s %s", caps.Error.Code, caps.Error.Description)
	}

	rows := make([]taterUsenetCategory, 0, len(caps.Categories))
	for _, cat := range caps.Categories {
		id := strings.TrimSpace(cat.ID)
		name := cleanTaterText(cat.Name)
		if id == "" || name == "" || !taterIsMediaCategoryGroup(id, name) {
			continue
		}

		children := []taterUsenetCategory{{
			ID:        id,
			Title:     "All " + name,
			FullTitle: name,
			Group:     name,
			IsSubcat:  false,
		}}
		for _, subcat := range cat.Subcats {
			subID := strings.TrimSpace(subcat.ID)
			subName := cleanTaterText(subcat.Name)
			if subID == "" || subName == "" {
				continue
			}
			children = append(children, taterUsenetCategory{
				ID:        subID,
				Title:     subName,
				FullTitle: name + " / " + subName,
				Group:     name,
				IsSubcat:  true,
			})
		}

		rows = append(rows, taterUsenetCategory{
			ID:       id,
			Title:    name,
			Group:    name,
			IsGroup:  true,
			Children: children,
			Count:    len(children),
		})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no media categories found")
	}
	return rows, nil
}

func parseTaterNewznabItems(data []byte, cfg *config.Config) ([]taterUsenetItem, error) {
	var rss newznabRSS
	if err := xml.Unmarshal(data, &rss); err != nil {
		return nil, fmt.Errorf("browse XML invalid")
	}
	if rss.Error != nil {
		return nil, fmt.Errorf("newznab %s %s", rss.Error.Code, rss.Error.Description)
	}

	rows := make([]taterUsenetItem, 0, len(rss.Channel.Items))
	for _, raw := range rss.Channel.Items {
		title := cleanTaterText(raw.Title)
		nzbURL := strings.TrimSpace(raw.Enclosure.URL)
		size := parseTaterInt64(raw.Enclosure.Length)
		item := taterUsenetItem{
			Title:       title,
			NzbURL:      nzbURL,
			GUID:        strings.TrimSpace(raw.GUID),
			Date:        cleanTaterText(raw.PubDate),
			Description: cleanTaterText(raw.Description),
			SizeBytes:   size,
		}

		for _, attr := range raw.Attrs {
			name := strings.ToLower(strings.TrimSpace(attr.Name))
			value := strings.TrimSpace(attr.Value)
			switch name {
			case "size":
				if n := parseTaterInt64(value); n > item.SizeBytes {
					item.SizeBytes = n
				}
			case "category":
				item.Category = cleanTaterText(value)
			case "guid":
				if item.GUID == "" {
					item.GUID = value
				}
			case "files":
				item.Files = value
			case "grabs":
				item.Grabs = value
			}
		}

		if item.NzbURL == "" {
			item.NzbURL = strings.TrimSpace(raw.Link)
		}
		item.NzbURL = taterResolveNzbURL(item.NzbURL, item.GUID, cfg)
		if item.SizeBytes > 0 {
			item.SizeText = formatTaterBytes(item.SizeBytes)
		}
		if item.Title != "" && item.NzbURL != "" {
			rows = append(rows, item)
		}
	}
	return rows, nil
}

func taterAnnotateNzbMediaTypes(items []taterUsenetItem, categoryID, category string) {
	for i := range items {
		if items[i].MediaType != "" && items[i].MediaType != "nzb" {
			continue
		}
		itemCategory := strings.TrimSpace(items[i].Category + " " + category)
		items[i].MediaType = taterNzbMediaType(categoryID, itemCategory)
	}
}

func taterNzbMediaType(categoryID, category string) string {
	normalizedCategory := strings.ToLower(cleanTaterText(category))
	switch {
	case strings.Contains(normalizedCategory, "movie") ||
		strings.Contains(normalizedCategory, "film"):
		return "movie"
	case strings.Contains(normalizedCategory, "television") ||
		strings.Contains(normalizedCategory, "series") ||
		strings.Contains(normalizedCategory, "tv"):
		return "episode"
	case strings.Contains(normalizedCategory, "audio") ||
		strings.Contains(normalizedCategory, "music"):
		return "audio"
	}

	for _, rawID := range strings.Split(categoryID, ",") {
		id, err := strconv.Atoi(strings.TrimSpace(rawID))
		if err != nil {
			continue
		}
		switch {
		case id >= 2000 && id < 3000:
			return "movie"
		case id >= 3000 && id < 4000:
			return "audio"
		case id >= 5000 && id < 6000:
			return "episode"
		}
	}
	return "nzb"
}

func taterResolveNzbURL(rawURL, guid string, cfg *config.Config) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL != "" {
		if id := taterOmgItemID(rawURL); id != "" && taterIsOMGURL(rawURL) && !strings.Contains(strings.ToLower(rawURL), "/nzb/") {
			return taterOmgNzbURL(cfg, id)
		}
		return taterEnsureNewznabKey(rawURL, cfg)
	}
	guid = strings.TrimSpace(guid)
	if guid == "" {
		return ""
	}
	if id := taterOmgItemID(guid); id != "" {
		return taterOmgNzbURL(cfg, id)
	}
	rawURL, err := taterNewznabURL(cfg, map[string]string{"t": "get", "id": guid})
	if err != nil {
		return ""
	}
	return rawURL
}

func taterEnsureNewznabKey(rawURL string, cfg *config.Config) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimSpace(rawURL)
	}
	q := u.Query()
	if q.Has("apikey") || q.Has("api") {
		return u.String()
	}
	if taterIsOMGHost(u.Host) && strings.HasPrefix(strings.ToLower(u.Path), "/nzb") {
		q.Set("user", strings.TrimSpace(cfg.Newznab.Username))
		q.Set("api", cleanTaterAPIKey(cfg.Newznab.APIKey))
	} else {
		q.Set("apikey", cleanTaterAPIKey(cfg.Newznab.APIKey))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func taterStripNewznabAuth(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimSpace(rawURL)
	}
	q := u.Query()
	q.Del("apikey")
	q.Del("api")
	q.Del("user")
	u.RawQuery = q.Encode()
	return u.String()
}

func taterTrendingRows() []taterUsenetCategory {
	return []taterUsenetCategory{
		{Type: "trending", Title: "Movies Today", Detail: "MOVIE", Group: "Trending", FullTitle: "Movies Today", ID: "movie:today", Category: "movie", Time: "today"},
		{Type: "trending", Title: "Movies Week", Detail: "MOVIE", Group: "Trending", FullTitle: "Movies Week", ID: "movie:week", Category: "movie", Time: "week"},
		{Type: "trending", Title: "Movies Month", Detail: "MOVIE", Group: "Trending", FullTitle: "Movies Month", ID: "movie:month", Category: "movie", Time: "month"},
		{Type: "trending", Title: "Movies Year", Detail: "MOVIE", Group: "Trending", FullTitle: "Movies Year", ID: "movie:year", Category: "movie", Time: "year"},
		{Type: "trending", Title: "TV Today", Detail: "TV", Group: "Trending", FullTitle: "TV Today", ID: "tv:today", Category: "tv", Time: "today"},
		{Type: "trending", Title: "TV Week", Detail: "TV", Group: "Trending", FullTitle: "TV Week", ID: "tv:week", Category: "tv", Time: "week"},
		{Type: "trending", Title: "TV Month", Detail: "TV", Group: "Trending", FullTitle: "TV Month", ID: "tv:month", Category: "tv", Time: "month"},
		{Type: "trending", Title: "TV Year", Detail: "TV", Group: "Trending", FullTitle: "TV Year", ID: "tv:year", Category: "tv", Time: "year"},
	}
}

func taterDiscoverRows() []taterUsenetCategory {
	year := strconv.Itoa(time.Now().Year())
	return []taterUsenetCategory{
		{Type: "discover", Title: "Popular Movies", Detail: "MOVIE", Group: "Discover", FullTitle: "Popular Movies", ID: "movie:top", Category: "movie"},
		{Type: "discover", Title: "New Movies", Detail: year, Group: "Discover", FullTitle: "New Movies", ID: "movie:year:" + year, Category: "movie", Time: year},
		{Type: "discover", Title: "Featured Movies", Detail: "MOVIE", Group: "Discover", FullTitle: "Featured Movies", ID: "movie:imdbrating", Category: "movie"},
		{Type: "discover", Title: "Popular TV", Detail: "TV", Group: "Discover", FullTitle: "Popular TV", ID: "series:top", Category: "series"},
		{Type: "discover", Title: "New TV", Detail: year, Group: "Discover", FullTitle: "New TV", ID: "series:year:" + year, Category: "series", Time: year},
		{Type: "discover", Title: "Featured TV", Detail: "TV", Group: "Discover", FullTitle: "Featured TV", ID: "series:imdbrating", Category: "series"},
	}
}

func taterDiscoverCatalog(catalog string) (mediaType, catalogID, genre, title string, err error) {
	switch catalog {
	case "movie:top":
		return "movie", "top", "", "Popular Movies", nil
	case "movie:imdbrating":
		return "movie", "imdbRating", "", "Featured Movies", nil
	case "series:top":
		return "series", "top", "", "Popular TV", nil
	case "series:imdbrating":
		return "series", "imdbRating", "", "Featured TV", nil
	}

	parts := strings.Split(catalog, ":")
	if len(parts) == 3 && (parts[0] == "movie" || parts[0] == "series") && parts[1] == "year" {
		year := strings.TrimSpace(parts[2])
		if len(year) != 4 {
			return "", "", "", "", fmt.Errorf("Discover year is invalid")
		}
		if parts[0] == "movie" {
			return "movie", "year", year, "New Movies", nil
		}
		return "series", "year", year, "New TV", nil
	}

	return "", "", "", "", fmt.Errorf("Discover catalog is invalid")
}

func taterCinemetaCatalogURL(mediaType, catalogID, genre string) string {
	path := fmt.Sprintf("/catalog/%s/%s.json", url.PathEscape(mediaType), url.PathEscape(catalogID))
	if genre != "" {
		path = fmt.Sprintf("/catalog/%s/%s/genre=%s.json", url.PathEscape(mediaType), url.PathEscape(catalogID), url.PathEscape(genre))
	}
	u := url.URL{
		Scheme: "https",
		Host:   "v3-cinemeta.strem.io",
		Path:   path,
	}
	return u.String()
}

func parseTaterCinemetaItems(data []byte, mediaType string) ([]taterUsenetItem, error) {
	var catalog cinemetaCatalogResponse
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("Discover JSON invalid")
	}

	rows := make([]taterUsenetItem, 0, len(catalog.Metas))
	for _, meta := range catalog.Metas {
		title := cleanTaterText(meta.Name)
		if title == "" {
			continue
		}

		release := cleanTaterText(meta.ReleaseInfo)
		if release == "" {
			release = cleanTaterText(meta.Year)
		}
		if release == "" && meta.Released != "" {
			release = cleanTaterText(strings.Split(meta.Released, "T")[0])
		}

		kind := mediaType
		if kind == "" {
			kind = strings.ToLower(strings.TrimSpace(meta.Type))
		}
		label := "MOVIE"
		searchQuery := title
		if kind == "series" {
			label = "TV"
		} else if release != "" {
			searchQuery = title + " " + release
		}

		genres := meta.Genres
		if len(genres) == 0 {
			genres = meta.Genre
		}
		genreText := cleanTaterText(strings.Join(genres, " / "))
		category := label
		if genreText != "" {
			category += " / " + genreText
		}

		guid := strings.TrimSpace(meta.ID)
		if guid == "" {
			guid = strings.TrimSpace(meta.IMDBID)
		}

		sizeText := label
		if release != "" {
			sizeText += " " + release
		}

		rows = append(rows, taterUsenetItem{
			Title:       title,
			Type:        "discovery",
			MediaType:   kind,
			SearchQuery: searchQuery,
			GUID:        guid,
			Date:        release,
			Description: cleanTaterText(meta.Description),
			Category:    category,
			Poster:      strings.TrimSpace(meta.Poster),
			SizeText:    sizeText,
		})
	}
	return rows, nil
}

func taterIsMediaCategoryGroup(id, name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if strings.Contains(lower, "app") ||
		strings.Contains(lower, "game") ||
		strings.Contains(lower, "other") ||
		strings.Contains(lower, "xxx") {
		return false
	}
	return id == "2000" ||
		id == "3000" ||
		id == "5000" ||
		strings.Contains(lower, "movie") ||
		lower == "tv" ||
		strings.Contains(lower, "television") ||
		strings.Contains(lower, "audio") ||
		strings.Contains(lower, "music")
}

func taterOmgTrendingURL(cfg *config.Config, category, period string) string {
	u := url.URL{
		Scheme: "https",
		Host:   "rss.omgwtfnzbs.org",
		Path:   "/rss-trends.php",
	}
	q := u.Query()
	q.Set("user", strings.TrimSpace(cfg.Newznab.Username))
	q.Set("api", cleanTaterAPIKey(cfg.Newznab.APIKey))
	q.Set("cat", category)
	q.Set("s", "")
	q.Set("time", period)
	q.Set("res", "")
	u.RawQuery = q.Encode()
	return u.String()
}

func taterOmgNzbURL(cfg *config.Config, id string) string {
	u := url.URL{
		Scheme: "https",
		Host:   "api.omgwtfnzbs.org",
		Path:   "/nzb/",
	}
	q := u.Query()
	q.Set("id", strings.TrimSpace(id))
	q.Set("user", strings.TrimSpace(cfg.Newznab.Username))
	q.Set("api", cleanTaterAPIKey(cfg.Newznab.APIKey))
	u.RawQuery = q.Encode()
	return u.String()
}

func taterOmgItemID(value string) string {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(u.Query().Get("id"))
}

func taterIsOMGURL(value string) bool {
	u, err := url.Parse(strings.TrimSpace(value))
	return err == nil && taterIsOMGHost(u.Host)
}

func taterNewznabHost(value string) string {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.Host == "" {
		u, err = url.Parse("http://" + strings.TrimSpace(value))
	}
	if err != nil {
		return ""
	}
	return u.Host
}

func taterIsOMGHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "omgwtfnzbs.org" || host == "api.omgwtfnzbs.org" || host == "rss.omgwtfnzbs.org"
}

func taterNewznabError(data []byte) string {
	if len(data) == 0 || data[0] != '<' {
		return ""
	}
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		tok, err := decoder.Token()
		if err != nil {
			return ""
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "error" {
			continue
		}
		var code, desc string
		for _, attr := range start.Attr {
			switch attr.Name.Local {
			case "code":
				code = attr.Value
			case "description":
				desc = attr.Value
			}
		}
		return cleanTaterText("NEWZNAB " + code + " " + desc)
	}
}

func bodyTrimForXML(data []byte) []byte {
	return []byte(strings.TrimSpace(string(data)))
}

func stableTaterNzbFilename(title, sourceURL string) string {
	title = cleanTaterText(title)
	if title == "" {
		if u, err := url.Parse(sourceURL); err == nil {
			title = filepath.Base(u.Path)
		}
	}
	title = sanitizeFilename(title)
	if title == "" {
		title = "tater-tube"
	}
	if len(title) > 80 {
		title = title[:80]
	}
	sum := sha1.Sum([]byte(sourceURL))
	return title + "-" + hex.EncodeToString(sum[:])[:12] + ".nzb"
}

func cleanTaterAPIKey(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasSuffix(value, "Copy") {
		value = strings.TrimSuffix(value, "Copy")
	}
	return strings.TrimSpace(value)
}

func cleanTaterText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func parseTaterInt64(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func parseTaterInt(value string, fallback int) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func formatTaterBytes(bytes int64) string {
	if bytes <= 0 {
		return ""
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value := float64(bytes)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit >= 3 {
		return fmt.Sprintf("%.1f %s", value, units[unit])
	}
	return fmt.Sprintf("%.0f %s", value, units[unit])
}
