package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

const (
	taterLocalLibraryAutoScanStartupDelay = 30 * time.Second
	taterLocalLibraryAutoScanRetryDelay   = time.Minute
	taterLocalLibraryRealtimeRetryDelay   = 5 * time.Second
	taterLocalLibraryScannerIdleDelay     = 24 * time.Hour
)

func taterLocalLibraryAutoScanEnabled(cfg *config.Config) bool {
	return taterLocalMediaEnabled(cfg) &&
		(cfg.LocalMedia.AutoScanEnabled == nil || *cfg.LocalMedia.AutoScanEnabled)
}

func taterLocalLibraryAutoScanInterval(cfg *config.Config) time.Duration {
	minutes := 15
	if cfg != nil && cfg.LocalMedia.AutoScanIntervalMinutes > 0 {
		minutes = cfg.LocalMedia.AutoScanIntervalMinutes
	}
	if minutes < 5 {
		minutes = 5
	}
	if minutes > 1440 {
		minutes = 1440
	}
	return time.Duration(minutes) * time.Minute
}

func taterLocalLibraryFilesChanged(before, after taterLocalLibraryIndex) bool {
	if len(before.Files) != len(after.Files) {
		return true
	}
	for i := range before.Files {
		left := before.Files[i]
		right := after.Files[i]
		if left.Key != right.Key || left.SizeBytes != right.SizeBytes ||
			left.ModifiedUnixNano != right.ModifiedUnixNano {
			return true
		}
	}
	return false
}

func taterLocalLibraryNextScheduledDelay(cfg *config.Config) time.Duration {
	if taterLocalLibraryAutoScanEnabled(cfg) {
		return taterLocalLibraryAutoScanInterval(cfg)
	}
	return taterLocalLibraryScannerIdleDelay
}

// runLocalLibraryScanner owns automatic local-library discovery. It is
// intentionally independent of Tube TV: players and Tube TV both consume the
// resulting index, but neither feature is responsible for keeping it fresh.
func (s *Server) runLocalLibraryScanner(ctx context.Context) {
	timer := time.NewTimer(taterLocalLibraryAutoScanStartupDelay)
	defer timer.Stop()
	pendingRealtime := false
	initialCheck := true

	for {
		configurationCheck := false
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			configurationCheck = initialCheck
			initialCheck = false
		case <-s.localLibraryScanWake:
			configurationCheck = true
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-s.localLibraryRealtimeScanWake:
			pendingRealtime = true
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}

		delay := taterLocalLibraryAutoScanRetryDelay
		cfg := s.configManager.GetConfig()
		realtimeScan := pendingRealtime && taterLocalLibraryRealtimeMonitoringEnabled(cfg)
		if pendingRealtime && !realtimeScan {
			_ = s.takeTaterLocalLibraryRealtimeCategories()
			pendingRealtime = false
		}
		if !realtimeScan && !taterLocalLibraryAutoScanEnabled(cfg) &&
			!(configurationCheck && taterLocalLibraryRealtimeMonitoringEnabled(cfg)) {
			timer.Reset(taterLocalLibraryNextScheduledDelay(cfg))
			continue
		}

		interval := taterLocalLibraryAutoScanInterval(cfg)
		previous, previousErr := readTaterLocalLibraryIndex(cfg)
		if !realtimeScan && previousErr == nil && previous.ConfigFingerprint == taterLocalLibraryFingerprint(cfg) {
			age := time.Since(previous.GeneratedAt)
			if age >= 0 && age < interval {
				if taterLocalLibraryAutoScanEnabled(cfg) {
					timer.Reset(interval - age)
				} else {
					timer.Reset(taterLocalLibraryScannerIdleDelay)
				}
				continue
			}
		}

		scanCfg := cfg.DeepCopy()
		message := "Checking for new and changed local media"
		if realtimeScan {
			message = "Applying real-time local library changes"
		}
		if !beginTaterLocalLibraryScan(scanCfg, message) {
			if realtimeScan {
				delay = taterLocalLibraryRealtimeRetryDelay
			}
			timer.Reset(delay)
			continue
		}

		request := taterLocalLibraryScanRequest{Incremental: true}
		if realtimeScan {
			request.CategoryIDs = s.takeTaterLocalLibraryRealtimeCategories()
			request.RefreshMetadata = true
			pendingRealtime = false
		}
		slog.InfoContext(ctx, "Starting automatic local media scan",
			"real_time", realtimeScan, "categories", request.CategoryIDs)
		index, err := runTaterLocalLibraryScan(scanCfg, request)
		if err != nil {
			slog.WarnContext(ctx, "Automatic local media scan failed", "error", err)
			if realtimeScan {
				s.restoreTaterLocalLibraryRealtimeCategories(request.CategoryIDs)
				pendingRealtime = true
			}
			delay = taterLocalLibraryAutoScanRetryDelay
		} else {
			slog.InfoContext(ctx, "Automatic local media scan completed")
			if previousErr != nil || taterLocalLibraryFilesChanged(previous, index) {
				// Tube TV observes the updated local index and rebuilds on its own
				// planner cycle; invalidate only when the media set changed.
				taterTVResetGuideForConfig(scanCfg)
			}
			delay = taterLocalLibraryNextScheduledDelay(cfg)
		}
		timer.Reset(delay)
	}
}
