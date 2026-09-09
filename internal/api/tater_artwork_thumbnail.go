package api

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	taterArtworkThumbnailWidth  = 320
	taterArtworkThumbnailHeight = 480
	taterArtworkPosterWidth     = 600
	taterArtworkPosterHeight    = 900
	taterArtworkWideWidth       = 960
	taterArtworkWideHeight      = 540
	taterArtworkMaximumPixels   = 80_000_000
)

type taterArtworkThumbnailCacheEntry struct {
	Size            int64
	ModTimeUnixNano int64
	JPEG            []byte
}

var taterArtworkThumbnailCache = struct {
	sync.Mutex
	Items map[string]taterArtworkThumbnailCacheEntry
}{Items: map[string]taterArtworkThumbnailCacheEntry{}}

var taterArtworkThumbnailSlots = make(chan struct{}, 2)

func taterArtworkThumbnail(path string) ([]byte, error) {
	return taterArtworkThumbnailSized(path, taterArtworkThumbnailWidth, taterArtworkThumbnailHeight)
}

func taterArtworkThumbnailSized(path string, maximumWidth, maximumHeight int) ([]byte, error) {
	if maximumWidth <= 0 || maximumHeight <= 0 {
		return nil, fmt.Errorf("artwork thumbnail dimensions are invalid")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absPath)
	if err != nil || info == nil || !info.Mode().IsRegular() {
		if err == nil {
			err = os.ErrNotExist
		}
		return nil, err
	}

	cacheKey := fmt.Sprintf("%s\x00%dx%d", absPath, maximumWidth, maximumHeight)
	taterArtworkThumbnailCache.Lock()
	if cached, ok := taterArtworkThumbnailCache.Items[cacheKey]; ok &&
		cached.Size == info.Size() && cached.ModTimeUnixNano == info.ModTime().UnixNano() && len(cached.JPEG) > 0 {
		result := append([]byte(nil), cached.JPEG...)
		taterArtworkThumbnailCache.Unlock()
		return result, nil
	}
	taterArtworkThumbnailCache.Unlock()

	taterArtworkThumbnailSlots <- struct{}{}
	defer func() { <-taterArtworkThumbnailSlots }()

	file, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > taterArtworkMaximumPixels {
		_ = file.Close()
		return nil, fmt.Errorf("artwork dimensions are invalid or too large")
	}
	if _, err := file.Seek(0, 0); err != nil {
		_ = file.Close()
		return nil, err
	}
	source, _, err := image.Decode(file)
	_ = file.Close()
	if err != nil {
		return nil, err
	}
	result, err := encodeTaterArtworkThumbnailSized(source, maximumWidth, maximumHeight)
	if err != nil {
		return nil, err
	}

	taterArtworkThumbnailCache.Lock()
	if len(taterArtworkThumbnailCache.Items) >= 256 {
		taterArtworkThumbnailCache.Items = map[string]taterArtworkThumbnailCacheEntry{}
	}
	taterArtworkThumbnailCache.Items[cacheKey] = taterArtworkThumbnailCacheEntry{
		Size: info.Size(), ModTimeUnixNano: info.ModTime().UnixNano(), JPEG: append([]byte(nil), result...),
	}
	taterArtworkThumbnailCache.Unlock()
	return result, nil
}

func taterArtworkThumbnailBytes(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > taterMusicArtworkMaximumBytes {
		return nil, fmt.Errorf("artwork is empty or too large")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > taterArtworkMaximumPixels {
		return nil, fmt.Errorf("artwork dimensions are invalid or too large")
	}
	source, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	return encodeTaterArtworkThumbnail(source)
}

func encodeTaterArtworkThumbnail(source image.Image) ([]byte, error) {
	return encodeTaterArtworkThumbnailSized(
		source, taterArtworkThumbnailWidth, taterArtworkThumbnailHeight)
}

func encodeTaterArtworkThumbnailSized(source image.Image, maximumWidth, maximumHeight int) ([]byte, error) {
	if maximumWidth <= 0 || maximumHeight <= 0 {
		return nil, fmt.Errorf("artwork thumbnail dimensions are invalid")
	}
	width := source.Bounds().Dx()
	height := source.Bounds().Dy()
	scale := min(
		float64(maximumWidth)/float64(width),
		float64(maximumHeight)/float64(height),
	)
	if scale > 1 {
		scale = 1
	}
	width = max(1, int(float64(width)*scale))
	height = max(1, int(float64(height)*scale))
	thumbnail := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.ApproxBiLinear.Scale(thumbnail, thumbnail.Bounds(), source, source.Bounds(), draw.Over, nil)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, thumbnail, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	result := encoded.Bytes()
	if len(result) == 0 || len(result) > taterMusicArtworkMaximumBytes {
		return nil, fmt.Errorf("artwork thumbnail is empty or too large")
	}
	return result, nil
}
