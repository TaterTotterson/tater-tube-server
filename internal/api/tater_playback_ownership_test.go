package api

import (
	"context"
	"testing"
	"time"
)

func TestTaterLocalHLSManagerStopsOnlyRequestedPlayer(t *testing.T) {
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()

	first := &taterLocalHLSSession{
		id: "first", playerID: "player-one", root: t.TempDir(), cancel: cancelFirst,
	}
	second := &taterLocalHLSSession{
		id: "second", playerID: "player-two", root: t.TempDir(), cancel: cancelSecond,
	}
	manager := &taterLocalHLSManager{
		sessions: map[string]*taterLocalHLSSession{first.id: first, second.id: second},
	}

	if stopped := manager.stopForPlayer("player-one"); stopped != 1 {
		t.Fatalf("expected one stopped HLS session, got %d", stopped)
	}
	select {
	case <-firstCtx.Done():
	default:
		t.Fatal("requested player's HLS session was not canceled")
	}
	select {
	case <-secondCtx.Done():
		t.Fatal("another player's HLS session was canceled")
	default:
	}
	if manager.get(first.id) != nil {
		t.Fatal("stopped HLS session remains registered")
	}
	if !manager.isSuperseded(first.id) {
		t.Fatal("late requests can recreate the stopped HLS generation")
	}
	if manager.get(second.id) != second {
		t.Fatal("another player's HLS session was removed")
	}
}

func TestTaterTVHLSManagerRetainsSharedSessionUntilLastPlayerLeaves(t *testing.T) {
	oldCtx, cancelOld := context.WithCancel(context.Background())
	newCtx, cancelNew := context.WithCancel(context.Background())
	manager := &taterTVHLSManager{
		sessions: map[string]*taterTVHLSSession{
			"old-channel": {key: "old-channel", cancel: cancelOld, accessed: time.Now()},
			"new-channel": {key: "new-channel", cancel: cancelNew, accessed: time.Now()},
		},
		playerSessions: map[string]string{},
	}

	manager.bindPlayer("living-room", "old-channel")
	manager.bindPlayer("bedroom", "old-channel")
	manager.bindPlayer("living-room", "new-channel")

	select {
	case <-oldCtx.Done():
		t.Fatal("shared channel stopped while another player was still watching")
	default:
	}
	if !manager.playerBoundTo("living-room", "new-channel") {
		t.Fatal("switching player was not bound to the new channel")
	}
	if manager.playerBoundTo("living-room", "old-channel") {
		t.Fatal("late segments from the old channel are still accepted")
	}

	manager.unbindPlayer("bedroom")
	select {
	case <-oldCtx.Done():
	default:
		t.Fatal("old channel was not stopped after its last player left")
	}
	if manager.get("old-channel") != nil {
		t.Fatal("unused old channel remains registered")
	}

	manager.unbindPlayer("living-room")
	select {
	case <-newCtx.Done():
	default:
		t.Fatal("new channel was not stopped after its player left")
	}
}
