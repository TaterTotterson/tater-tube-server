package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

func taterMusicNFOTestConfig(t *testing.T) (*config.Config, string, string) {
	t.Helper()
	root := t.TempDir()
	musicRoot := filepath.Join(root, "music")
	albumDir := filepath.Join(musicRoot, "Bob Marley & The Wailers", "Exodus")
	if err := os.MkdirAll(albumDir, 0755); err != nil {
		t.Fatal(err)
	}
	enabled := true
	cfg := &config.Config{
		Metadata: config.MetadataConfig{RootPath: filepath.Join(root, "metadata")},
		LocalMedia: config.LocalMediaConfig{Categories: []config.LocalMediaCategory{{
			ID: "music", Name: "Music", LibraryType: "music", Paths: []string{musicRoot}, Enabled: &enabled,
		}}},
	}
	return cfg, musicRoot, albumDir
}

func TestTaterMusicNFOCreatesCompatibleAlbumAndArtistSidecars(t *testing.T) {
	cfg, _, albumDir := taterMusicNFOTestConfig(t)
	album := taterLocalMusicAlbumIndex{
		ID: "album:exodus", CategoryID: "music", CategoryName: "Music", SourceIndex: 0,
		Path: "Bob Marley & The Wailers/Exodus", Title: "Exodus",
		Artist: "Bob Marley & The Wailers", AlbumArtist: "Bob Marley & The Wailers",
		Year: "1977", Genres: []string{"Reggae", "Roots Reggae"}, HasArtwork: true,
		MusicBrainzID:       "2f8f6b31-8b7b-3adf-9c39-54d41c66e4f1",
		MusicBrainzArtistID: "41acbdf5-6e25-42c9-81d4-1b5d1913fe1b",
	}
	index := taterLocalLibraryIndex{
		Categories: []taterLocalLibraryCategoryIndex{{ID: "music", LibraryType: "music"}},
		Albums:     []taterLocalMusicAlbumIndex{album},
	}
	progress := taterMusicEnrichmentProgress{}
	if err := scrapeTaterMissingAlbumArtwork(context.Background(), cfg, &index, func(next taterMusicEnrichmentProgress) {
		progress = next
	}); err != nil {
		t.Fatal(err)
	}
	if progress.MetadataFound != 2 {
		t.Fatalf("expected album and artist NFO files, got progress %#v", progress)
	}
	createdAlbum := index.Albums[0]
	if !createdAlbum.HasMetadata || !createdAlbum.HasArtistMetadata ||
		index.Categories[0].Stats.Metadata != 1 || index.Categories[0].Stats.MissingMetadata != 0 {
		t.Fatalf("music metadata was not indexed: album=%#v stats=%#v", createdAlbum, index.Categories[0].Stats)
	}

	albumRaw, err := os.ReadFile(filepath.Join(albumDir, "album.nfo"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"<album>", "<title>Exodus</title>", "<artist>Bob Marley &amp; The Wailers</artist>",
		"<year>1977</year>", "<genre>Reggae</genre>",
		"<musicbrainzreleasegroupid>2f8f6b31-8b7b-3adf-9c39-54d41c66e4f1</musicbrainzreleasegroupid>",
	} {
		if !strings.Contains(string(albumRaw), expected) {
			t.Fatalf("album NFO did not contain %q:\n%s", expected, albumRaw)
		}
	}
	artistRaw, err := os.ReadFile(filepath.Join(filepath.Dir(albumDir), "artist.nfo"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"<artist>", "<title>Bob Marley &amp; The Wailers</title>",
		"<musicbrainzartistid>41acbdf5-6e25-42c9-81d4-1b5d1913fe1b</musicbrainzartistid>",
	} {
		if !strings.Contains(string(artistRaw), expected) {
			t.Fatalf("artist NFO did not contain %q:\n%s", expected, artistRaw)
		}
	}
}

func TestTaterMusicNFOUsesExistingFilesAndNeverOverwritesThem(t *testing.T) {
	cfg, _, albumDir := taterMusicNFOTestConfig(t)
	albumPath := filepath.Join(albumDir, "album.nfo")
	artistPath := filepath.Join(filepath.Dir(albumDir), "artist.nfo")
	originalAlbum := []byte("\xef\xbb\xbf" + `<?xml version="1.0" encoding="utf-8"?>
<album><title>Exodus</title><artist>Bob Marley &amp; The Wailers</artist><year>1977</year><style>Roots Reggae</style><musicbrainzreleasegroupid>existing-release-group</musicbrainzreleasegroupid></album>`)
	originalArtist := []byte(`<?xml version="1.0" encoding="utf-8"?>
<artist><title>Bob Marley &amp; The Wailers</title><biography>Existing artist biography.</biography><musicbrainzartistid>existing-artist</musicbrainzartistid></artist>`)
	if err := os.WriteFile(albumPath, originalAlbum, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artistPath, originalArtist, 0644); err != nil {
		t.Fatal(err)
	}
	album := taterLocalMusicAlbumIndex{
		ID: "album:existing", CategoryID: "music", SourceIndex: 0,
		Path: "Bob Marley & The Wailers/Exodus", Title: "Exodus",
		Artist: "Bob Marley & The Wailers", AlbumArtist: "Bob Marley & The Wailers",
	}
	applyTaterLocalMusicNFO(cfg, &album)
	if !album.HasMetadata || !album.HasArtistMetadata || album.MusicBrainzID != "existing-release-group" ||
		album.MusicBrainzArtistID != "existing-artist" || !strings.Contains(strings.Join(album.Styles, "|"), "Roots Reggae") {
		t.Fatalf("existing music NFO metadata was not applied: %#v", album)
	}
	created, err := ensureTaterMusicNFO(context.Background(), cfg, &album)
	if err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatalf("existing NFO files were unexpectedly replaced: %d writes", created)
	}
	if after, err := os.ReadFile(albumPath); err != nil || string(after) != string(originalAlbum) {
		t.Fatalf("album NFO changed: error=%v", err)
	}
	if after, err := os.ReadFile(artistPath); err != nil || string(after) != string(originalArtist) {
		t.Fatalf("artist NFO changed: error=%v", err)
	}
}

func TestTaterMusicNFODoesNotCreateArtistFileInUnstructuredLibraryRoot(t *testing.T) {
	root := t.TempDir()
	albumDir := filepath.Join(root, "Exodus")
	if err := os.MkdirAll(albumDir, 0755); err != nil {
		t.Fatal(err)
	}
	enabled := true
	cfg := &config.Config{LocalMedia: config.LocalMediaConfig{Categories: []config.LocalMediaCategory{{
		ID: "music", Name: "Music", LibraryType: "music", Paths: []string{root}, Enabled: &enabled,
	}}}}
	album := taterLocalMusicAlbumIndex{
		ID: "album:flat", CategoryID: "music", SourceIndex: 0, Path: "Exodus",
		Title: "Exodus", Artist: "Bob Marley & The Wailers", MusicBrainzID: "release-group",
	}
	created, err := ensureTaterMusicNFO(context.Background(), cfg, &album)
	if err != nil {
		t.Fatal(err)
	}
	if created != 1 || !album.HasMetadata || album.ArtistMetadataAvailable {
		t.Fatalf("unexpected flat-library metadata result: created=%d album=%#v", created, album)
	}
	if _, err := os.Stat(filepath.Join(root, "artist.nfo")); !os.IsNotExist(err) {
		t.Fatalf("artist.nfo should not be created in the music library root: %v", err)
	}
}
