package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

type taterHDREnhancementProbeResult struct {
	available bool
	details   string
	checkedAt time.Time
}

var taterHDREnhancementProbes = struct {
	sync.Mutex
	byFFmpeg map[string]taterHDREnhancementProbeResult
}{byFFmpeg: map[string]taterHDREnhancementProbeResult{}}

func requestedTaterHDREnhancement(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	if cleanTaterVideoRange(r.URL.Query().Get("tater_source_video_range")) != "sdr" ||
		cleanTaterVideoRange(r.URL.Query().Get("tater_output_video_range")) != "hdr10" {
		return ""
	}
	value := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tater_hdr_enhancement")))
	switch value {
	case "conservative", "balanced", "vivid":
		return value
	default:
		return ""
	}
}

func taterHDREnhancementFilter(strength string) string {
	strength = cleanTaterHDREnhancementStrength(strength)
	toneMappingParam := "0.50"
	contrastRecovery := "0.20"
	switch strength {
	case "conservative":
		toneMappingParam = "0.25"
		contrastRecovery = "0.10"
	case "vivid":
		toneMappingParam = "0.75"
		contrastRecovery = "0.35"
	}
	return strings.Join([]string{
		"libplacebo=colorspace=bt2020nc",
		"color_primaries=bt2020",
		"color_trc=smpte2084",
		"range=tv",
		"inverse_tonemapping=true",
		"tonemapping=spline",
		"tonemapping_param=" + toneMappingParam,
		"gamut_mode=perceptual",
		"contrast_recovery=" + contrastRecovery,
	}, ":")
}

func appendTaterHDREnhancementFilter(filters, strength string) string {
	hdrFilter := taterHDREnhancementFilter(strength)
	if strings.TrimSpace(filters) == "" {
		return hdrFilter
	}
	// Hardware encoders append their required upload pixel format to the scaler.
	// Keep HDR conversion before that boundary so the encoder receives Main 10
	// frames rather than asking libplacebo to consume hardware-uploaded frames.
	for _, suffix := range []string{",format=nv12,hwupload", ",format=nv12"} {
		if strings.HasSuffix(filters, suffix) {
			return strings.TrimSuffix(filters, suffix) + "," + hdrFilter + suffix
		}
	}
	return filters + "," + hdrFilter
}

func probeTaterHDREnhancement(parent context.Context, ffmpegPath, strength string) error {
	ffmpegPath = effectiveFFmpegPath(ffmpegPath)
	strength = cleanTaterHDREnhancementStrength(strength)
	cacheKey := ffmpegPath + "\x00" + strength

	taterHDREnhancementProbes.Lock()
	defer taterHDREnhancementProbes.Unlock()
	if cached, ok := taterHDREnhancementProbes.byFFmpeg[cacheKey]; ok &&
		(cached.available || time.Since(cached.checkedAt) < 5*time.Minute) {
		if cached.available {
			return nil
		}
		return fmt.Errorf("%s", cached.details)
	}

	result := taterHDREnhancementProbeResult{checkedAt: time.Now()}
	if !taterTVFFmpegHasFilter(parent, ffmpegPath, "libplacebo") {
		result.details = "the configured FFmpeg build does not include libplacebo"
	} else {
		filter := taterHDREnhancementFilter(strength) + ",format=yuv420p10le"
		if err := probeTaterUpscalingFilter(parent, ffmpegPath, filter); err != nil {
			result.details = conciseUpscalingProbeError(err)
		} else {
			result.available = true
		}
	}
	taterHDREnhancementProbes.byFFmpeg[cacheKey] = result
	if result.available {
		return nil
	}
	return fmt.Errorf("%s", result.details)
}

func resolveTaterHDREnhancement(parent context.Context, ffmpegPath string, r *http.Request) string {
	strength := requestedTaterHDREnhancement(r)
	if strength == "" {
		return ""
	}
	if err := probeTaterHDREnhancement(parent, ffmpegPath, strength); err != nil {
		slog.WarnContext(parent, "HDR enhancement is unavailable; falling back to SDR playback",
			"strength", strength, "error", err)
		return ""
	}
	return strength
}

func clearTaterHDREnhancementProbeCache(ffmpegPath string) {
	ffmpegPath = effectiveFFmpegPath(ffmpegPath)
	prefix := ffmpegPath + "\x00"
	taterHDREnhancementProbes.Lock()
	defer taterHDREnhancementProbes.Unlock()
	for key := range taterHDREnhancementProbes.byFFmpeg {
		if strings.HasPrefix(key, prefix) {
			delete(taterHDREnhancementProbes.byFFmpeg, key)
		}
	}
}
