package api

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessingCoordinatorLetsActiveMarkersDrainBeforeOptimization(t *testing.T) {
	coordinator := newTaterProcessingCoordinator()
	require.True(t, coordinator.tryStartMarker())
	require.True(t, coordinator.reserveOptimizer())
	assert.False(t, coordinator.tryStartMarker(), "new marker work must yield to the optimizer")

	waiting := make(chan error, 1)
	go func() {
		waiting <- coordinator.waitForMarkers(t.Context())
	}()

	select {
	case <-waiting:
		t.Fatal("optimizer must wait for the active marker file")
	case <-time.After(50 * time.Millisecond):
	}

	coordinator.finishMarker()
	select {
	case err := <-waiting:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("optimizer did not continue after marker work drained")
	}

	coordinator.releaseOptimizer()
	assert.True(t, coordinator.tryStartMarker(), "marker work must resume after optimization")
	coordinator.finishMarker()
}

func TestProcessingCoordinatorWaitHonorsCancellation(t *testing.T) {
	coordinator := newTaterProcessingCoordinator()
	require.True(t, coordinator.tryStartMarker())
	require.True(t, coordinator.reserveOptimizer())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.ErrorIs(t, coordinator.waitForMarkers(ctx), context.Canceled)
	coordinator.finishMarker()
	coordinator.releaseOptimizer()
}
