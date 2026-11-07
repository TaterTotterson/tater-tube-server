package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
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
	if !manager.playerRecentlyStopped("bedroom") {
		t.Fatal("explicitly stopped player was not protected from a late playlist reload")
	}
	if manager.bindPlayer("bedroom", "new-channel") {
		t.Fatal("a late playlist request rebound an explicitly stopped player")
	}
	manager.allowPlayer("bedroom")
	if manager.playerRecentlyStopped("bedroom") {
		t.Fatal("a deliberate new playback plan did not clear the stop guard")
	}

	manager.unbindPlayer("living-room")
	select {
	case <-newCtx.Done():
	default:
		t.Fatal("new channel was not stopped after its player left")
	}
}

func TestKillActiveStreamStopsPairedTubeTVSession(t *testing.T) {
	oldTVManager := globalTaterTVHLS
	oldLocalManager := globalTaterLocalHLS
	t.Cleanup(func() {
		globalTaterTVHLS = oldTVManager
		globalTaterLocalHLS = oldLocalManager
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	globalTaterTVHLS = &taterTVHLSManager{
		sessions: map[string]*taterTVHLSSession{
			"channel-07": {key: "channel-07", cancel: cancel, accessed: time.Now()},
		},
		playerSessions: map[string]string{"living-room": "channel-07"},
		stoppedPlayers: map[string]time.Time{},
	}
	globalTaterLocalHLS = &taterLocalHLSManager{
		sessions:   map[string]*taterLocalHLSSession{},
		superseded: map[string]time.Time{},
	}

	tracker := NewStreamTracker(nil)
	t.Cleanup(tracker.Stop)
	stream := tracker.AddStream(
		"Channel 07 CARTOON CHANNEL",
		"Tube TV",
		"Living Room",
		"10.4.20.114:52488",
		"AppleCoreMedia",
		0,
	)
	stream.PlayerID = "living-room"

	server := &Server{streamTracker: tracker}
	app := fiber.New()
	app.Delete("/streams/:id", server.handleKillStream)
	response, err := app.Test(httptest.NewRequest(http.MethodDelete, "/streams/"+stream.ID, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if tracker.GetStream(stream.ID) != nil {
		t.Fatal("paired Tube TV stream remains active after dashboard termination")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("Tube TV transcode was not canceled")
	}
	if !globalTaterTVHLS.playerRecentlyStopped("living-room") {
		t.Fatal("late AppleCoreMedia requests are not guarded after termination")
	}
}

func TestTaterTVHLSPlaybackPlanRejectsPreviousServerGeneration(t *testing.T) {
	annotated := annotateTaterTVHLSSession(
		"http://tube.local/api/tater/tv/channel/08/playlist.m3u8?player_token=secret",
	)
	parsed, err := url.Parse(annotated)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Query().Get("tv_session"); got != taterTVHLSBootID {
		t.Fatalf("tv_session = %q, want %q", got, taterTVHLSBootID)
	}
	if !taterTVHLSRequestMatchesCurrentBoot(httptest.NewRequest("GET", annotated, nil)) {
		t.Fatal("current process generation was rejected")
	}
	if taterTVHLSRequestMatchesCurrentBoot(httptest.NewRequest(
		"GET",
		"http://tube.local/api/tater/tv/channel/08/playlist.m3u8?tv_session=previous-process",
		nil,
	)) {
		t.Fatal("playlist URL from a previous server process was accepted")
	}
	if !taterTVHLSRequestMatchesCurrentBoot(httptest.NewRequest(
		"GET",
		"http://tube.local/api/tater/tv/channel/08/playlist.m3u8",
		nil,
	)) {
		t.Fatal("legacy untagged playlist URL should remain compatible")
	}
}
