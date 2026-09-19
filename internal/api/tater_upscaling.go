package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	taterAIUpscalerShaderName   = "FSRCNNX_x2_8-0-4-1.glsl"
	taterAIUpscalerShaderURL    = "https://github.com/igv/FSRCNN-TensorFlow/releases/download/1.1/FSRCNNX_x2_8-0-4-1.glsl"
	taterAIUpscalerShaderSHA256 = "e800dbc5c1c95185cc82216c597724533ff5f2880179f256eef600f03e8dc2ae"
)

type taterAIUpscalerProbe struct {
	shaderPath string
	err        error
	checkedAt  time.Time
}

var taterAIUpscalerProbes = struct {
	sync.Mutex
	byFFmpeg map[string]taterAIUpscalerProbe
}{byFFmpeg: make(map[string]taterAIUpscalerProbe)}

var taterAIUpscalerWarnings sync.Map
var taterStandardUpscalerWarnings sync.Map

// resolveTaterUpscaler keeps AI upscaling optional and fail-safe. Both Auto
// and AI use FSRCNNX when the configured FFmpeg build can run it; otherwise
// playback continues with the portable Spline path.
func resolveTaterUpscaler(ctx context.Context, ffmpegPath, requested string) (scaler, shaderPath string) {
	switch strings.ToLower(strings.TrimSpace(requested)) {
	case "auto", "ai":
		path, err := prepareTaterAIUpscaler(ctx, ffmpegPath)
		if err == nil {
			return "ai", path
		}
		warningKey := ffmpegPath + "\x00" + err.Error()
		if _, alreadyLogged := taterAIUpscalerWarnings.LoadOrStore(warningKey, struct{}{}); !alreadyLogged {
			slog.WarnContext(ctx, "AI upscaling is unavailable; using Standard upscaling",
				"ffmpeg_path", ffmpegPath,
				"reason", err)
		}
		return resolveTaterStandardUpscaler(ctx, ffmpegPath), ""
	case "spline36", "standard":
		return resolveTaterStandardUpscaler(ctx, ffmpegPath), ""
	default:
		return "", ""
	}
}

// resolveTaterStandardUpscaler keeps Spline as the default on every FFmpeg
// build. zscale provides the preferred Spline36 implementation when libzimg is
// available; swscale's spline implementation is the portable fallback.
func resolveTaterStandardUpscaler(ctx context.Context, ffmpegPath string) string {
	ffmpegPath = effectiveFFmpegPath(ffmpegPath)
	if taterTVFFmpegHasFilter(ctx, ffmpegPath, "zscale") {
		return "spline36"
	}
	if _, alreadyLogged := taterStandardUpscalerWarnings.LoadOrStore(ffmpegPath, struct{}{}); !alreadyLogged {
		slog.WarnContext(ctx, "FFmpeg zscale is unavailable; using portable Spline upscaling",
			"ffmpeg_path", ffmpegPath)
	}
	return "spline"
}

func resolveTaterUpscalerForRequest(ctx context.Context, ffmpegPath string, request *http.Request) (scaler, shaderPath string) {
	requested := requestedTaterScaler(request)
	if requested != "auto" && requested != "ai" {
		return resolveTaterUpscaler(ctx, ffmpegPath, requested)
	}
	if request == nil || cleanTaterVideoRange(request.URL.Query().Get("tater_source_video_range")) != "sdr" {
		return resolveTaterUpscaler(ctx, ffmpegPath, "standard")
	}
	sourceWidth := requestedTaterVideoDimension(request, "tater_source_width")
	sourceHeight := requestedTaterVideoDimension(request, "tater_source_height")
	outputWidth := requestedTaterVideoDimension(request, "tater_output_width")
	outputHeight := requestedTaterVideoDimension(request, "tater_output_height")
	if sourceWidth <= 0 || sourceHeight <= 0 || outputWidth <= sourceWidth || outputHeight <= sourceHeight ||
		sourceWidth > 1920 || sourceHeight > 1080 {
		return resolveTaterUpscaler(ctx, ffmpegPath, "standard")
	}
	widthScale := float64(outputWidth) / float64(sourceWidth)
	heightScale := float64(outputHeight) / float64(sourceHeight)
	if widthScale < 1.3 || heightScale < 1.3 || widthScale > 2.01 || heightScale > 2.01 {
		return resolveTaterUpscaler(ctx, ffmpegPath, "standard")
	}
	return resolveTaterUpscaler(ctx, ffmpegPath, requested)
}

