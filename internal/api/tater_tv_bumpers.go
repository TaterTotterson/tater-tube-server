package api

import (
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
)

const (
	taterTVBumperBefore = "before"
	taterTVBumperAfter  = "after"
	taterTVBumperBoth   = "both"
)

var taterTVBumperPlacements = []string{
	taterTVBumperBefore,
	taterTVBumperAfter,
	taterTVBumperBoth,
}

type taterTVBumper struct {
	Title          string  `json:"title"`
	GroupID        string  `json:"groupId"`
	Group          string  `json:"group"`
	Placement      string  `json:"placement"`
	PlacementLabel string  `json:"placementLabel"`
	Name           string  `json:"name"`
	URL            string  `json:"url,omitempty"`
	Kind           string  `json:"kind"`
	Local          bool    `json:"local"`
	Duration       float64 `json:"duration"`
	FullDuration   float64 `json:"fullDuration"`
	DurationKnown  bool    `json:"durationKnown"`
}

type taterTVBumperGroup struct {
	ID             string          `json:"id"`
	Title          string          `json:"title"`
	Placement      string          `json:"placement"`
	PlacementLabel string          `json:"placementLabel"`
	Count          int             `json:"count"`
	Videos         []taterTVBumper `json:"videos"`
}

func (s *Server) handleTubeTVBumperLibrary(c *fiber.Ctx) error {
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondInternalError(c, "Configuration not available", "CONFIG_NOT_FOUND")
	}
	return RespondSuccess(c, fiber.Map{
		"root":   taterTVBumperRoot(cfg),
		"groups": taterTVBumperGroups(cfg, "", ""),
	})
}

func (s *Server) handleTubeTVCreateBumperGroup(c *fiber.Ctx) error {
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondInternalError(c, "Configuration not available", "CONFIG_NOT_FOUND")
	}
	var body struct {
		Name      string `json:"name"`
		Placement string `json:"placement"`
	}
	if err := c.BodyParser(&body); err != nil {
		return RespondValidationError(c, "Invalid bumper group request", err.Error())
	}
	groupID := taterTVCategoryID(body.Name, "")
	placement := taterTVNormalizeBumperPlacement(body.Placement)
	if groupID == "" || placement == "" {
		return RespondValidationError(c, "Bumper group name and placement are required", "placement must be before, after, or both")
	}
	if _, _, exists := taterTVFindBumperGroup(cfg, groupID); exists {
		return RespondConflict(c, "Bumper group already exists", groupID)
	}
	if err := os.MkdirAll(taterTVBumperGroupPath(cfg, placement, groupID), 0755); err != nil {
		return RespondInternalError(c, "Failed to create bumper group", err.Error())
	}
	taterTVResetGuideForConfig(cfg)
	return s.handleTubeTVBumperLibrary(c)
}

func (s *Server) handleTubeTVUploadBumpers(c *fiber.Ctx) error {
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondInternalError(c, "Configuration not available", "CONFIG_NOT_FOUND")
	}
	groupID := taterTVCategoryID(c.FormValue("group"), "")
	placement := taterTVNormalizeBumperPlacement(c.FormValue("placement"))
	if groupID == "" || placement == "" {
		return RespondValidationError(c, "Bumper group and placement are required", "placement must be before, after, or both")
	}
	if foundPlacement, _, exists := taterTVFindBumperGroup(cfg, groupID); exists && foundPlacement != placement {
		return RespondConflict(c, "Bumper group belongs to a different placement", foundPlacement)
	}
	form, err := c.MultipartForm()
	if err != nil {
		return RespondValidationError(c, "Bumper upload invalid", err.Error())
	}
	files := form.File["files"]
	if len(files) == 0 {
		return RespondValidationError(c, "No bumper files uploaded", "files field is empty")
	}
	dir := taterTVBumperGroupPath(cfg, placement, groupID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return RespondInternalError(c, "Failed to create bumper group", err.Error())
	}
	for _, header := range files {
		name := taterTVSafeFileName(header.Filename)
		if !isMediaExtension(filepath.Ext(name)) {
			continue
		}
		src, err := header.Open()
		if err != nil {
			return RespondInternalError(c, "Failed to open uploaded bumper", err.Error())
		}
		dst, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			_ = src.Close()
			return RespondInternalError(c, "Failed to save bumper", err.Error())
		}
		_, copyErr := io.Copy(dst, src)
		closeErr := dst.Close()
		_ = src.Close()
		if copyErr != nil {
			return RespondInternalError(c, "Failed to save bumper", copyErr.Error())
		}
		if closeErr != nil {
			return RespondInternalError(c, "Failed to finish bumper upload", closeErr.Error())
		}
	}
	taterTVResetGuideForConfig(cfg)
	return s.handleTubeTVBumperLibrary(c)
}

