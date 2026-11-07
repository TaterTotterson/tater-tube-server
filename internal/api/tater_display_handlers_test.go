package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/TaterTotterson/tater-tube-server/internal/nzbfilesystem"
)

func TestTaterDisplayAnnouncementCreatesDirectLANEndpoint(t *testing.T) {
	server := &Server{}
	now := time.Now().UTC()
	ok := server.acceptTaterDisplayAnnouncement(taterDisplayDiscoveryMessage{
		Type: "tater.display.announce", ProtocolVersion: 1,
		DeviceID: "rook-1", Name: "Kitchen Rook", Room: "Kitchen", Board: "rook",
		Port: 8930, Capabilities: map[string]any{"now_playing": float64(1)},
	}, &net.UDPAddr{IP: net.ParseIP("192.168.10.44"), Port: 53123}, now)
	if !ok {
		t.Fatal("announcement was rejected")
	}
	display, found := server.taterDisplaySatellite("rook-1", now)
	if !found || !display.Compatible || display.Endpoint != "http://192.168.10.44:8930/v1/now-playing" {
		t.Fatalf("display = %#v", display)
	}
}

func TestTaterDisplayPushSendsLinkedPlayerActivity(t *testing.T) {
	configDir := t.TempDir()
	cfg := config.DefaultConfig(configDir)
	cfg.Players.Paired = []config.PlayerConfig{{
		ID: "player-1", Name: "Living Room TV", LinkedDisplayID: "rook-1",
	}}
	tracker := NewStreamTracker(nil)
	t.Cleanup(tracker.Stop)
	tracker.SetPlayerPlaybackPresence(nzbfilesystem.ActiveStream{
		FilePath: "Movies/The Big Lebowski.mkv", Source: "Local",
		PlayerID: "player-1", UserName: "Living Room TV",
		MediaID: "movie-1", MediaTitle: "The Big Lebowski", MediaType: "movie",
		CategoryID: "local:movies", MediaPath: "Movies/The Big Lebowski.mkv",
		PlaybackPosition: 42, MediaDuration: 7020,
	}, true)

	var received taterCompanionNowPlaying
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer receiver.Close()

	now := time.Now().UTC()
	server := &Server{configManager: &mockConfigManager{cfg: cfg}, streamTracker: tracker}
	server.taterDisplaySatellites.Store("rook-1", taterDisplaySatellite{
		ID: "rook-1", Name: "Kitchen Rook", Endpoint: receiver.URL,
		Online: true, Compatible: true, LastSeenAt: now,
	})
	runtime := &taterDisplayRuntime{ctx: context.Background(), client: receiver.Client()}
	server.pushTaterDisplayForPlayer(runtime, "player-1")

	if !received.Active || received.SourceID != "tater-tube:player-1" || received.SourceName != "Living Room TV" {
		t.Fatalf("source payload = %#v", received)
	}
	if received.Title != "The Big Lebowski" || !strings.HasPrefix(received.EventID, "activity:") || received.LeaseMS != taterDisplayLeaseMS {
		t.Fatalf("now-playing payload = %#v", received)
	}
	if received.ProgressMS != 42000 || received.DurationMS != 7020000 || received.Revision == 0 {
		t.Fatalf("progress payload = %#v", received)
	}
}

func TestTaterDisplayPayloadClearsWithoutActivity(t *testing.T) {
	payload, _, _, active := taterDisplayPayloadForPlayer(
		config.PlayerConfig{ID: "player-1", Name: "TV"}, nil, taterDisplayDeliveryState{}, time.Now().UTC(),
	)
	if active || payload.Active || payload.SourceID != "tater-tube:player-1" {
		t.Fatalf("idle payload = %#v, active=%v", payload, active)
	}
}

func TestTaterDisplayPayloadInfersPauseFromUnchangedActivity(t *testing.T) {
	now := time.Now().UTC()
	player := config.PlayerConfig{ID: "player-1", Name: "TV"}
	activity := nzbfilesystem.ActiveStream{
		FilePath: "Shows/Example.S02E03.mkv", PlayerID: player.ID,
		MediaTitle: "Example", SeriesTitle: "Example Series",
		PlaybackPosition: 42, MediaDuration: 120, StartedAt: now.Add(-time.Minute),
	}

	playing, _, state, active := taterDisplayPayloadForPlayer(player, []nzbfilesystem.ActiveStream{activity}, taterDisplayDeliveryState{}, now)
	if !active || playing.PlaybackState != "playing" || playing.PlaybackSpeed != 1 || playing.Season != 2 || playing.Episode != 3 {
		t.Fatalf("playing payload = %#v", playing)
	}
	paused, _, state, active := taterDisplayPayloadForPlayer(player, []nzbfilesystem.ActiveStream{activity}, state, now.Add(taterDisplayPauseThreshold+time.Second))
	if !active || paused.PlaybackState != "paused" || paused.PlaybackSpeed != 0 {
		t.Fatalf("paused payload = %#v", paused)
	}
	activity.PlaybackPosition++
	resumed, _, _, active := taterDisplayPayloadForPlayer(player, []nzbfilesystem.ActiveStream{activity}, state, now.Add(taterDisplayPauseThreshold+2*time.Second))
	if !active || resumed.PlaybackState != "playing" || resumed.PlaybackSpeed != 1 {
		t.Fatalf("resumed payload = %#v", resumed)
	}
}

func TestShouldSendTaterDisplayKeepsFirmwareProgressSmoothBetweenActivityUpdates(t *testing.T) {
	now := time.Now().UTC()
	previous := taterDisplayDeliveryState{
		EventID: "activity:one", LastAt: now,
		LastPositionMS: 42000, PositionChanged: now, PlaybackState: "playing",
	}

	if shouldSendTaterDisplay(previous, previous, true, now.Add(5*time.Second)) {
		t.Fatal("unchanged activity should let the display interpolate progress")
	}
	advanced := previous
	advanced.LastPositionMS = 57000
	advanced.PositionChanged = now.Add(15 * time.Second)
	if !shouldSendTaterDisplay(previous, advanced, true, now.Add(15*time.Second)) {
		t.Fatal("new Activity progress should be delivered immediately")
	}
	if !shouldSendTaterDisplay(previous, previous, true, now.Add(taterDisplayRenewal)) {
		t.Fatal("unchanged active playback should renew before its lease expires")
	}
	if !shouldSendTaterDisplay(previous, taterDisplayDeliveryState{}, false, now.Add(time.Second)) {
		t.Fatal("ending playback should clear the display")
	}
	cleared := taterDisplayDeliveryState{LastAt: now}
	if shouldSendTaterDisplay(cleared, taterDisplayDeliveryState{}, false, now.Add(time.Second)) {
		t.Fatal("an idle display should not receive repeated clears")
	}
}
