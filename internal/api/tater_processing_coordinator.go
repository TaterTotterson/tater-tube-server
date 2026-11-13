package api

import (
	"context"
	"sync"
	"time"
)

const taterProcessingCoordinatorPollDelay = 100 * time.Millisecond

// taterProcessingCoordinator prevents background media analysis from competing
// with Library Optimization for FFmpeg, GPU, and storage resources. Marker
// workers finish files already in progress, then yield until optimization ends.
type taterProcessingCoordinator struct {
	mu                sync.Mutex
	optimizerReserved bool
	activeMarkers     int
}

func newTaterProcessingCoordinator() *taterProcessingCoordinator {
	return &taterProcessingCoordinator{}
}

func (coordinator *taterProcessingCoordinator) reserveOptimizer() bool {
	if coordinator == nil {
		return true
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.optimizerReserved {
		return false
	}
	coordinator.optimizerReserved = true
	return true
}

func (coordinator *taterProcessingCoordinator) releaseOptimizer() {
	if coordinator == nil {
		return
	}
	coordinator.mu.Lock()
	coordinator.optimizerReserved = false
	coordinator.mu.Unlock()
}

func (coordinator *taterProcessingCoordinator) tryStartMarker() bool {
	if coordinator == nil {
		return true
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.optimizerReserved {
		return false
	}
	coordinator.activeMarkers++
	return true
}

func (coordinator *taterProcessingCoordinator) finishMarker() {
	if coordinator == nil {
		return
	}
	coordinator.mu.Lock()
	coordinator.activeMarkers = max(0, coordinator.activeMarkers-1)
	coordinator.mu.Unlock()
}

func (coordinator *taterProcessingCoordinator) activeMarkerCount() int {
	if coordinator == nil {
		return 0
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return coordinator.activeMarkers
}

func (coordinator *taterProcessingCoordinator) waitForMarkers(ctx context.Context) error {
	if coordinator == nil {
		return nil
	}
	for coordinator.activeMarkerCount() > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(taterProcessingCoordinatorPollDelay):
		}
	}
	return nil
}
