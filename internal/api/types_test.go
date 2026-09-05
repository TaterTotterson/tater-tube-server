package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

func TestConfigAPIResponseMasksAudioDBAPIKey(t *testing.T) {
	cfg := config.DefaultConfig(t.TempDir())
	cfg.LocalMedia.AudioDBAPIKey = "private-audiodb-key"
	cfg.LocalMedia.TMDBAPIKey = "private-tmdb-key"

	response := ToConfigAPIResponse(cfg, "")
	if response.LocalMedia.AudioDBAPIKey != "********" || !response.LocalMedia.AudioDBAPIKeySet {
		t.Fatalf("unexpected AudioDB API key response: %#v", response.LocalMedia)
	}
	if response.LocalMedia.TMDBAPIKey != "********" || !response.LocalMedia.TMDBAPIKeySet {
		t.Fatalf("unexpected TMDB API key response: %#v", response.LocalMedia)
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), cfg.LocalMedia.AudioDBAPIKey) {
		t.Fatal("AudioDB API key leaked into the configuration response")
	}
	if strings.Contains(string(raw), cfg.LocalMedia.TMDBAPIKey) {
		t.Fatal("TMDB API key leaked into the configuration response")
	}
}
