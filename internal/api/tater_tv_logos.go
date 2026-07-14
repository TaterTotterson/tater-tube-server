package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
)

const (
	taterTVLogoTreeURL = "https://api.github.com/repos/tv-logo/tv-logos/git/trees/main?recursive=1"
	taterTVLogoRawBase = "https://raw.githubusercontent.com/tv-logo/tv-logos/main/"
)

type tubeTVLogoSearchResponse struct {
	Logos []tubeTVLogoResult `json:"logos"`
}

type tubeTVLogoResult struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Size  int64  `json:"size,omitempty"`
}

type tubeTVLogoTreeResponse struct {
	Tree []struct {
		Path string `json:"path"`
		Type string `json:"type"`
		Size int64  `json:"size,omitempty"`
	} `json:"tree"`
}

var taterTVLogoIndex = struct {
	sync.Mutex
	fetchedAt time.Time
	logos     []tubeTVLogoResult
}{}

func (s *Server) handleTubeTVLogoSearch(c *fiber.Ctx) error {
	query := strings.TrimSpace(c.Query("q"))
	limit := 60
	if parsed := c.QueryInt("limit", limit); parsed > 0 && parsed <= 120 {
		limit = parsed
	}
	logos, err := searchTaterTVLogos(context.Background(), query, limit)
	if err != nil {
		return RespondServiceUnavailable(c, "Channel logo search failed", err.Error())
	}
	return RespondSuccess(c, tubeTVLogoSearchResponse{Logos: logos})
}

func searchTaterTVLogos(ctx context.Context, query string, limit int) ([]tubeTVLogoResult, error) {
	logos, err := loadTaterTVLogoIndex(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 60
	}
	terms := logoSearchTerms(query)
	type scoredLogo struct {
		logo  tubeTVLogoResult
		score int
	}
	scored := make([]scoredLogo, 0, len(logos))
	for _, logo := range logos {
		score := scoreTaterTVLogo(logo, terms)
		if len(terms) > 0 && score <= 0 {
			continue
		}
		scored = append(scored, scoredLogo{logo: logo, score: score})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].logo.Title < scored[j].logo.Title
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	results := make([]tubeTVLogoResult, 0, len(scored))
	for _, row := range scored {
		results = append(results, row.logo)
	}
	return results, nil
}

func loadTaterTVLogoIndex(ctx context.Context) ([]tubeTVLogoResult, error) {
	taterTVLogoIndex.Lock()
	defer taterTVLogoIndex.Unlock()

	if len(taterTVLogoIndex.logos) > 0 && time.Since(taterTVLogoIndex.fetchedAt) < 24*time.Hour {
		return append([]tubeTVLogoResult(nil), taterTVLogoIndex.logos...), nil
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, taterTVLogoTreeURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "TaterTubeServer")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("logo catalog returned HTTP %d", resp.StatusCode)
	}

	var tree tubeTVLogoTreeResponse
	if err := json.NewDecoder(resp.Body).Decode(&tree); err != nil {
		return nil, err
	}
	logos := make([]tubeTVLogoResult, 0, len(tree.Tree))
	for _, row := range tree.Tree {
		logoPath := config.SanitizeTubeTVLogoPath(row.Path)
		if row.Type != "blob" || logoPath == "" {
			continue
		}
		name := strings.ToLower(filepath.Base(logoPath))
		if strings.Contains(name, "mosaic") {
			continue
		}
		logos = append(logos, tubeTVLogoResult{
			Path:  logoPath,
			Title: titleFromLogoPath(logoPath),
			URL:   rawTaterTVLogoURL(logoPath),
			Size:  row.Size,
		})
	}
	if len(logos) == 0 {
		return nil, fmt.Errorf("logo catalog returned no png logos")
	}
	taterTVLogoIndex.logos = logos
	taterTVLogoIndex.fetchedAt = time.Now()
	return append([]tubeTVLogoResult(nil), logos...), nil
}

func scoreTaterTVLogo(logo tubeTVLogoResult, terms []string) int {
	if len(terms) == 0 {
		if strings.Contains(logo.Path, "countries/united-states/") {
			return 2
		}
		return 1
	}
	haystack := normalizeLogoSearchText(logo.Title + " " + logo.Path)
	score := 0
	for _, term := range terms {
		if strings.Contains(haystack, term) {
			score += 10
		}
	}
	if strings.Contains(logo.Path, "countries/united-states/") {
		score += 2
	}
	if strings.HasPrefix(haystack, strings.Join(terms, " ")) {
		score += 4
	}
	return score
}

func logoSearchTerms(query string) []string {
	normalized := normalizeLogoSearchText(query)
	if normalized == "" {
		return nil
	}
	return strings.Fields(normalized)
}

func normalizeLogoSearchText(value string) string {
	value = strings.ToLower(value)
	replacer := strings.NewReplacer("-", " ", "_", " ", "/", " ", ".", " ")
	value = replacer.Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

func titleFromLogoPath(logoPath string) string {
	base := strings.TrimSuffix(path.Base(logoPath), path.Ext(logoPath))
	base = strings.TrimSuffix(base, "-us")
	base = strings.TrimSuffix(base, "-uk")
	parts := strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(base))
	for i, part := range parts {
		if len(part) <= 3 {
			parts[i] = strings.ToUpper(part)
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func rawTaterTVLogoURL(logoPath string) string {
	segments := strings.Split(config.SanitizeTubeTVLogoPath(logoPath), "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return taterTVLogoRawBase + strings.Join(segments, "/")
}

func taterTVChannelLogosEnabled(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return cfg.TubeTV.ChannelLogosEnabled == nil || *cfg.TubeTV.ChannelLogosEnabled
}

func taterTVLogoRoot(cfg *config.Config) string {
	root := ""
	if cfg != nil {
		root = strings.TrimSpace(cfg.Metadata.RootPath)
	}
	if root == "" {
		root = "/config/metadata"
	}
	return filepath.Join(root, "tube-tv-channel-logos")
}

func taterTVResolveLogoFile(ctx context.Context, cfg *config.Config, logoPath string) (string, error) {
	logoPath = config.SanitizeTubeTVLogoPath(logoPath)
	if logoPath == "" {
		return "", fmt.Errorf("channel logo path is empty")
	}
	root := taterTVLogoRoot(cfg)
	localPath, err := safeLocalPath(root, logoPath)
	if err != nil {
		return "", err
	}
	if stat, err := os.Stat(localPath); err == nil && !stat.IsDir() && stat.Size() > 0 {
		return localPath, nil
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawTaterTVLogoURL(logoPath), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "TaterTubeServer")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("logo download returned HTTP %d", resp.StatusCode)
	}

	tmp := localPath + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(file, io.LimitReader(resp.Body, 8*1024*1024))
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return "", closeErr
	}
	if err := os.Rename(tmp, localPath); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return localPath, nil
}
