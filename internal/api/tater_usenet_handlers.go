package api

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
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
	Title       string `json:"title"`
	NzbURL      string `json:"nzbUrl"`
	Type        string `json:"type,omitempty"`
	MediaType   string `json:"mediaType,omitempty"`
	SearchQuery string `json:"searchQuery,omitempty"`
	CategoryID  string `json:"categoryId,omitempty"`
	SourceIndex int    `json:"sourceIndex,omitempty"`
	Path        string `json:"path,omitempty"`
	StreamURL   string `json:"streamUrl,omitempty"`
	GUID        string `json:"guid,omitempty"`
	Date        string `json:"date,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	Poster      string `json:"poster,omitempty"`
	Files       string `json:"files,omitempty"`
	Grabs       string `json:"grabs,omitempty"`
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
	SizeText    string `json:"sizeText,omitempty"`
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
		"configured": taterNewznabEnabled(cfg) || taterLocalMediaEnabled(cfg),
	})
}

func (s *Server) handleTaterUsenetCatalog(c *fiber.Ctx) error {
	cfg, _, ok := s.taterUsenetAuthorizedConfig(c)
	if !ok {
		return nil
	}
	if !taterNewznabEnabled(cfg) && !taterLocalMediaEnabled(cfg) {
		return RespondServiceUnavailable(c, "Stream catalog is not configured", "")
	}

	categories := []taterUsenetCategory{}
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
		"categories": categories,
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
		title := strings.TrimSpace(c.Query("title"))
		if title == "" {
			title = "Local"
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

	safeFilename := stableTaterNzbFilename(req.Title, downloadURL)
	if fileinfo.IsProbablyObfuscated(nzbtrim.TrimNzbExtension(safeFilename)) {
		if derived := deriveNzbNameFromContent(nzbData); derived != "" {
			safeFilename = stableTaterNzbFilename(derived, downloadURL)
		}
	}
	nzbName := nzbtrim.TrimNzbExtension(safeFilename)
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
	return s.waitAndRespondWithStreamAuth(c, itemID, baseURL, "player_token", playerToken, nzbName, nil, req.Timeout)
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

func taterLocalRootRow(cfg *config.Config) taterUsenetCategory {
	children := []taterUsenetCategory{}
	for _, cat := range cfg.LocalMedia.Categories {
		if cat.Enabled != nil && !*cat.Enabled {
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
		return taterLocalTVItems(cat, paths, baseURL, playerToken, sourceIndex, relPath)
	case "folders":
		return taterLocalFolderItems(cat, paths, baseURL, playerToken, sourceIndex, relPath)
	default:
		return taterLocalMovieItems(cat, paths, baseURL, playerToken)
	}
}

func taterLocalFolderItems(cat config.LocalMediaCategory, paths []string, baseURL, playerToken string, sourceIndex int, relPath string) ([]taterUsenetItem, error) {
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
			SizeBytes:   size,
		}
		if size > 0 {
			item.SizeText = formatTaterBytes(size)
		}
		items = append(items, item)
	}
	return items, nil
}

func taterLocalMovieItems(cat config.LocalMediaCategory, paths []string, baseURL, playerToken string) ([]taterUsenetItem, error) {
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
				Date:        year,
				SizeBytes:   size,
				SizeText:    localMovieDetail(year),
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

func taterLocalTVItems(cat config.LocalMediaCategory, paths []string, baseURL, playerToken string, sourceIndex int, relPath string) ([]taterUsenetItem, error) {
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
			SizeBytes:   size,
			SizeText:    "EPISODE",
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
	return items, nil
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
	localYearPattern      = regexp.MustCompile(`\b(19[0-9]{2}|20[0-9]{2})\b`)
	localQualityTail      = regexp.MustCompile(`(?i)\b(2160p|1080p|720p|480p|bluray|blu-ray|brrip|webrip|web-dl|webdl|hdtv|x264|x265|h264|h265|hevc|aac|dts|truehd|atmos|proper|repack|extended|remux)\b.*$`)
	localEpisodePattern   = regexp.MustCompile(`(?i)\bS([0-9]{1,2})E([0-9]{1,3})\b`)
	localSeasonPattern    = regexp.MustCompile(`(?i)^season[\s._-]*([0-9]{1,2})$|^s([0-9]{1,2})$`)
	localSeparatorPattern = regexp.MustCompile(`[._]+`)
)

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
	client := httpclient.NewForExternal(cfg.Network, httpclient.LongTimeout)
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
