package api

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaterArtworkThumbnailShrinksAndCachesPoster(t *testing.T) {
	path := filepath.Join(t.TempDir(), "poster.jpg")
	source := image.NewRGBA(image.Rect(0, 0, 1200, 1800))
	for y := 0; y < source.Bounds().Dy(); y++ {
		for x := 0; x < source.Bounds().Dx(); x++ {
			source.SetRGBA(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 80, A: 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(file, source, &jpeg.Options{Quality: 90}); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	thumbnail, err := taterArtworkThumbnail(path)
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(thumbnail))
	if err != nil {
		t.Fatal(err)
	}
	if config.Width != 320 || config.Height != 480 {
		t.Fatalf("unexpected thumbnail dimensions: %dx%d", config.Width, config.Height)
	}
	cached, err := taterArtworkThumbnail(path)
	if err != nil || !bytes.Equal(thumbnail, cached) {
		t.Fatalf("thumbnail cache mismatch: error=%v", err)
	}
}

func TestTaterVideoAdminArtworkURLRequestsThumbnail(t *testing.T) {
	url := taterLocalVideoAdminArtworkURL(taterLocalVideoIndex{
		ID: "video-id", HasArtwork: true, ArtworkUpdated: 123,
	})
	if !strings.Contains(url, "thumbnail=1") || !strings.Contains(url, "v=123") {
		t.Fatalf("admin artwork URL did not request a versioned thumbnail: %q", url)
	}
	musicURL := taterLocalMusicAdminArtworkURL(taterLocalMusicAlbumIndex{
		ID: "album-id", HasArtwork: true, ArtworkUpdated: 456,
	})
	if !strings.Contains(musicURL, "thumbnail=1") || !strings.Contains(musicURL, "v=456") {
		t.Fatalf("music admin artwork URL did not request a versioned thumbnail: %q", musicURL)
	}
}
