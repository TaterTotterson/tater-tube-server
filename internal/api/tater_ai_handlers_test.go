package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/TaterTotterson/tater-tube-server/internal/database"
	"github.com/gofiber/fiber/v2"
)

func TestTaterPicksTTSUsesDedicatedGroupBriefing(t *testing.T) {
	configDir := t.TempDir()
	cfg := config.DefaultConfig(configDir)
	cfg.Players.Paired = []config.PlayerConfig{{
		ID: "living-room", Name: "Living Room", TokenHash: hashTaterSecret("player-token"),
	}}
	queueDB, err := database.NewDB(database.Config{
		Type: "sqlite", DatabasePath: filepath.Join(configDir, "picks-briefing.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queueDB.Close() })
	repo := database.NewRepository(queueDB.Connection(), database.DialectSQLite)
	now := time.Now().UTC().Truncate(time.Millisecond)
	groupBriefing := "You have been returning to clever comedies, so I kept this group light, familiar, and easy to settle into."
	if err := repo.SaveTaterRecommendations(context.Background(), database.TaterRecommendationBatch{
		ID: "batch-one", ProfileID: taterDefaultProfileID,
		Summary:       "A few good choices are ready.",
		PicksBriefing: groupBriefing,
		GeneratedAt:   now, ExpiresAt: now.Add(time.Hour),
	}, []database.TaterRecommendation{{
		ID: "pick-one", BatchID: "batch-one", Rank: 1, CandidateID: "movie-one",
		Title: "Movie One", MediaType: "movie", Source: "local_media",
		Reason: "A smart comedy for tonight.", LaunchJSON: `{}`, CreatedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}

	server := &Server{configManager: &mockConfigManager{cfg: cfg}, queueRepo: repo}
	app := fiber.New()
	app.Post("/api/tater/tts/requests", server.handleTaterPlayerCreateTTSRequest)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/tater/tts/requests",
		strings.NewReader(`{"profile_id":"household","batch_id":"batch-one","briefing_kind":"recommendations","local_hour":19}`),
	)
	request.Header.Set("Authorization", "Bearer player-token")
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", response.StatusCode)
	}
	var envelope struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	queued, err := repo.GetTaterTTSRequest(context.Background(), envelope.Data.ID, "living-room")
	if err != nil {
		t.Fatal(err)
	}
	want := "Good evening. " + groupBriefing
	if queued.Text != want {
		t.Fatalf("queued TTS text = %q, want %q", queued.Text, want)
	}
	if strings.Contains(queued.Text, "A few good choices") {
		t.Fatalf("queued TTS used the generic batch summary: %q", queued.Text)
	}
}

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

func TestTaterBalancedRecommendationCandidatesIncludesMoviesSeriesAndGenres(t *testing.T) {
	candidates := []taterCandidate{}
	for i := 0; i < 300; i++ {
		genre := "Action"
		if i%2 == 0 {
			genre = "Comedy"
		}
		candidates = append(candidates, taterCandidate{
			ID: fmt.Sprintf("movie-%03d", i), Title: fmt.Sprintf("Movie %03d", i),
			MediaType: "movie", Genres: []string{genre},
		})
	}
	for i := 0; i < 24; i++ {
		genre := "Drama"
		if i%2 == 0 {
			genre = "Animation"
		}
		candidates = append(candidates, taterCandidate{
			ID: fmt.Sprintf("series-%03d", i), Title: fmt.Sprintf("Series %03d", i),
			MediaType: "series", Genres: []string{genre},
		})
	}

	selected := taterBalancedRecommendationCandidates(candidates, 200, "batch-one", nil, false)
	if len(selected) != 200 {
		t.Fatalf("expected 200 candidates, got %d", len(selected))
	}
	seriesCount := 0
	seenGenres := map[string]bool{}
	seenIDs := map[string]bool{}
	for _, candidate := range selected {
		if seenIDs[candidate.ID] {
			t.Fatalf("duplicate candidate %q", candidate.ID)
		}
		seenIDs[candidate.ID] = true
		if candidate.MediaType == "series" {
			seriesCount++
		}
		for _, genre := range candidate.Genres {
			seenGenres[genre] = true
		}
	}
	if seriesCount != 24 {
		t.Fatalf("expected every available series in the shortlist, got %d", seriesCount)
	}
	for _, genre := range []string{"Action", "Comedy", "Drama", "Animation"} {
		if !seenGenres[genre] {
			t.Fatalf("expected genre %q in shortlist", genre)
		}
	}

	rotated := taterBalancedRecommendationCandidates(candidates, 200, "batch-two", nil, false)
	if selected[0].ID == rotated[0].ID && selected[1].ID == rotated[1].ID {
		t.Fatalf("different batch seeds did not rotate the shortlist")
	}
}

func TestTaterBalancedRecommendationCandidatesDefersPreviousPicks(t *testing.T) {
	candidates := make([]taterCandidate, 0, 40)
	excluded := map[string]bool{}
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("movie-%02d", i)
		candidates = append(candidates, taterCandidate{ID: id, MediaType: "movie"})
		if i < 8 {
			excluded[id] = true
		}
	}
	selected := taterBalancedRecommendationCandidates(candidates, 20, "fresh", excluded, false)
	for _, candidate := range selected {
		if excluded[candidate.ID] {
			t.Fatalf("previous pick %q returned while fresh alternatives exist", candidate.ID)
		}
	}
}

func TestTaterBalancedRecommendationCandidatesPrioritizesWeekendMorningTitles(t *testing.T) {
	candidates := []taterCandidate{}
	for i := 0; i < 24; i++ {
		candidates = append(candidates, taterCandidate{
			ID: fmt.Sprintf("general-%02d", i), MediaType: "movie", Genres: []string{"Drama"},
		})
	}
	for i := 0; i < 12; i++ {
		mediaType := "movie"
		if i%2 == 0 {
			mediaType = "series"
		}
		candidates = append(candidates, taterCandidate{
			ID: fmt.Sprintf("cartoon-%02d", i), MediaType: mediaType, Genres: []string{"Animation", "Family"},
		})
	}
	selected := taterBalancedRecommendationCandidates(candidates, 24, "saturday", nil, true)
	for i, candidate := range selected[:8] {
		if !taterWeekendMorningCandidate(candidate) {
			t.Fatalf("priority slot %d was not a weekend-morning title: %#v", i, candidate)
		}
	}
}

func TestTaterAssistantFirstName(t *testing.T) {
	if got := cleanTaterAssistantFirstName("  Totty   Totterson "); got != "Totty" {
		t.Fatalf("got %q, want Totty", got)
	}
	if got := taterAssistantNameFromHeader("Jos%C3%A9%20Totterson"); got != "José" {
		t.Fatalf("got %q, want José", got)
	}
	if got := taterAssistantNameFromHeader(""); got != "" {
		t.Fatalf("empty header should not replace a stored name, got %q", got)
	}
}