func (s *Server) handleTubeTVDeleteBumperFile(c *fiber.Ctx) error {
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondInternalError(c, "Configuration not available", "CONFIG_NOT_FOUND")
	}
	groupID := taterTVCategoryID(c.Query("group"), "")
	placement := taterTVNormalizeBumperPlacement(c.Query("placement"))
	rawName := strings.TrimSpace(c.Query("name"))
	name := taterTVSafeFileName(rawName)
	if groupID == "" || placement == "" || rawName == "" {
		return RespondValidationError(c, "Bumper file is required", "placement, group, and name are required")
	}
	if err := os.Remove(filepath.Join(taterTVBumperGroupPath(cfg, placement, groupID), name)); err != nil && !os.IsNotExist(err) {
		return RespondInternalError(c, "Failed to delete bumper", err.Error())
	}
	taterTVResetGuideForConfig(cfg)
	return s.handleTubeTVBumperLibrary(c)
}

func (s *Server) handleTubeTVDeleteBumperGroup(c *fiber.Ctx) error {
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondInternalError(c, "Configuration not available", "CONFIG_NOT_FOUND")
	}
	groupID := taterTVCategoryID(c.Query("group"), "")
	placement := taterTVNormalizeBumperPlacement(c.Query("placement"))
	if groupID == "" || placement == "" {
		return RespondValidationError(c, "Bumper group is required", "placement and group are required")
	}
	if err := os.RemoveAll(taterTVBumperGroupPath(cfg, placement, groupID)); err != nil {
		return RespondInternalError(c, "Failed to delete bumper group", err.Error())
	}
	taterTVResetGuideForConfig(cfg)
	return s.handleTubeTVBumperLibrary(c)
}

func (s *Server) handleTaterTVBumperFile(c *fiber.Ctx) error {
	cfg, _, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}
	groupID := taterTVCategoryID(c.Query("group"), "")
	placement := taterTVNormalizeBumperPlacement(c.Query("placement"))
	rawName := strings.TrimSpace(c.Query("name"))
	name := taterTVSafeFileName(rawName)
	if groupID == "" || placement == "" || rawName == "" {
		return RespondValidationError(c, "Bumper file is required", "placement, group, and name are required")
	}
	path := filepath.Join(taterTVBumperGroupPath(cfg, placement, groupID), name)
	if !isMediaExtension(filepath.Ext(path)) {
		return RespondValidationError(c, "Bumper file type is not supported", filepath.Ext(path))
	}
	if stat, err := os.Stat(path); err != nil || stat.IsDir() {
		return RespondNotFound(c, "Bumper file not found", "")
	}
	c.Set("Accept-Ranges", "bytes")
	return c.SendFile(path, false)
}

