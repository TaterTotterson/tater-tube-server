package pool

import (
	"context"
)

// Live playback receives meaningful connection headroom instead of competing
// with an import burst for every provider slot. The first stream reserves
// eight slots; additional streams reserve four each. Small pools retain at
// least one import slot so background work still makes progress.
const (
	streamBaseHeadroom       = 8
	streamAdditionalHeadroom = 4
)

type ImportBudgetSnapshot struct {
	Capacity          int
	EffectiveCapacity int
	InFlight          int
	Queued            int
	Reserved          int
	ActiveStreams     int
}

// ImportBudget bounds the total number of in-flight import segment (body)
// fetches pool-wide, across all concurrent imports. Its capacity tracks the
// pool's total connection count and automatically shrinks while streams are
// active:
//
//	effective cap = capacity − min(8 + 4 × (activeStreams−1), capacity−1)
//
// so imports expand to the full pool when idle, yield headroom to streams
// under playback, and always keep at least 1 connection so a lone import can
// make progress. A capacity of 0 disables the budget (no-op), which keeps
// pool-less paths and test fakes deadlock-free.
type ImportBudget struct {
	sem          adaptiveSemaphore
	capacity     int
	streamSource StreamActivitySource
}

// NewImportBudget constructs a budget with capacity 0 (disabled). Use
// SetCapacity and SetStreamSource to configure it.
func NewImportBudget() *ImportBudget {
	b := &ImportBudget{}
	b.sem.capLocked = b.effectiveCapLocked
	return b
}

// effectiveCapLocked computes the current cap. Called with sem.mu held.
func (b *ImportBudget) effectiveCapLocked() int {
	if b.capacity <= 0 {
		return 0 // disabled
	}
	reserve := 0
	if b.streamSource != nil {
		streams := b.streamSource.ActiveStreams()
		if streams > 0 {
			reserve = streamBaseHeadroom + streamAdditionalHeadroom*(streams-1)
		}
	}
	if reserve > b.capacity-1 {
		reserve = b.capacity - 1
	}
	return b.capacity - reserve
}

// Snapshot returns the current background-fetch pressure and live-playback
// reservation without changing admission state.
func (b *ImportBudget) Snapshot() ImportBudgetSnapshot {
	b.sem.mu.Lock()
	defer b.sem.mu.Unlock()
	activeStreams := 0
	if b.streamSource != nil {
		activeStreams = b.streamSource.ActiveStreams()
	}
	effective := b.effectiveCapLocked()
	reserved := b.capacity - effective
	if reserved < 0 {
		reserved = 0
	}
	return ImportBudgetSnapshot{
		Capacity:          b.capacity,
		EffectiveCapacity: effective,
		InFlight:          b.sem.inFlight,
		Queued:            len(b.sem.waiters),
		Reserved:          reserved,
		ActiveStreams:     activeStreams,
	}
}

// SetCapacity updates the total connection capacity (sum of provider
// connections). Queued waiters are woken if the effective cap grew; on shrink,
// in-flight fetches drain naturally.
func (b *ImportBudget) SetCapacity(totalConns int) {
	if totalConns < 0 {
		totalConns = 0
	}
	b.sem.mu.Lock()
	b.capacity = totalConns
	b.sem.wakeWaitersLocked()
	b.sem.mu.Unlock()
}

// Capacity returns the configured total capacity (not the stream-adjusted
// effective cap). Useful for sizing worker pools.
func (b *ImportBudget) Capacity() int {
	b.sem.mu.Lock()
	defer b.sem.mu.Unlock()
	return b.capacity
}

// SetStreamSource wires the activity signal. nil sources are tolerated and
// pin the effective cap to the full capacity.
func (b *ImportBudget) SetStreamSource(src StreamActivitySource) {
	b.sem.mu.Lock()
	b.streamSource = src
	b.sem.wakeWaitersLocked()
	b.sem.mu.Unlock()
}

// NotifyStreamChange should be called when the stream count changes so the
// budget can wake or hold waiters according to the new effective cap.
func (b *ImportBudget) NotifyStreamChange() {
	b.sem.mu.Lock()
	b.sem.wakeWaitersLocked()
	b.sem.mu.Unlock()
}

// Acquire blocks until a connection token is available or ctx is cancelled.
// The returned release function MUST be called exactly once when the fetch is
// done. When the capacity is 0 the call is a fast-path no-op.
func (b *ImportBudget) Acquire(ctx context.Context) (release func(), err error) {
	return b.sem.Acquire(ctx)
}
