package api

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/fsnotify/fsnotify"
)

const (
	taterLocalLibraryMonitorDebounce = 5 * time.Second
	taterLocalLibraryMonitorRetry    = time.Minute
)

type taterLocalLibraryMonitorPathStatus struct {
	CategoryID   string `json:"category_id"`
	CategoryName string `json:"category_name"`
	Path         string `json:"path"`
	State        string `json:"state"`
	Message      string `json:"message,omitempty"`
}

type taterLocalLibraryMonitorStatus struct {
	Enabled             bool                                 `json:"enabled"`
	Active              bool                                 `json:"active"`
	State               string                               `json:"state"`
	Message             string                               `json:"message,omitempty"`
	WatchedDirectories  int                                  `json:"watched_directories"`
	Paths               []taterLocalLibraryMonitorPathStatus `json:"paths"`
	LastEventAt         *time.Time                           `json:"last_event_at,omitempty"`
	LastScanRequestedAt *time.Time                           `json:"last_scan_requested_at,omitempty"`
}

type taterLocalLibraryMonitorRoot struct {
	CategoryID string
	Path       string
}

func taterLocalLibraryRealtimeMonitoringEnabled(cfg *config.Config) bool {
	return taterLocalMediaEnabled(cfg) &&
		(cfg.LocalMedia.RealtimeMonitoringEnabled == nil || *cfg.LocalMedia.RealtimeMonitoringEnabled)
}

func (s *Server) setTaterLocalLibraryMonitorStatus(status taterLocalLibraryMonitorStatus) {
	if status.Paths == nil {
		status.Paths = []taterLocalLibraryMonitorPathStatus{}
	}
	s.localLibraryMonitorMu.Lock()
	previous := s.localLibraryMonitorStatus
	status.LastEventAt = previous.LastEventAt
	status.LastScanRequestedAt = previous.LastScanRequestedAt
	s.localLibraryMonitorStatus = status
	s.localLibraryMonitorMu.Unlock()
}

func (s *Server) getTaterLocalLibraryMonitorStatus() taterLocalLibraryMonitorStatus {
	s.localLibraryMonitorMu.RLock()
	defer s.localLibraryMonitorMu.RUnlock()
	status := s.localLibraryMonitorStatus
	status.Paths = append([]taterLocalLibraryMonitorPathStatus(nil), status.Paths...)
	return status
}

func (s *Server) noteTaterLocalLibraryMonitorEvent() {
	now := time.Now().UTC()
	s.localLibraryMonitorMu.Lock()
	s.localLibraryMonitorStatus.LastEventAt = &now
	s.localLibraryMonitorMu.Unlock()
}

func (s *Server) queueTaterLocalLibraryRealtimeScan(categoryIDs []string) {
	s.localLibraryRealtimeMu.Lock()
	for _, categoryID := range categoryIDs {
		if categoryID = strings.TrimSpace(categoryID); categoryID != "" {
			s.localLibraryRealtimeCategories[categoryID] = struct{}{}
		}
	}
	s.localLibraryRealtimeMu.Unlock()

	now := time.Now().UTC()
	s.localLibraryMonitorMu.Lock()
	s.localLibraryMonitorStatus.LastScanRequestedAt = &now
	s.localLibraryMonitorMu.Unlock()

	select {
	case s.localLibraryRealtimeScanWake <- struct{}{}:
	default:
	}
}

func (s *Server) takeTaterLocalLibraryRealtimeCategories() []string {
	s.localLibraryRealtimeMu.Lock()
	defer s.localLibraryRealtimeMu.Unlock()
	categoryIDs := make([]string, 0, len(s.localLibraryRealtimeCategories))
	for categoryID := range s.localLibraryRealtimeCategories {
		categoryIDs = append(categoryIDs, categoryID)
	}
	s.localLibraryRealtimeCategories = map[string]struct{}{}
	sort.Strings(categoryIDs)
	return categoryIDs
}

func (s *Server) restoreTaterLocalLibraryRealtimeCategories(categoryIDs []string) {
	s.localLibraryRealtimeMu.Lock()
	defer s.localLibraryRealtimeMu.Unlock()
	for _, categoryID := range categoryIDs {
		if categoryID = strings.TrimSpace(categoryID); categoryID != "" {
			s.localLibraryRealtimeCategories[categoryID] = struct{}{}
		}
	}
}

