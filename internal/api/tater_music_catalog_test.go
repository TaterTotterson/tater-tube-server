package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
)

func TestTaterLocalMusicMetadataUsesEmbeddedTags(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	ffmpegPath := filepath.Join(binDir, "ffmpeg")
	ffprobePath := filepath.Join(binDir, "ffprobe")
	if err := os.WriteFile(ffmpegPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	probePayload := `{
	  "format": {
	    "duration": "245.75",
	    "tags": {
	      "title": "Three Little Birds",
	      "artist": "Bob Marley & The Wailers",
	      "album_artist": "Bob Marley & The Wailers",
	      "album": "Exodus",
	      "genre": "Reggae; Roots Reggae",
	      "track": "9/10",
	      "date": "1977"
	    }
	  },
	  "streams": [{"codec_type":"video","disposition":{"attached_pic":1}}]
	}`
	probeScript := "#!/bin/sh\ncat <<'EOF'\n" + probePayload + "\nEOF\n"
	if err := os.WriteFile(ffprobePath, []byte(probeScript), 0755); err != nil {
		t.Fatal(err)
	}

	trackPath := filepath.Join(root, "09.Three.Little.Birds.flac")
	if err := os.WriteFile(trackPath, []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Transcoding: config.TranscodingConfig{FFmpegPath: ffmpegPath}}
	metadata := taterLocalMusicMetadataForPath(cfg, trackPath)
	if metadata.Title != "Three Little Birds" ||
		metadata.Artist != "Bob Marley & The Wailers" ||
		metadata.Album != "Exodus" ||
		metadata.Track != 9 ||
		metadata.Year != "1977" {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
	if strings.Join(metadata.Genres, "|") != "Reggae|Roots Reggae" {
		t.Fatalf("unexpected genres: %#v", metadata.Genres)
	}
	if metadata.Duration != 245.75 {
		t.Fatalf("unexpected duration: %f", metadata.Duration)
	}
	if !metadata.HasArtwork {
		t.Fatal("expected embedded artwork to be detected")
	}
}

func TestTaterMusicCatalogPublishesAndServesEmbeddedArtwork(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	albumDir := filepath.Join(root, "Artist", "Album")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(albumDir, 0755); err != nil {
		t.Fatal(err)
	}
	ffmpegPath := filepath.Join(binDir, "ffmpeg")
	ffprobePath := filepath.Join(binDir, "ffprobe")
	if err := os.WriteFile(ffmpegPath, []byte("#!/bin/sh\nprintf '\\377\\330cover\\377\\331'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	probePayload := `{"format":{"duration":"180","tags":{"title":"Covered Song","artist":"Artist","album":"Album"}},"streams":[{"codec_type":"video","disposition":{"attached_pic":1}}]}`
	probeScript := "#!/bin/sh\ncat <<'EOF'\n" + probePayload + "\nEOF\n"
	if err := os.WriteFile(ffprobePath, []byte(probeScript), 0755); err != nil {
		t.Fatal(err)
	}
	trackPath := filepath.Join(albumDir, "01 Covered Song.flac")
	if err := os.WriteFile(trackPath, []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}

	enabled := true
	const playerToken = "artwork-player-token"
	cfg := &config.Config{
		LocalMedia: config.LocalMediaConfig{
			Enabled: &enabled,
			Categories: []config.LocalMediaCategory{{
				ID: "music", Name: "Music", LibraryType: "music", Paths: []string{root}, Enabled: &enabled,
			}},
		},
		Players: config.PlayersConfig{Paired: []config.PlayerConfig{{
			ID: "music-core", Name: "Tater Music Core", TokenHash: hashTaterSecret(playerToken), LastSeenAt: "2099-01-01T00:00:00Z",
		}}},
		Transcoding: config.TranscodingConfig{FFmpegPath: ffmpegPath},
	}
	app := fiber.New()
	server := &Server{configManager: &mockConfigManager{cfg: cfg}}
	app.Get("/catalog", server.handleTaterMusicCatalog)
	app.Get("/artwork", server.handleTaterMusicArtwork)

	catalogRequest := httptest.NewRequest(http.MethodGet, "/catalog", nil)
	catalogRequest.Header.Set("Authorization", "Bearer "+playerToken)
	catalogResponse, err := app.Test(catalogRequest)
	if err != nil {
		t.Fatal(err)
	}
	var envelope testAPIResponse[struct {
		Tracks []taterUsenetItem `json:"tracks"`
	}]
	if err := json.NewDecoder(catalogResponse.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Tracks) != 1 || !envelope.Data.Tracks[0].HasArtwork || envelope.Data.Tracks[0].Poster == "" {
		t.Fatalf("catalog did not expose artwork: %#v", envelope.Data.Tracks)
	}
	posterURL, err := url.Parse(envelope.Data.Tracks[0].Poster)
	if err != nil {
		t.Fatal(err)
	}
	artworkRequest := httptest.NewRequest(http.MethodGet, "/artwork?"+posterURL.RawQuery, nil)
	artworkResponse, err := app.Test(artworkRequest)
	if err != nil {
		t.Fatal(err)
	}
	if artworkResponse.StatusCode != http.StatusOK || artworkResponse.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("unexpected artwork response: status=%d type=%q", artworkResponse.StatusCode, artworkResponse.Header.Get("Content-Type"))
	}
	body, err := io.ReadAll(artworkResponse.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "\xff\xd8cover\xff\xd9" {
		t.Fatalf("unexpected artwork body: %q", body)
	}
}

func TestTaterMusicCatalogHelpersFilterAndBuildFacets(t *testing.T) {
	tracks := []taterUsenetItem{
		{
			Title:        "Three Little Birds",
			Artist:       "Bob Marley & The Wailers",
			AlbumArtist:  "Bob Marley & The Wailers",
			Album:        "Exodus",
			Genres:       []string{"Reggae", "Roots Reggae"},
			RatingKey:    "track:one",
			SizeBytes:    100,
			ModifiedUnix: 200,
		},
		{
			Title:        "Blue in Green",
			Artist:       "Miles Davis",
			Album:        "Kind of Blue",
			Genre:        "Jazz",
			RatingKey:    "track:two",
			SizeBytes:    120,
			ModifiedUnix: 201,
		},
	}
	if !taterMusicTrackContains(tracks[0], "bob reggae") {
		t.Fatal("expected artist and genre query to match")
	}
	if taterMusicTrackContains(tracks[1], "reggae") {
		t.Fatal("did not expect jazz track to match reggae")
	}
	artists, albums, genres := taterMusicFacets(tracks)
	if strings.Join(artists, "|") != "Bob Marley & The Wailers|Miles Davis" {
		t.Fatalf("unexpected artists: %#v", artists)
	}
	if strings.Join(albums, "|") != "Exodus|Kind of Blue" {
		t.Fatalf("unexpected albums: %#v", albums)
	}
	if strings.Join(genres, "|") != "Jazz|Reggae|Roots Reggae" {
		t.Fatalf("unexpected genres: %#v", genres)
	}
	if id := taterMusicCatalogID(tracks); len(id) != 24 {
		t.Fatalf("unexpected catalog id: %q", id)
	}
}

func TestTaterMusicCatalogEndpointReturnsPairedPlayerLibrary(t *testing.T) {
	root := t.TempDir()
	albumDir := filepath.Join(root, "Bob Marley", "Exodus")
	if err := os.MkdirAll(albumDir, 0755); err != nil {
		t.Fatal(err)
	}
	trackPath := filepath.Join(albumDir, "09 Three Little Birds.flac")
	if err := os.WriteFile(trackPath, []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	ffmpegPath := filepath.Join(binDir, "ffmpeg")
	ffprobePath := filepath.Join(binDir, "ffprobe")
	if err := os.WriteFile(ffmpegPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	probeScript := `#!/bin/sh
cat <<'EOF'
{"format":{"duration":"245.75","tags":{"title":"Three Little Birds","artist":"Bob Marley & The Wailers","album_artist":"Bob Marley & The Wailers","album":"Exodus","genre":"Reggae","track":"9","date":"1977"}},"streams":[]}
EOF
`
	if err := os.WriteFile(ffprobePath, []byte(probeScript), 0755); err != nil {
		t.Fatal(err)
	}

	enabled := true
	const playerToken = "music-player-token"
	cfg := &config.Config{
		LocalMedia: config.LocalMediaConfig{
			Enabled: &enabled,
			Categories: []config.LocalMediaCategory{{
				ID:          "music",
				Name:        "Music",
				LibraryType: "music",
				Paths:       []string{root},
				Enabled:     &enabled,
			}},
		},
		Players: config.PlayersConfig{
			Paired: []config.PlayerConfig{{
				ID:         "music-core",
				Name:       "Tater Music Core",
				TokenHash:  hashTaterSecret(playerToken),
				LastSeenAt: "2099-01-01T00:00:00Z",
			}},
		},
		Transcoding: config.TranscodingConfig{FFmpegPath: ffmpegPath},
	}
	app := fiber.New()
	server := &Server{configManager: &mockConfigManager{cfg: cfg}}
	app.Get("/catalog", server.handleTaterMusicCatalog)

	req := httptest.NewRequest(http.MethodGet, "/catalog?genre=reggae", nil)
	req.Header.Set("Authorization", "Bearer "+playerToken)
	response, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", response.StatusCode)
	}
	var envelope testAPIResponse[struct {
		CatalogID string            `json:"catalog_id"`
		Tracks    []taterUsenetItem `json:"tracks"`
		Artists   []string          `json:"artists"`
		Genres    []string          `json:"genres"`
		Total     int               `json:"total"`
	}]
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.Success || envelope.Data.Total != 1 || len(envelope.Data.Tracks) != 1 {
		t.Fatalf("unexpected catalog response: %#v", envelope)
	}
	track := envelope.Data.Tracks[0]
	if track.Title != "Three Little Birds" ||
		track.Artist != "Bob Marley & The Wailers" ||
		track.Album != "Exodus" ||
		track.Genre != "Reggae" {
		t.Fatalf("unexpected catalog track: %#v", track)
	}
	if envelope.Data.CatalogID == "" ||
		strings.Join(envelope.Data.Artists, "|") != "Bob Marley & The Wailers" ||
		strings.Join(envelope.Data.Genres, "|") != "Reggae" {
		t.Fatalf("unexpected catalog facets: %#v", envelope.Data)
	}
	if !strings.Contains(track.StreamURL, "/api/tater/local/stream") ||
		!strings.Contains(track.StreamURL, "player_token="+playerToken) {
		t.Fatalf("unexpected authenticated stream URL: %q", track.StreamURL)
	}

	unauthorized := httptest.NewRequest(http.MethodGet, "/catalog", nil)
	unauthorizedResponse, err := app.Test(unauthorized)
	if err != nil {
		t.Fatal(err)
	}
	if unauthorizedResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized status, got %d", unauthorizedResponse.StatusCode)
	}
}
