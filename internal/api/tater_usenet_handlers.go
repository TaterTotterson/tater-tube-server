package api

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/auth"
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
	GUID        string `json:"guid,omitempty"`
	Date        string `json:"date,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
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

func (s *Server) handleTaterUsenetStatus(c *fiber.Ctx) error {
	cfg, downloadKey, ok := s.taterUsenetAuthorizedConfig(c)
	if !ok {
		return nil
	}
	return RespondSuccess(c, fiber.Map{
		"configured":   taterNewznabEnabled(cfg),
		"download_key": downloadKey,
	})
}

func (s *Server) handleTaterUsenetCatalog(c *fiber.Ctx) error {
	cfg, _, ok := s.taterUsenetAuthorizedConfig(c)
	if !ok {
		return nil
	}
	if !taterNewznabEnabled(cfg) {
		return RespondServiceUnavailable(c, "Newznab Stream catalog is not configured", "")
	}

	body, err := taterFetchNewznab(c.Context(), cfg, map[string]string{"t": "caps"})
	if err != nil {
		return RespondServiceUnavailable(c, "Failed to load Newznab categories", err.Error())
	}
	children, err := parseTaterNewznabCategories(body)
	if err != nil {
		return RespondServiceUnavailable(c, "Failed to parse Newznab categories", err.Error())
	}

	streamChildren := []taterUsenetCategory{
		{Type: "search", Title: "Search", Detail: "ALL MEDIA"},
		{Type: "trendingRoot", Title: "Trending", Detail: "PROVIDER", Children: taterTrendingRows()},
	}
	streamChildren = append(streamChildren, children...)

	return RespondSuccess(c, fiber.Map{
		"categories": []taterUsenetCategory{{
			ID:       "stream",
			Title:    "Stream",
			Detail:   "SERVER",
			Type:     "group",
			IsGroup:  true,
			Children: streamChildren,
			Count:    len(streamChildren),
		}},
	})
}

func (s *Server) handleTaterUsenetItems(c *fiber.Ctx) error {
	cfg, _, ok := s.taterUsenetAuthorizedConfig(c)
	if !ok {
		return nil
	}
	if !taterNewznabEnabled(cfg) {
		return RespondServiceUnavailable(c, "Newznab Stream catalog is not configured", "")
	}

	categoryID := strings.TrimSpace(c.Query("category_id"))
	if categoryID == "" {
		return RespondValidationError(c, "Category is required", "category_id is empty")
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
	cfg, downloadKey, ok := s.taterUsenetAuthorizedConfig(c)
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
	baseURL := resolveBaseURL(c, cfg.Stremio.BaseURL)
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
	return s.waitAndRespond(c, itemID, baseURL, downloadKey, nzbName, nil, req.Timeout)
}

func (s *Server) taterUsenetAuthorizedConfig(c *fiber.Ctx) (*config.Config, string, bool) {
	if s.configManager == nil {
		RespondServiceUnavailable(c, "Configuration not available", "")
		return nil, "", false
	}

	downloadKey := strings.TrimSpace(c.Query("download_key"))
	if downloadKey == "" {
		downloadKey = strings.TrimSpace(c.FormValue("download_key"))
	}
	if downloadKey != "" {
		if s.validateDownloadKey(c.Context(), downloadKey) {
			return s.configManager.GetConfig(), downloadKey, true
		}
		if s.validateAPIKey(c, downloadKey) {
			return s.configManager.GetConfig(), auth.HashAPIKey(downloadKey), true
		}
		slog.WarnContext(c.Context(), "Tater Tube Stream endpoint: invalid download key")
		RespondUnauthorized(c, "Invalid download_key", "")
		return nil, "", false
	}

	if rawKey := strings.TrimSpace(c.Get("X-Api-Key")); rawKey != "" {
		if s.validateAPIKey(c, rawKey) {
			return s.configManager.GetConfig(), auth.HashAPIKey(rawKey), true
		}
		slog.WarnContext(c.Context(), "Tater Tube Stream endpoint: invalid X-Api-Key")
		RespondUnauthorized(c, "Invalid X-Api-Key", "")
		return nil, "", false
	}

	RespondUnauthorized(c, "Authentication required", "Provide download_key or X-Api-Key")
	return nil, "", false
}

func taterNewznabEnabled(cfg *config.Config) bool {
	return cfg != nil &&
		cfg.Newznab.Enabled != nil &&
		*cfg.Newznab.Enabled &&
		strings.TrimSpace(cfg.Newznab.URL) != "" &&
		strings.TrimSpace(cleanTaterAPIKey(cfg.Newznab.APIKey)) != ""
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