func taterTVBumperGroups(cfg *config.Config, baseURL, playerToken string) []taterTVBumperGroup {
	groups := []taterTVBumperGroup{}
	for _, placement := range taterTVBumperPlacements {
		root := filepath.Join(taterTVBumperRoot(cfg), placement)
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			groupID := taterTVCategoryID(entry.Name(), "")
			if groupID == "" {
				continue
			}
			files, _ := os.ReadDir(filepath.Join(root, entry.Name()))
			videos := []taterTVBumper{}
			for _, file := range files {
				if file.IsDir() || strings.HasPrefix(file.Name(), ".") || !isMediaExtension(filepath.Ext(file.Name())) {
					continue
				}
				path := filepath.Join(root, entry.Name(), file.Name())
				duration := taterLocalDurationSeconds(cfg, path)
				if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
					continue
				}
				title := cleanMovieTitleFromName(strings.TrimSuffix(file.Name(), filepath.Ext(file.Name())))
				if title == "" {
					title = "Bumper"
				}
				videos = append(videos, taterTVBumper{
					Title:          title,
					GroupID:        groupID,
					Group:          taterTVBumperGroupTitle(groupID),
					Placement:      placement,
					PlacementLabel: taterTVBumperPlacementLabel(placement),
					Name:           file.Name(),
					URL:            taterTVBumperURL(baseURL, placement, groupID, file.Name(), playerToken),
					Kind:           "bumper",
					Local:          false,
					Duration:       duration,
					FullDuration:   duration,
					DurationKnown:  true,
				})
			}
			sort.SliceStable(videos, func(i, j int) bool {
				return strings.ToLower(videos[i].Title) < strings.ToLower(videos[j].Title)
			})
			groups = append(groups, taterTVBumperGroup{
				ID:             groupID,
				Title:          taterTVBumperGroupTitle(groupID),
				Placement:      placement,
				PlacementLabel: taterTVBumperPlacementLabel(placement),
				Count:          len(videos),
				Videos:         videos,
			})
		}
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Placement != groups[j].Placement {
			return taterTVBumperPlacementRank(groups[i].Placement) < taterTVBumperPlacementRank(groups[j].Placement)
		}
		return strings.ToLower(groups[i].Title) < strings.ToLower(groups[j].Title)
	})
	return groups
}

func taterTVBumperRoot(cfg *config.Config) string {
	if cfg == nil || strings.TrimSpace(cfg.Metadata.RootPath) == "" {
		return filepath.Join(os.TempDir(), "tater-tube-bumpers")
	}
	return filepath.Join(filepath.Clean(cfg.Metadata.RootPath), "tube-tv-bumpers")
}

func taterTVBumperGroupPath(cfg *config.Config, placement, groupID string) string {
	return filepath.Join(taterTVBumperRoot(cfg), taterTVNormalizeBumperPlacement(placement), taterTVCategoryID(groupID, ""))
}

func taterTVFindBumperGroup(cfg *config.Config, groupID string) (string, string, bool) {
	groupID = taterTVCategoryID(groupID, "")
	if groupID == "" {
		return "", "", false
	}
	for _, placement := range taterTVBumperPlacements {
		path := taterTVBumperGroupPath(cfg, placement, groupID)
		if stat, err := os.Stat(path); err == nil && stat.IsDir() {
			return placement, path, true
		}
	}
	return "", "", false
}

func taterTVNormalizeBumperPlacement(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case taterTVBumperBefore:
		return taterTVBumperBefore
	case taterTVBumperAfter:
		return taterTVBumperAfter
	case taterTVBumperBoth:
		return taterTVBumperBoth
	default:
		return ""
	}
}

func taterTVBumperPlacementLabel(placement string) string {
	switch taterTVNormalizeBumperPlacement(placement) {
	case taterTVBumperBefore:
		return "Before commercials"
	case taterTVBumperAfter:
		return "After commercials"
	case taterTVBumperBoth:
		return "Before + after"
	default:
		return "Bumper"
	}
}

func taterTVBumperPlacementRank(placement string) int {
	switch taterTVNormalizeBumperPlacement(placement) {
	case taterTVBumperBefore:
		return 0
	case taterTVBumperAfter:
		return 1
	case taterTVBumperBoth:
		return 2
	default:
		return 3
	}
}

func taterTVBumperGroupTitle(groupID string) string {
	title := cleanMovieTitleFromName(strings.ReplaceAll(groupID, "-", " "))
	if title == "" {
		return "Bumpers"
	}
	return title
}

func taterTVBumperURL(baseURL, placement, groupID, name, playerToken string) string {
	if strings.TrimSpace(baseURL) == "" {
		return ""
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/api/tater/tv/bumpers/file")
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("placement", taterTVNormalizeBumperPlacement(placement))
	q.Set("group", taterTVCategoryID(groupID, ""))
	q.Set("name", taterTVSafeFileName(name))
	q.Set("player_token", playerToken)
	u.RawQuery = q.Encode()
	return u.String()
}
