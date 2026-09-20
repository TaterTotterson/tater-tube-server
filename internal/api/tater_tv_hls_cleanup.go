package api

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

const (
	taterTVHLSCleanupWait  = 30 * time.Second
	taterTVHLSStaleRootAge = time.Hour
)

// pruneSegmentFiles removes media files that have fallen far enough behind the
// live playlist that no normal client should still request them. Segment
// metadata remains in memory so timestamps and discontinuity sequences stay
// correct across programs, commercials, and bumpers.
func (s *taterTVHLSSession) pruneSegmentFiles() {
	s.mu.Lock()
	cutoff := len(s.segments) - taterTVHLSSegmentFileLimit
	if cutoff <= s.prunedSegments {
		s.mu.Unlock()
		return
	}
	stale := append([]taterTVHLSSegment(nil), s.segments[s.prunedSegments:cutoff]...)
	retainedInitPaths := make(map[string]struct{})
	for _, segment := range s.segments[cutoff:] {
		if segment.InitPath != "" {
			retainedInitPaths[segment.InitPath] = struct{}{}
		}
	}
	s.prunedSegments = cutoff
	s.mu.Unlock()

	for _, segment := range stale {
		removeTaterTVHLSSessionFile(s.root, segment.Path)
		if segment.InitPath != "" {
			if _, retained := retainedInitPaths[segment.InitPath]; !retained {
				removeTaterTVHLSSessionFile(s.root, segment.InitPath)
			}
		}
	}
}

func removeTaterTVHLSSessionFile(root, relPath string) {
	path, err := safeLocalPath(root, relPath)
	if err != nil {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Debug("Unable to prune old Tube TV HLS file", "path", path, "error", err)
	}
}

// cleanupAfterStop waits for the session producer to exit before removing its
// complete working directory. The wait is asynchronous so channel changes and
// server shutdown are never held up by FFmpeg.
func (s *taterTVHLSSession) cleanupAfterStop() {
	root := strings.TrimSpace(s.root)
	if root == "" {
		return
	}
	s.cleanupOnce.Do(func() {
		done := s.runDone
		go func() {
			if done != nil {
				select {
				case <-done:
				case <-time.After(taterTVHLSCleanupWait):
					slog.Warn("Timed out waiting for Tube TV HLS session to stop before cleanup", "root", root)
				}
			}
			if err := removeTaterTVHLSSessionRoot(root); err != nil {
				slog.Warn("Unable to remove stopped Tube TV HLS session", "root", root, "error", err)
			}
		}()
	})
}

func removeTaterTVHLSSessionRoot(root string) error {
	cleanRoot := filepath.Clean(strings.TrimSpace(root))
	if cleanRoot == "." || cleanRoot == string(filepath.Separator) || filepath.Base(filepath.Dir(cleanRoot)) != "tube-tv-hls" {
		return fmt.Errorf("refusing to remove unsafe Tube TV HLS session path %q", root)
	}
	return os.RemoveAll(cleanRoot)
}

// maintainTaterTVHLS retires idle in-memory sessions and removes abandoned
// working directories left behind by an earlier crash or unclean shutdown.
func maintainTaterTVHLS(cfg *config.Config, now time.Time) {
	activeRoots := globalTaterTVHLS.pruneAndActiveRoots()
	removed, err := cleanupStaleTaterTVHLSRoots(taterTVHLSRoot(cfg), activeRoots, now, taterTVHLSStaleRootAge)
	if err != nil {
		slog.Warn("Unable to clean stale Tube TV HLS sessions", "error", err)
		return
	}
	if removed > 0 {
		slog.Info("Cleaned stale Tube TV HLS sessions", "sessions_removed", removed)
	}
}

func (m *taterTVHLSManager) pruneAndActiveRoots() map[string]struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked()
	active := make(map[string]struct{}, len(m.sessions))
	for _, session := range m.sessions {
		if session != nil && strings.TrimSpace(session.root) != "" {
			active[filepath.Clean(session.root)] = struct{}{}
		}
	}
	return active
}

func cleanupStaleTaterTVHLSRoots(root string, activeRoots map[string]struct{}, now time.Time, maxAge time.Duration) (int, error) {
	cleanRoot := filepath.Clean(strings.TrimSpace(root))
	if cleanRoot == "." || cleanRoot == string(filepath.Separator) || filepath.Base(cleanRoot) != "tube-tv-hls" {
		return 0, fmt.Errorf("refusing to clean unsafe Tube TV HLS root %q", root)
	}
	entries, err := os.ReadDir(cleanRoot)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	removed := 0
	for _, entry := range entries {
		path := filepath.Join(cleanRoot, entry.Name())
		if _, active := activeRoots[filepath.Clean(path)]; active {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		if maxAge > 0 && now.Sub(info.ModTime()) < maxAge {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			slog.Warn("Unable to remove stale Tube TV HLS path", "path", path, "error", err)
			continue
		}
		removed++
	}
	return removed, nil
}