func taterLocalLibraryPathWithinRoot(path, root string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func taterLocalLibraryHiddenPath(path, root string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || relative == "." {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(relative), "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

func addTaterLocalLibraryWatchTree(
	watcher *fsnotify.Watcher,
	root string,
	watched map[string]struct{},
) (int, bool, error) {
	root = filepath.Clean(root)
	added := 0
	rootWatched := false
	var firstErr error
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			if firstErr == nil {
				firstErr = walkErr
			}
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if path != root && strings.HasPrefix(entry.Name(), ".") {
			return filepath.SkipDir
		}
		path = filepath.Clean(path)
		if _, exists := watched[path]; exists {
			if path == root {
				rootWatched = true
			}
			return nil
		}
		if err := watcher.Add(path); err != nil {
			if path == root {
				return err
			}
			if firstErr == nil {
				firstErr = err
			}
			return filepath.SkipDir
		}
		watched[path] = struct{}{}
		added++
		if path == root {
			rootWatched = true
		}
		return nil
	})
	if err != nil {
		return added, rootWatched, err
	}
	return added, rootWatched, firstErr
}

func configureTaterLocalLibraryWatcher(
	cfg *config.Config,
) (*fsnotify.Watcher, []taterLocalLibraryMonitorRoot, map[string]struct{}, taterLocalLibraryMonitorStatus, bool) {
	status := taterLocalLibraryMonitorStatus{
		Enabled: taterLocalLibraryRealtimeMonitoringEnabled(cfg),
		State:   "disabled",
		Paths:   []taterLocalLibraryMonitorPathStatus{},
	}
	roots := []taterLocalLibraryMonitorRoot{}
	if cfg == nil {
		status.State = "unavailable"
		status.Message = "Configuration is unavailable"
		return nil, roots, map[string]struct{}{}, status, true
	}

	periodicEnabled := taterLocalLibraryAutoScanEnabled(cfg)
	for _, category := range cfg.LocalMedia.Categories {
		categoryEnabled := taterLocalLibraryEnabled(category)
		for _, configuredRoot := range taterLocalMediaCategoryPaths(category) {
			root := filepath.Clean(configuredRoot)
			pathStatus := taterLocalLibraryMonitorPathStatus{
				CategoryID: strings.TrimSpace(category.ID), CategoryName: strings.TrimSpace(category.Name),
				Path: root, State: "disabled",
			}
			if !categoryEnabled || !taterLocalMediaEnabled(cfg) {
				pathStatus.Message = "Library is disabled"
			} else if !status.Enabled {
				if periodicEnabled {
					pathStatus.State = "periodic_only"
					pathStatus.Message = "Using the scheduled safety scan"
				} else {
					pathStatus.Message = "Automatic updates are disabled"
				}
			} else {
				roots = append(roots, taterLocalLibraryMonitorRoot{CategoryID: pathStatus.CategoryID, Path: root})
			}
			status.Paths = append(status.Paths, pathStatus)
		}
	}

	if !status.Enabled {
		status.Active = false
		if periodicEnabled {
			status.State = "periodic_only"
			status.Message = "Real-time monitoring is off; scheduled scans remain active"
		} else {
			status.Message = "Automatic library updates are disabled"
		}
		return nil, roots, map[string]struct{}{}, status, false
	}
	if len(roots) == 0 {
		status.State = "unavailable"
		status.Message = "Add and enable a local library folder to start monitoring"
		return nil, roots, map[string]struct{}{}, status, false
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		status.State = "unavailable"
		status.Message = "Filesystem monitoring could not start"
		if periodicEnabled {
			status.State = "periodic_only"
			status.Message = "Filesystem monitoring could not start; scheduled scans remain active"
		}
		for i := range status.Paths {
			if status.Paths[i].State == "disabled" {
				status.Paths[i].State = status.State
				status.Paths[i].Message = err.Error()
			}
		}
		return nil, roots, map[string]struct{}{}, status, true
	}

	watched := map[string]struct{}{}
	watchingPaths := 0
	needsRetry := false
	for i := range status.Paths {
		pathStatus := &status.Paths[i]
		eligible := false
		for _, root := range roots {
			if root.CategoryID == pathStatus.CategoryID && root.Path == pathStatus.Path {
				eligible = true
				break
			}
		}
		if !eligible {
			continue
		}
		info, statErr := os.Stat(pathStatus.Path)
		if statErr != nil || info == nil || !info.IsDir() {
			pathStatus.State = "unavailable"
			pathStatus.Message = "Folder is not currently available"
			needsRetry = true
			continue
		}
		added, rootWatched, watchErr := addTaterLocalLibraryWatchTree(watcher, pathStatus.Path, watched)
		status.WatchedDirectories += added
		if rootWatched {
			pathStatus.State = "watching"
			watchingPaths++
			if watchErr != nil {
				pathStatus.Message = "Some subfolders could not be monitored; scheduled scans cover missed changes"
				needsRetry = true
			}
			continue
		}
		pathStatus.State = "unavailable"
		pathStatus.Message = "Filesystem events are unavailable"
		if periodicEnabled {
			pathStatus.State = "periodic_only"
			pathStatus.Message = "Filesystem events are unavailable; using scheduled scans"
		}
		if watchErr != nil {
			pathStatus.Message = fmt.Sprintf("Filesystem events are unavailable: %v", watchErr)
		}
		needsRetry = true
	}

	status.Active = watchingPaths > 0
	if status.Active {
		status.State = "watching"
		if watchingPaths == len(roots) {
			status.Message = "Changes are monitored in real time"
		} else {
			status.Message = "Watching available folders; scheduled scans cover the rest"
		}
	} else if periodicEnabled {
		status.State = "periodic_only"
		status.Message = "Using scheduled scans because filesystem events are unavailable"
	} else {
		status.State = "unavailable"
		status.Message = "Filesystem monitoring is unavailable and scheduled scans are disabled"
	}
	return watcher, roots, watched, status, needsRetry
}

func taterLocalLibraryEventCategories(eventPath string, roots []taterLocalLibraryMonitorRoot) []string {
	categorySet := map[string]struct{}{}
	for _, root := range roots {
		if taterLocalLibraryPathWithinRoot(eventPath, root.Path) &&
			!taterLocalLibraryHiddenPath(eventPath, root.Path) {
			categorySet[root.CategoryID] = struct{}{}
		}
	}
	categoryIDs := make([]string, 0, len(categorySet))
	for categoryID := range categorySet {
		categoryIDs = append(categoryIDs, categoryID)
	}
	sort.Strings(categoryIDs)
	return categoryIDs
}

func (s *Server) runLocalLibraryMonitor(ctx context.Context) {
	var watcher *fsnotify.Watcher
	var events <-chan fsnotify.Event
	var watcherErrors <-chan error
	var roots []taterLocalLibraryMonitorRoot
	var watched map[string]struct{}

	debounceTimer := time.NewTimer(time.Hour)
	if !debounceTimer.Stop() {
		<-debounceTimer.C
	}
	defer debounceTimer.Stop()
	var debounce <-chan time.Time
	pendingCategories := map[string]struct{}{}

	retryTimer := time.NewTimer(time.Hour)
	if !retryTimer.Stop() {
		<-retryTimer.C
	}
	defer retryTimer.Stop()
	var retry <-chan time.Time

	closeWatcher := func() {
		if watcher != nil {
			_ = watcher.Close()
		}
		watcher = nil
		events = nil
		watcherErrors = nil
	}
	defer closeWatcher()

	resetRetry := func(enabled bool) {
		if !retryTimer.Stop() {
			select {
			case <-retryTimer.C:
			default:
			}
		}
		retry = nil
		if enabled {
			retryTimer.Reset(taterLocalLibraryMonitorRetry)
			retry = retryTimer.C
		}
	}

	rebuild := func() {
		closeWatcher()
		cfg := s.configManager.GetConfig()
		var status taterLocalLibraryMonitorStatus
		var needsRetry bool
		watcher, roots, watched, status, needsRetry = configureTaterLocalLibraryWatcher(cfg)
		if watcher != nil {
			events = watcher.Events
			watcherErrors = watcher.Errors
		}
		s.setTaterLocalLibraryMonitorStatus(status)
		resetRetry(needsRetry)
		if status.Active {
			slog.InfoContext(ctx, "Local media real-time monitoring started",
				"paths", len(roots), "directories", status.WatchedDirectories)
		}
	}

	rebuild()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.localLibraryMonitorWake:
			pendingCategories = map[string]struct{}{}
			debounce = nil
			if !debounceTimer.Stop() {
				select {
				case <-debounceTimer.C:
				default:
				}
			}
			rebuild()
		case <-retry:
			rebuild()
		case event, ok := <-events:
			if !ok {
				closeWatcher()
				resetRetry(true)
				continue
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			categoryIDs := taterLocalLibraryEventCategories(event.Name, roots)
			if len(categoryIDs) == 0 {
				continue
			}
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					added, _, addErr := addTaterLocalLibraryWatchTree(watcher, event.Name, watched)
					if added > 0 {
						s.localLibraryMonitorMu.Lock()
						s.localLibraryMonitorStatus.WatchedDirectories += added
						s.localLibraryMonitorMu.Unlock()
					}
					if addErr != nil {
						resetRetry(true)
					}
				}
			}
			if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				for _, root := range roots {
					if filepath.Clean(event.Name) == filepath.Clean(root.Path) {
						resetRetry(true)
						break
					}
				}
			}
			for _, categoryID := range categoryIDs {
				pendingCategories[categoryID] = struct{}{}
			}
			s.noteTaterLocalLibraryMonitorEvent()
			if !debounceTimer.Stop() {
				select {
				case <-debounceTimer.C:
				default:
				}
			}
			debounceTimer.Reset(taterLocalLibraryMonitorDebounce)
			debounce = debounceTimer.C
		case <-debounce:
			categoryIDs := make([]string, 0, len(pendingCategories))
			for categoryID := range pendingCategories {
				categoryIDs = append(categoryIDs, categoryID)
			}
			sort.Strings(categoryIDs)
			pendingCategories = map[string]struct{}{}
			debounce = nil
			if len(categoryIDs) > 0 {
				s.queueTaterLocalLibraryRealtimeScan(categoryIDs)
			}
		case err, ok := <-watcherErrors:
			if !ok {
				closeWatcher()
				resetRetry(true)
				continue
			}
			slog.WarnContext(ctx, "Local media filesystem watcher reported an error", "error", err)
			s.localLibraryMonitorMu.Lock()
			s.localLibraryMonitorStatus.Message = "A filesystem watcher reported an error; scheduled scans remain active"
			s.localLibraryMonitorMu.Unlock()
			resetRetry(true)
		}
	}
}