func prepareTaterAIUpscaler(ctx context.Context, ffmpegPath string) (string, error) {
	ffmpegPath = effectiveFFmpegPath(ffmpegPath)
	cacheKey := ffmpegPath

	taterAIUpscalerProbes.Lock()
	defer taterAIUpscalerProbes.Unlock()
	if cached, ok := taterAIUpscalerProbes.byFFmpeg[cacheKey]; ok {
		if cached.err == nil || time.Since(cached.checkedAt) < 5*time.Minute {
			return cached.shaderPath, cached.err
		}
	}

	var shaderPath string
	var err error
	if !taterTVFFmpegHasFilter(ctx, ffmpegPath, "libplacebo") {
		err = fmt.Errorf("the configured FFmpeg build does not include libplacebo")
	} else {
		shaderPath, err = findOrDownloadTaterAIUpscalerShader(ctx)
	}
	if err == nil {
		err = probeTaterAIUpscaler(ctx, ffmpegPath, shaderPath)
	}
	result := taterAIUpscalerProbe{shaderPath: shaderPath, err: err, checkedAt: time.Now()}
	taterAIUpscalerProbes.byFFmpeg[cacheKey] = result
	return result.shaderPath, result.err
}

func findOrDownloadTaterAIUpscalerShader(ctx context.Context) (string, error) {
	if configured := strings.TrimSpace(os.Getenv("TATER_AI_UPSCALER_SHADER")); configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return configured, nil
		}
		return "", fmt.Errorf("TATER_AI_UPSCALER_SHADER does not point to a readable file")
	}

	candidates := []string{
		filepath.Join("/usr/share/tater-tube-server/ai-upscaling", taterAIUpscalerShaderName),
	}
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates,
			filepath.Join(filepath.Dir(executable), "ai-upscaling", taterAIUpscalerShaderName),
		)
	}
	for _, candidate := range candidates {
		if validTaterAIUpscalerShader(candidate) {
			return candidate, nil
		}
	}

	cacheDir := filepath.Join(os.TempDir(), "tater-tube-ai-upscaling", "fsrcnnx-1.1")
	shaderPath := filepath.Join(cacheDir, taterAIUpscalerShaderName)
	if validTaterAIUpscalerShader(shaderPath) {
		return shaderPath, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create AI upscaler cache: %w", err)
	}
	if err := downloadTaterAIUpscalerShader(ctx, shaderPath); err != nil {
		return "", err
	}
	return shaderPath, nil
}

func validTaterAIUpscalerShader(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, 1024*1024)); err != nil {
		return false
	}
	return hex.EncodeToString(hash.Sum(nil)) == taterAIUpscalerShaderSHA256
}

func downloadTaterAIUpscalerShader(ctx context.Context, destination string) error {
	downloadCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, taterAIUpscalerShaderURL, nil)
	if err != nil {
		return fmt.Errorf("prepare AI upscaler download: %w", err)
	}
	req.Header.Set("User-Agent", "Tater-Tube-Server-AI-Upscaling")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download AI upscaler: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download AI upscaler: unexpected HTTP status %s", response.Status)
	}

	temp, err := os.CreateTemp(filepath.Dir(destination), ".fsrcnnx-*.tmp")
	if err != nil {
		return fmt.Errorf("create AI upscaler download: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(response.Body, 1024*1024+1))
	closeErr := temp.Close()
	if copyErr != nil {
		return fmt.Errorf("save AI upscaler: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close AI upscaler download: %w", closeErr)
	}
	if written > 1024*1024 {
		return fmt.Errorf("AI upscaler download exceeded the expected size")
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); actual != taterAIUpscalerShaderSHA256 {
		return fmt.Errorf("AI upscaler checksum did not match")
	}
	if err := os.Chmod(tempPath, 0o644); err != nil {
		return fmt.Errorf("set AI upscaler permissions: %w", err)
	}
	_ = os.Remove(destination)
	if err := os.Rename(tempPath, destination); err != nil {
		return fmt.Errorf("install AI upscaler: %w", err)
	}
	return nil
}

func probeTaterAIUpscaler(parent context.Context, ffmpegPath, shaderPath string) error {
	if _, err := exec.LookPath(ffmpegPath); err != nil {
		return fmt.Errorf("ffmpeg was not found: %w", err)
	}
	probeCtx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	filter := taterAIUpscaleFilter(128, 72, shaderPath)
	cmd := exec.CommandContext(probeCtx, ffmpegPath,
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-f", "lavfi", "-i", "color=size=64x36:rate=1:duration=1",
		"-vf", filter,
		"-frames:v", "1", "-an", "-f", "null", "-",
	)
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = err.Error()
		}
		return fmt.Errorf("FFmpeg libplacebo/Vulkan probe failed: %s", reason)
	}
	return nil
}

func taterAIUpscaleFilter(outputWidth, outputHeight int, shaderPath string) string {
	path := escapeTaterFilterValue(shaderPath)
	return fmt.Sprintf(
		"libplacebo=w=%d:h=%d:format=yuv420p:upscaler=spline36:custom_shader_path=%s",
		outputWidth, outputHeight, path,
	)
}

func escapeTaterFilterValue(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "'", "'\\''")
	return "'" + value + "'"
}
