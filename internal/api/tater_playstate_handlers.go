package api

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
)

type taterPlayState struct {
	ID          string    `json:"id"`
	SeriesID    string    `json:"seriesId,omitempty"`
	Title       string    `json:"title"`
	SeriesTitle string    `json:"seriesTitle,omitempty"`
	MediaType   string    `json:"mediaType,omitempty"`
	CategoryID  string    `json:"categoryId,omitempty"`
	SourceIndex int       `json:"sourceIndex,omitempty"`
	Path        string    `json:"path,omitempty"`
	PositionMS  int64     `json:"positionMs,omitempty"`
	DurationMS  int64     `json:"durationMs,omitempty"`
	Completed   bool      `json:"completed,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type taterPlayStateStore struct {
	Items map[string]taterPlayState `json:"items"`
}

var taterPlayStateMu sync.Mutex

func (s *Server) handleTaterPlayStateContinue(c *fiber.Ctx) error {
	cfg, playerToken, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}

	store, err := loadTaterPlayStateStore(cfg)
	if err != nil {
		return RespondServiceUnavailable(c, "Failed to load play state", err.Error())
	}

	collapsed := make(map[string]taterPlayState)
	for _, state := range store.Items {
		displayState, ok := taterContinueDisplayState(cfg, state)
		if !ok {
			continue
		}
		key := displayState.ID
		if strings.TrimSpace(displayState.SeriesID) != "" {
			key = displayState.SeriesID
		}
		if existing, ok := collapsed[key]; !ok || displayState.UpdatedAt.After(existing.UpdatedAt) {
			collapsed[key] = displayState
		}
	}

	states := make([]taterPlayState, 0, len(collapsed))
	for _, state := range collapsed {
		states = append(states, state)
	}
	sort.SliceStable(states, func(i, j int) bool {
		return states[i].UpdatedAt.After(states[j].UpdatedAt)
	})

	rows := make([]taterUsenetItem, 0, len(states))
	for _, state := range states {
		row := taterPlayStateToItem(state, resolveBaseURL(c, ""), playerToken)
		if row.PlayStateID != "" {
			rows = append(rows, row)
		}
	}
	for i := range rows {
		rows[i].Index = i + 1
	}

	return RespondSuccess(c, fiber.Map{"items": rows})
}

func (s *Server) handleTaterPlayStateSave(c *fiber.Ctx) error {
	cfg, _, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}

	var req taterPlayState
	if err := c.BodyParser(&req); err != nil {
		return RespondValidationError(c, "Invalid play state", err.Error())
	}

	req.CategoryID = "local:" + taterRawLocalCategoryID(req.CategoryID)
	req.Path = cleanLocalRelativePath(req.Path)
	req.MediaType = strings.TrimSpace(req.MediaType)
	req.Title = cleanTaterText(req.Title)
	req.SeriesTitle = cleanTaterText(req.SeriesTitle)
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" {
		req.ID = taterLocalPlayStateID(req.CategoryID, req.SourceIndex, req.Path)
	}
	if req.ID == "" || req.Path == "" {
		return RespondValidationError(c, "Invalid play state", "playStateId or local path is required")
	}
	if req.SeriesID == "" && strings.EqualFold(req.MediaType, "episode") {
		req.SeriesID = taterLocalSeriesStateID(req.CategoryID, req.SourceIndex, req.Path)
	}
	if req.SeriesTitle == "" && strings.EqualFold(req.MediaType, "episode") {
		req.SeriesTitle = taterSeriesTitleFromPath(req.Path)
	}
	if strings.EqualFold(req.MediaType, "episode") && req.SeriesID != "" {
		req.ID = req.SeriesID
	}
	if req.PositionMS < 0 {
		req.PositionMS = 0
	}
	if req.DurationMS < 0 {
		req.DurationMS = 0
	}
	if req.Title == "" {
		req.Title = cleanMovieTitleFromName(strings.TrimSuffix(filepath.Base(req.Path), filepath.Ext(req.Path)))
	}
	req.UpdatedAt = time.Now().UTC()
	if taterPlayStateCompleted(req) {
		req.Completed = true
	}

	store, err := loadTaterPlayStateStore(cfg)
	if err != nil {
		return RespondServiceUnavailable(c, "Failed to load play state", err.Error())
	}
	store.Items[req.ID] = req
	if err := saveTaterPlayStateStore(cfg, store); err != nil {
		return RespondServiceUnavailable(c, "Failed to save play state", err.Error())
	}

	return RespondSuccess(c, fiber.Map{"saved": true})
}

func (s *Server) handleTaterPlayStateNext(c *fiber.Ctx) error {
	cfg, playerToken, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}

	var current taterPlayState
	if err := c.BodyParser(&current); err != nil {
		return RespondValidationError(c, "Invalid current episode", err.Error())
	}
	current.CategoryID = "local:" + taterRawLocalCategoryID(current.CategoryID)
	current.Path = cleanLocalRelativePath(current.Path)
	current.MediaType = strings.TrimSpace(current.MediaType)
	current.SeriesTitle = cleanTaterText(current.SeriesTitle)
	if !strings.EqualFold(current.MediaType, "episode") || current.Path == "" {
		return RespondSuccess(c, fiber.Map{"item": nil})
	}

	next, found := taterNextEpisodePlayState(cfg, current)
	if !found {
		return RespondSuccess(c, fiber.Map{"item": nil})
	}
	return RespondSuccess(c, fiber.Map{
		"item": taterPlayStateToItem(next, resolveBaseURL(c, ""), playerToken),
	})
}

func taterAttachLocalPlayStates(cfg *config.Config, items []taterUsenetItem) []taterUsenetItem {
	if len(items) == 0 {
		return items
	}
	store, err := loadTaterPlayStateStore(cfg)
	if err != nil {
		store = taterPlayStateStore{Items: map[string]taterPlayState{}}
	}
	for i := range items {
		if items[i].Type != "localFile" {
			continue
		}
		taterSetLocalPlayStateIDs(&items[i])
		state, ok := store.Items[items[i].PlayStateID]
		if !ok && items[i].SeriesStateID != "" {
			state, ok = store.Items[items[i].SeriesStateID]
			if ok && cleanLocalRelativePath(state.Path) != cleanLocalRelativePath(items[i].Path) {
				ok = false
			}
		}
		if !ok || !taterShouldContinue(state) {
			continue
		}
		taterApplyPlayStateToItem(&items[i], state)
	}
	return items
}

func taterSetLocalPlayStateIDs(item *taterUsenetItem) {
	if item == nil {
		return
	}
	item.CategoryID = "local:" + taterRawLocalCategoryID(item.CategoryID)
	item.PlayStateID = taterLocalPlayStateID(item.CategoryID, item.SourceIndex, item.Path)
	if strings.EqualFold(item.MediaType, "episode") {
		item.SeriesStateID = taterLocalSeriesStateID(item.CategoryID, item.SourceIndex, item.Path)
	}
}

func taterApplyPlayStateToItem(item *taterUsenetItem, state taterPlayState) {
	if item == nil {
		return
	}
	item.ViewOffset = state.PositionMS
	item.ViewOffsetSec = float64(state.PositionMS) / 1000.0
	if state.DurationMS > 0 && item.DurationSeconds <= 0 {
		attachTaterDuration(item, float64(state.DurationMS)/1000.0)
	}
	if state.DurationMS > 0 {
		item.ProgressPercent = math.Round((float64(state.PositionMS)/float64(state.DurationMS))*1000) / 10
	}
}

func taterPlayStateToItem(state taterPlayState, baseURL, playerToken string) taterUsenetItem {
	categoryID := "local:" + taterRawLocalCategoryID(state.CategoryID)
	row := taterUsenetItem{
		Title:         state.Title,
		Key:           state.ID,
		RatingKey:     state.ID,
		Type:          "localFile",
		MediaType:     state.MediaType,
		CategoryID:    categoryID,
		SourceIndex:   state.SourceIndex,
		Path:          state.Path,
		StreamURL:     taterLocalStreamURL(baseURL, taterRawLocalCategoryID(categoryID), state.SourceIndex, state.Path, playerToken),
		SeekMode:      "client",
		PlayStateID:   state.ID,
		SeriesStateID: state.SeriesID,
		ViewOffset:    state.PositionMS,
		ViewOffsetSec: float64(state.PositionMS) / 1000.0,
	}
	if row.Title == "" {
		row.Title = cleanMovieTitleFromName(strings.TrimSuffix(filepath.Base(row.Path), filepath.Ext(row.Path)))
	}
	if state.DurationMS > 0 {
		attachTaterDuration(&row, float64(state.DurationMS)/1000.0)
	}
	if state.SeriesTitle != "" {
		row.SizeText = state.SeriesTitle
	} else if row.DurationDisplay != "" {
		row.SizeText = "RESUME " + row.DurationDisplay
	} else {
		row.SizeText = "RESUME"
	}
	taterApplyPlayStateToItem(&row, state)
	return row
}

func taterContinueDisplayState(cfg *config.Config, state taterPlayState) (taterPlayState, bool) {
	if strings.EqualFold(state.MediaType, "episode") {
		if state.Completed || taterPlayStateCompleted(state) {
			return taterNextEpisodePlayState(cfg, state)
		}
		if !taterShouldContinue(state) {
			return taterPlayState{}, false
		}
		return state, true
	}
	if !taterShouldContinue(state) {
		return taterPlayState{}, false
	}
	return state, true
}

func taterShouldContinue(state taterPlayState) bool {
	if state.Completed || strings.TrimSpace(state.ID) == "" || strings.TrimSpace(state.Path) == "" {
		return false
	}
	return !taterPlayStateCompleted(state)
}

func taterPlayStateCompleted(state taterPlayState) bool {
	if state.Completed {
		return true
	}
	if state.DurationMS <= 0 {
		return false
	}
	remaining := state.DurationMS - state.PositionMS
	threshold := int64(30_000)
	if state.DurationMS < 300_000 {
		threshold = 10_000
	}
	return remaining <= threshold
}

func taterPlayStateStorePath(cfg *config.Config) string {
	dir := "."
	if cfg != nil {
		if strings.TrimSpace(cfg.Database.Path) != "" {
			dir = filepath.Dir(cfg.Database.Path)
		} else if strings.TrimSpace(cfg.Metadata.RootPath) != "" {
			dir = filepath.Dir(cfg.Metadata.RootPath)
		}
	}
	return filepath.Join(dir, "tater-play-state.json")
}

func taterNextEpisodePlayState(cfg *config.Config, state taterPlayState) (taterPlayState, bool) {
	if cfg == nil {
		return taterPlayState{}, false
	}
	cat, ok := taterLocalMediaCategory(cfg, taterRawLocalCategoryID(state.CategoryID))
	if !ok || !strings.EqualFold(strings.TrimSpace(cat.LibraryType), "tv") {
		return taterPlayState{}, false
	}
	paths := taterLocalMediaCategoryPaths(cat)
	if state.SourceIndex < 0 || state.SourceIndex >= len(paths) {
		return taterPlayState{}, false
	}
	showPath := taterSeriesKeyFromPath(state.Path)
	if showPath == "" {
		return taterPlayState{}, false
	}
	episodes, err := taterLocalSeriesEpisodeItems(cfg, cat, paths[state.SourceIndex], state.SourceIndex, showPath, "", "")
	if err != nil || len(episodes) == 0 {
		return taterPlayState{}, false
	}

	currentPath := cleanLocalRelativePath(state.Path)
	for i, item := range episodes {
		if cleanLocalRelativePath(item.Path) != currentPath {
			continue
		}
		if i+1 >= len(episodes) {
			return taterPlayState{}, false
		}
		next := episodes[i+1]
		seriesID := state.SeriesID
		if seriesID == "" {
			seriesID = taterLocalSeriesStateID(next.CategoryID, next.SourceIndex, next.Path)
		}
		seriesTitle := state.SeriesTitle
		if seriesTitle == "" {
			seriesTitle = taterSeriesTitleFromPath(next.Path)
		}
		return taterPlayState{
			ID:          seriesID,
			SeriesID:    seriesID,
			Title:       next.Title,
			SeriesTitle: seriesTitle,
			MediaType:   "episode",
			CategoryID:  next.CategoryID,
			SourceIndex: next.SourceIndex,
			Path:        next.Path,
			PositionMS:  0,
			DurationMS:  int64(math.Round(next.DurationSeconds * 1000)),
			Completed:   false,
			UpdatedAt:   state.UpdatedAt,
		}, true
	}
	return taterPlayState{}, false
}

func taterLocalSeriesEpisodeItems(cfg *config.Config, cat config.LocalMediaCategory, root string, sourceIndex int, showPath, baseURL, playerToken string) ([]taterUsenetItem, error) {
	showPath = cleanLocalRelativePath(showPath)
	if showPath == "" {
		return nil, fmt.Errorf("series path is required")
	}
	showAbs, err := safeLocalPath(root, showPath)
	if err != nil {
		return nil, err
	}

	items := []taterUsenetItem{}
	err = filepath.WalkDir(showAbs, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			if entry.IsDir() {
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
			Path:        rel,
			StreamURL:   taterLocalStreamURL(baseURL, cat.ID, sourceIndex, rel, playerToken),
			SeekMode:    taterLocalSeekMode(cfg, filepath.Ext(name)),
			SizeBytes:   size,
			SizeText:    "EPISODE",
		}
		attachTaterLocalDuration(cfg, path, &item)
		items = append(items, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool {
		return taterEpisodeSortKey(items[i].Path) < taterEpisodeSortKey(items[j].Path)
	})
	return items, nil
}

func taterEpisodeSortKey(relPath string) string {
	clean := filepath.ToSlash(cleanLocalRelativePath(relPath))
	search := localSeparatorPattern.ReplaceAllString(clean, " ")
	search = strings.ReplaceAll(search, "-", " ")
	if match := localEpisodePattern.FindStringSubmatch(search); len(match) >= 3 {
		season, _ := strconv.Atoi(match[1])
		episode, _ := strconv.Atoi(match[2])
		return fmt.Sprintf("%04d-%04d-%s", season, episode, strings.ToLower(clean))
	}
	return strings.ToLower(clean)
}

func loadTaterPlayStateStore(cfg *config.Config) (taterPlayStateStore, error) {
	taterPlayStateMu.Lock()
	defer taterPlayStateMu.Unlock()
	return loadTaterPlayStateStoreUnlocked(cfg)
}

func loadTaterPlayStateStoreUnlocked(cfg *config.Config) (taterPlayStateStore, error) {
	store := taterPlayStateStore{Items: map[string]taterPlayState{}}
	data, err := os.ReadFile(taterPlayStateStorePath(cfg))
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return store, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return store, nil
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return taterPlayStateStore{Items: map[string]taterPlayState{}}, err
	}
	if store.Items == nil {
		store.Items = map[string]taterPlayState{}
	}
	return store, nil
}

func saveTaterPlayStateStore(cfg *config.Config, store taterPlayStateStore) error {
	taterPlayStateMu.Lock()
	defer taterPlayStateMu.Unlock()

	path := taterPlayStateStorePath(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func taterLocalPlayStateID(categoryID string, sourceIndex int, relPath string) string {
	categoryID = taterRawLocalCategoryID(categoryID)
	relPath = cleanLocalRelativePath(relPath)
	if categoryID == "" || relPath == "" {
		return ""
	}
	return "local:" + taterHashParts(categoryID, strconv.Itoa(sourceIndex), relPath)
}

func taterLocalSeriesStateID(categoryID string, sourceIndex int, relPath string) string {
	categoryID = taterRawLocalCategoryID(categoryID)
	showPath := taterSeriesKeyFromPath(relPath)
	if categoryID == "" || showPath == "" {
		return ""
	}
	return "local-series:" + taterHashParts(categoryID, strconv.Itoa(sourceIndex), showPath)
}

func taterSeriesKeyFromPath(relPath string) string {
	parts := strings.Split(cleanLocalRelativePath(relPath), "/")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func taterSeriesTitleFromPath(relPath string) string {
	return cleanShowTitle(taterSeriesKeyFromPath(relPath))
}

func taterRawLocalCategoryID(categoryID string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(categoryID), "local:"))
}

func taterHashParts(parts ...string) string {
	h := sha1.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
