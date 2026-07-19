package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

type taterNzbWatchAgainEntry struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	NzbURL       string    `json:"nzbUrl"`
	Category     string    `json:"category,omitempty"`
	LastPlayedAt time.Time `json:"lastPlayedAt"`
	PlayCount    int       `json:"playCount,omitempty"`
}

type taterNzbWatchAgainStore struct {
	Items map[string]taterNzbWatchAgainEntry `json:"items"`
}

var taterNzbWatchAgainMu sync.Mutex

func taterRecordNzbWatchAgain(cfg *config.Config, title, nzbURL, category string) error {
	title = cleanTaterText(title)
	nzbURL = taterStripNewznabAuth(nzbURL)
	category = cleanTaterText(category)
	if title == "" {
		title = strings.TrimSuffix(stableTaterNzbFilename("", nzbURL), ".nzb")
	}
	if nzbURL == "" {
		return fmt.Errorf("nzb url is required")
	}

	taterNzbWatchAgainMu.Lock()
	defer taterNzbWatchAgainMu.Unlock()

	store, err := loadTaterNzbWatchAgainStoreUnlocked(cfg)
	if err != nil {
		return err
	}
	id := taterNzbWatchAgainID(title, nzbURL)
	entry := store.Items[id]
	if entry.PlayCount < 0 {
		entry.PlayCount = 0
	}
	entry.ID = id
	entry.Title = title
	entry.NzbURL = nzbURL
	entry.Category = category
	entry.LastPlayedAt = time.Now().UTC()
	entry.PlayCount++
	store.Items[id] = entry

	store = trimTaterNzbWatchAgainStore(cfg, store)
	return saveTaterNzbWatchAgainStoreUnlocked(cfg, store)
}

func taterNzbWatchAgainRows(cfg *config.Config) ([]taterUsenetItem, error) {
	taterNzbWatchAgainMu.Lock()
	defer taterNzbWatchAgainMu.Unlock()

	store, err := loadTaterNzbWatchAgainStoreUnlocked(cfg)
	if err != nil {
		return nil, err
	}
	trimmed := trimTaterNzbWatchAgainStore(cfg, store)
	if len(trimmed.Items) != len(store.Items) {
		_ = saveTaterNzbWatchAgainStoreUnlocked(cfg, trimmed)
	}

	entries := make([]taterNzbWatchAgainEntry, 0, len(trimmed.Items))
	for _, entry := range trimmed.Items {
		if strings.TrimSpace(entry.NzbURL) == "" {
			continue
		}
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].LastPlayedAt.After(entries[j].LastPlayedAt)
	})

	rows := make([]taterUsenetItem, 0, len(entries))
	for i, entry := range entries {
		row := taterUsenetItem{
			Title:      entry.Title,
			Key:        entry.ID,
			RatingKey:  entry.ID,
			NzbURL:     entry.NzbURL,
			Type:       "watchAgain",
			MediaType:  taterNzbMediaType("", entry.Category),
			Category:   entry.Category,
			CategoryID: "watch-again",
			Index:      i + 1,
			Date:       entry.LastPlayedAt.Format(time.RFC3339),
			SizeText:   taterWatchAgainDetail(entry),
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func taterWatchAgainDetail(entry taterNzbWatchAgainEntry) string {
	parts := []string{"WATCH AGAIN"}
	if entry.PlayCount > 1 {
		parts = append(parts, fmt.Sprintf("%dx", entry.PlayCount))
	}
	if category := cleanTaterText(entry.Category); category != "" {
		parts = append(parts, strings.ToUpper(category))
	}
	return strings.Join(parts, " / ")
}

func trimTaterNzbWatchAgainStore(cfg *config.Config, store taterNzbWatchAgainStore) taterNzbWatchAgainStore {
	if store.Items == nil {
		store.Items = map[string]taterNzbWatchAgainEntry{}
	}
	limit := taterNzbWatchAgainLimit(cfg)
	retentionDays := taterNzbWatchAgainRetentionDays(cfg)
	cutoff := time.Time{}
	if retentionDays > 0 {
		cutoff = time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	}

	entries := make([]taterNzbWatchAgainEntry, 0, len(store.Items))
	for _, entry := range store.Items {
		if strings.TrimSpace(entry.ID) == "" || strings.TrimSpace(entry.NzbURL) == "" {
			continue
		}
		if !cutoff.IsZero() && entry.LastPlayedAt.Before(cutoff) {
			continue
		}
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].LastPlayedAt.After(entries[j].LastPlayedAt)
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}

	trimmed := taterNzbWatchAgainStore{Items: map[string]taterNzbWatchAgainEntry{}}
	for _, entry := range entries {
		trimmed.Items[entry.ID] = entry
	}
	return trimmed
}

func loadTaterNzbWatchAgainStoreUnlocked(cfg *config.Config) (taterNzbWatchAgainStore, error) {
	store := taterNzbWatchAgainStore{Items: map[string]taterNzbWatchAgainEntry{}}
	data, err := os.ReadFile(taterNzbWatchAgainStorePath(cfg))
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
		return taterNzbWatchAgainStore{Items: map[string]taterNzbWatchAgainEntry{}}, err
	}
	if store.Items == nil {
		store.Items = map[string]taterNzbWatchAgainEntry{}
	}
	return store, nil
}

func saveTaterNzbWatchAgainStoreUnlocked(cfg *config.Config, store taterNzbWatchAgainStore) error {
	path := taterNzbWatchAgainStorePath(cfg)
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

func taterNzbWatchAgainStorePath(cfg *config.Config) string {
	dir := "."
	if cfg != nil {
		if strings.TrimSpace(cfg.Database.Path) != "" {
			dir = filepath.Dir(cfg.Database.Path)
		} else if strings.TrimSpace(cfg.Metadata.RootPath) != "" {
			dir = filepath.Dir(cfg.Metadata.RootPath)
		}
	}
	return filepath.Join(dir, "tater-watch-again.json")
}

func taterNzbWatchAgainID(title, nzbURL string) string {
	return "nzb:" + taterHashParts(cleanTaterText(title), taterStripNewznabAuth(nzbURL))
}

func taterNzbWatchAgainLimit(cfg *config.Config) int {
	limit := 50
	if cfg != nil && cfg.Newznab.WatchAgainLimit > 0 {
		limit = cfg.Newznab.WatchAgainLimit
	}
	if limit > 500 {
		return 500
	}
	if limit < 1 {
		return 1
	}
	return limit
}

func taterNzbWatchAgainRetentionDays(cfg *config.Config) int {
	days := 30
	if cfg != nil {
		days = cfg.Newznab.WatchAgainRetentionDays
	}
	if days < 0 {
		return 30
	}
	if days > 3650 {
		return 3650
	}
	return days
}
