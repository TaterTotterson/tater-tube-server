package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/database"
)

func TestTaterOTACandidatesBuildSafeLaunchTargetsFromHistory(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := json.Marshal(map[string]string{
		"module_id":      "com.240mp.ota",
		"channel_number": "5.1",
		"channel_name":   "RETRO TV",
	})
	if err != nil {
		t.Fatal(err)
	}
	events := []database.TaterViewingEvent{
		{
			EventID: "watch-2", Source: "over_the_air", MediaID: "ota:5.1:retro-tv",
			MediaType: "live", Title: "CH 5.1  RETRO TV", State: "stopped",
			OccurredAt: now, MetadataJSON: string(metadata),
		},
		{
			EventID: "watch-1", Source: "over_the_air", MediaID: "ota:5.1:retro-tv",
			MediaType: "live", Title: "CH 5.1  RETRO TV", State: "stopped",
			OccurredAt: now.Add(-time.Hour), MetadataJSON: string(metadata),
		},
		{
			EventID: "legacy", Source: "over_the_air", MediaID: "legacy-hash",
			MediaType: "live", Title: "A channel without safe launch data",
			OccurredAt: now, MetadataJSON: "{}",
		},
	}

	candidates := taterOTACandidates(events)
	if len(candidates) != 1 {
		t.Fatalf("expected one safe OTA candidate, got %#v", candidates)
	}
	candidate := candidates[0]
	if candidate.Source != "over_the_air" || candidate.MediaType != "live" {
		t.Fatalf("unexpected OTA candidate type: %#v", candidate)
	}
	if candidate.Launch.Type != "module" ||
		candidate.Launch.ModuleID != "com.240mp.ota" ||
		candidate.Launch.ChannelNumber != "5.1" ||
		candidate.Launch.ChannelName != "RETRO TV" {
		t.Fatalf("unexpected OTA launch target: %#v", candidate.Launch)
	}
	if strings.Contains(candidate.Launch.StreamURL, "http") ||
		strings.Contains(candidate.Launch.Path, "http") {
		t.Fatalf("OTA recommendation exposed a stream URL: %#v", candidate.Launch)
	}
	if !strings.Contains(candidate.Description, "2 recent sessions") {
		t.Fatalf("expected watch frequency in description, got %q", candidate.Description)
	}
}

func TestTaterGreetingForHour(t *testing.T) {
	tests := []struct {
		hour int
		want string
	}{
		{5, "Good morning"},
		{11, "Good morning"},
		{12, "Good afternoon"},
		{16, "Good afternoon"},
		{17, "Good evening"},
		{23, "Good evening"},
		{0, "Good evening"},
	}
	for _, test := range tests {
		if got := taterGreetingForHour(test.hour); got != test.want {
			t.Fatalf("hour %d: got %q, want %q", test.hour, got, test.want)
		}
	}
}
