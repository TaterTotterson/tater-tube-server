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
	defaultTaterAIUpscalerModel = "fsrcnnx-8"
	maxTaterAIUpscalerSize      = 2 * 1024 * 1024
)

type taterAIUpscalerModel struct {
	id             string
	name           string
	shaderName     string
	shaderURL      string
	shaderSHA256   string
	cacheNamespace string
}

var taterAIUpscalerModels = map[string]taterAIUpscalerModel{
	"fsrcnnx-8": {
		id:             "fsrcnnx-8",
		name:           "FSRCNNX Fast",
		shaderName:     "FSRCNNX_x2_8-0-4-1.glsl",
		shaderURL:      "https://github.com/igv/FSRCNN-TensorFlow/releases/download/1.1/FSRCNNX_x2_8-0-4-1.glsl",
		shaderSHA256:   "e800dbc5c1c95185cc82216c597724533ff5f2880179f256eef600f03e8dc2ae",
		cacheNamespace: "fsrcnnx-1.1",
	},
	"fsrcnnx-16": {
		id:             "fsrcnnx-16",
		name:           "FSRCNNX Quality",
		shaderName:     "FSRCNNX_x2_16-0-4-1.glsl",
		shaderURL:      "https://github.com/igv/FSRCNN-TensorFlow/releases/download/1.1/FSRCNNX_x2_16-0-4-1.glsl",
		shaderSHA256:   "d5a24a271e5d9a3f7f7a053b150c460a44c25b3cf7f770857d57cc3a2e1c9965",
		cacheNamespace: "fsrcnnx-1.1",
	},
	"artcnn-c4f16": {
		id:             "artcnn-c4f16",
		name:           "ArtCNN Balanced",
		shaderName:     "ArtCNN_C4F16.glsl",
		shaderURL:      "https://github.com/Artoriuz/ArtCNN/releases/download/v1.6.2/ArtCNN_C4F16.glsl",
		shaderSHA256:   "03d0b3d31cb82c898a94a46663021a3e8f02c5a21d69c5cfdf0208de4bfd453e",
		cacheNamespace: "artcnn-1.6.2",
	},
	"artcnn-c4f16-ds": {
		id:             "artcnn-c4f16-ds",
		name:           "ArtCNN Restore",
		shaderName:     "ArtCNN_C4F16_DS.glsl",
		shaderURL:      "https://github.com/Artoriuz/ArtCNN/releases/download/v1.6.2/ArtCNN_C4F16_DS.glsl",
		shaderSHA256:   "57df650fddec3969e17799f5522c9b03dd2d33b1aeace237fef216bf3858125a",
		cacheNamespace: "artcnn-1.6.2",
	},
	"artcnn-c4f32": {
		id:             "artcnn-c4f32",
		name:           "ArtCNN Quality",
		shaderName:     "ArtCNN_C4F32.glsl",
		shaderURL:      "https://github.com/Artoriuz/ArtCNN/releases/download/v1.6.2/ArtCNN_C4F32.glsl",
		shaderSHA256:   "f773bce6cf5fe7e5e5d599a695edd40df5cd7a20c3d08c4d164d07591d5bead3",
		cacheNamespace: "artcnn-1.6.2",
	},
	"anime4k-cnn-m": {
		id:             "anime4k-cnn-m",
		name:           "Anime4K Balanced",
		shaderName:     "Anime4K_Upscale_CNN_x2_M.glsl",
		shaderURL:      "https://raw.githubusercontent.com/bloc97/Anime4K/v4.0.1/glsl/Upscale/Anime4K_Upscale_CNN_x2_M.glsl",
		shaderSHA256:   "716e02098a68f0d648761f2b96b4dd139e1cb09b174bb369fca3aa34328fff7e",
		cacheNamespace: "anime4k-4.0.1",
	},
	"anime4k-cnn-l": {
		id:             "anime4k-cnn-l",
		name:           "Anime4K Quality",
		shaderName:     "Anime4K_Upscale_CNN_x2_L.glsl",
		shaderURL:      "https://raw.githubusercontent.com/bloc97/Anime4K/v4.0.1/glsl/Upscale/Anime4K_Upscale_CNN_x2_L.glsl",
		shaderSHA256:   "db1fedf7be82f6fd9034e6bf39b64daf2b7576988bb584ec38f24f5236b1cd97",
		cacheNamespace: "anime4k-4.0.1",
	},
}

var taterAIUpscalerModelOrder = []string{
	"fsrcnnx-8",
	"fsrcnnx-16",
	"anime4k-cnn-m",
	"anime4k-cnn-l",
	"artcnn-c4f16",
	"artcnn-c4f16-ds",
	"artcnn-c4f32",
}

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

// resolveTaterUpscaler keeps AI upscaling optional and fail-safe. Selected
// models step through compatible AI fallbacks before playback continues with
// the portable Standard path.
func resolveTaterUpscaler(ctx context.Context, ffmpegPath, requested, requestedModel string) (scaler, shaderPath, resolvedModel string) {
	switch strings.ToLower(strings.TrimSpace(requested)) {
	case "auto", "ai":
		requestedModel = cleanTaterAIUpscalerModel(requestedModel)
		var selectedErr error
		for _, modelID := range taterAIUpscalerFallbackChain(requestedModel) {
			model := taterAIUpscalerModels[modelID]
			path, err := prepareTaterAIUpscaler(ctx, ffmpegPath, model)
			if err == nil {
				if modelID != requestedModel {
					warningKey := ffmpegPath + "\x00" + requestedModel + "\x00" + modelID
					if _, alreadyLogged := taterAIUpscalerWarnings.LoadOrStore(warningKey, struct{}{}); !alreadyLogged {
						slog.WarnContext(ctx, "Selected AI upscaling model is unavailable; using a fallback AI model",
							"requested_model", requestedModel,
							"resolved_model", modelID,
							"reason", selectedErr)
					}
				}
				return "ai", path, modelID
			}
			if selectedErr == nil {
				selectedErr = err
			}
		}
		if selectedErr == nil {
			selectedErr = fmt.Errorf("no compatible AI upscaling model was found")
		}
		warningKey := ffmpegPath + "\x00" + requestedModel + "\x00" + selectedErr.Error()
		if _, alreadyLogged := taterAIUpscalerWarnings.LoadOrStore(warningKey, struct{}{}); !alreadyLogged {
			slog.WarnContext(ctx, "AI upscaling is unavailable; using Standard upscaling",
				"ffmpeg_path", ffmpegPath,
				"requested_model", requestedModel,
				"reason", selectedErr)
		}
		return resolveTaterStandardUpscaler(ctx, ffmpegPath), "", ""
	case "spline36", "standard":
		return resolveTaterStandardUpscaler(ctx, ffmpegPath), "", ""
	default:
		return "", "", ""
	}
}

func cleanTaterAIUpscalerModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if _, ok := taterAIUpscalerModels[model]; ok {
		return model
	}
	return defaultTaterAIUpscalerModel
}

func taterAIUpscalerFallbackChain(model string) []string {
	switch cleanTaterAIUpscalerModel(model) {
	case "artcnn-c4f32":
		return []string{"artcnn-c4f32", "artcnn-c4f16", "fsrcnnx-8"}
	case "artcnn-c4f16-ds":
		return []string{"artcnn-c4f16-ds", "artcnn-c4f16", "fsrcnnx-8"}
	case "artcnn-c4f16":
		return []string{"artcnn-c4f16", "fsrcnnx-8"}
	case "anime4k-cnn-m":
		return []string{"anime4k-cnn-m", "fsrcnnx-8"}
	case "anime4k-cnn-l":
		return []string{"anime4k-cnn-l", "anime4k-cnn-m", "fsrcnnx-8"}
	case "fsrcnnx-16":
		return []string{"fsrcnnx-16", "fsrcnnx-8"}
	default:
		return []string{defaultTaterAIUpscalerModel}
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

func resolveTaterUpscalerForRequest(ctx context.Context, ffmpegPath string, request *http.Request) (scaler, shaderPath, resolvedModel string) {
	requested := requestedTaterScaler(request)
	requestedModel := requestedTaterAIUpscalerModel(request)
	if requested != "auto" && requested != "ai" {
		return resolveTaterUpscaler(ctx, ffmpegPath, requested, requestedModel)
	}
	if request == nil || cleanTaterVideoRange(request.URL.Query().Get("tater_source_video_range")) != "sdr" {
		return resolveTaterUpscaler(ctx, ffmpegPath, "standard", requestedModel)
	}
	sourceWidth := requestedTaterVideoDimension(request, "tater_source_width")
	sourceHeight := requestedTaterVideoDimension(request, "tater_source_height")
	outputWidth := requestedTaterVideoDimension(request, "tater_output_width")
	outputHeight := requestedTaterVideoDimension(request, "tater_output_height")
	if sourceWidth <= 0 || sourceHeight <= 0 || outputWidth <= sourceWidth || outputHeight <= sourceHeight ||
		sourceWidth > 1920 || sourceHeight > 1080 {
		return resolveTaterUpscaler(ctx, ffmpegPath, "standard", requestedModel)
	}
	widthScale := float64(outputWidth) / float64(sourceWidth)
	heightScale := float64(outputHeight) / float64(sourceHeight)
	if widthScale < 1.3 || heightScale < 1.3 || widthScale > 2.01 || heightScale > 2.01 {
		return resolveTaterUpscaler(ctx, ffmpegPath, "standard", requestedModel)
	}
	return resolveTaterUpscaler(ctx, ffmpegPath, requested, requestedModel)
}

func requestedTaterAIUpscalerModel(request *http.Request) string {
	if request == nil || request.URL == nil {
		return defaultTaterAIUpscalerModel
	}
	return cleanTaterAIUpscalerModel(request.URL.Query().Get("tater_ai_model"))
}

func prepareTaterAIUpscaler(ctx context.Context, ffmpegPath string, model taterAIUpscalerModel) (string, error) {
	ffmpegPath = effectiveFFmpegPath(ffmpegPath)
	cacheKey := ffmpegPath + "\x00" + model.id

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
		shaderPath, err = findOrDownloadTaterAIUpscalerShader(ctx, model)
	}
	if err == nil {
		err = probeTaterAIUpscaler(ctx, ffmpegPath, shaderPath)
	}
	result := taterAIUpscalerProbe{shaderPath: shaderPath, err: err, checkedAt: time.Now()}
	taterAIUpscalerProbes.byFFmpeg[cacheKey] = result
	return result.shaderPath, result.err
}

func findOrDownloadTaterAIUpscalerShader(ctx context.Context, model taterAIUpscalerModel) (string, error) {
	if configuredDir := strings.TrimSpace(os.Getenv("TATER_AI_UPSCALER_DIR")); configuredDir != "" {
		configured := filepath.Join(configuredDir, model.shaderName)
		if validTaterAIUpscalerShader(configured, model) {
			return configured, nil
		}
		return "", fmt.Errorf("TATER_AI_UPSCALER_DIR does not contain a valid %s shader", model.name)
	}
	if configured := strings.TrimSpace(os.Getenv("TATER_AI_UPSCALER_SHADER")); configured != "" && model.id == defaultTaterAIUpscalerModel {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			if validTaterAIUpscalerShader(configured, model) {
				return configured, nil
			}
			return "", fmt.Errorf("TATER_AI_UPSCALER_SHADER checksum did not match")
		}
		return "", fmt.Errorf("TATER_AI_UPSCALER_SHADER does not point to a readable file")
	}

	candidates := []string{
		filepath.Join("/usr/share/tater-tube-server/ai-upscaling", model.shaderName),
	}
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates,
			filepath.Join(filepath.Dir(executable), "ai-upscaling", model.shaderName),
		)
	}
	for _, candidate := range candidates {
		if validTaterAIUpscalerShader(candidate, model) {
			return candidate, nil
		}
	}

	cacheDir := filepath.Join(os.TempDir(), "tater-tube-ai-upscaling", model.cacheNamespace)
	shaderPath := filepath.Join(cacheDir, model.shaderName)
	if validTaterAIUpscalerShader(shaderPath, model) {
		return shaderPath, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create AI upscaler cache: %w", err)
	}
	if err := downloadTaterAIUpscalerShader(ctx, shaderPath, model); err != nil {
		return "", err
	}
	return shaderPath, nil
}

func validTaterAIUpscalerShader(path string, model taterAIUpscalerModel) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, maxTaterAIUpscalerSize+1)); err != nil {
		return false
	}
	return hex.EncodeToString(hash.Sum(nil)) == model.shaderSHA256
}

func downloadTaterAIUpscalerShader(ctx context.Context, destination string, model taterAIUpscalerModel) error {
	downloadCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, model.shaderURL, nil)
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

	temp, err := os.CreateTemp(filepath.Dir(destination), ".ai-upscaler-*.tmp")
	if err != nil {
		return fmt.Errorf("create AI upscaler download: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(response.Body, maxTaterAIUpscalerSize+1))
	closeErr := temp.Close()
	if copyErr != nil {
		return fmt.Errorf("save AI upscaler: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close AI upscaler download: %w", closeErr)
	}
	if written > maxTaterAIUpscalerSize {
		return fmt.Errorf("AI upscaler download exceeded the expected size")
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); actual != model.shaderSHA256 {
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
	filter := taterAIUpscaleFilter(128, 72, shaderPath)
	if err := probeTaterUpscalingFilter(parent, ffmpegPath, filter); err != nil {
		return fmt.Errorf("FFmpeg libplacebo/Vulkan probe failed: %s", err)
	}
	return nil
}

func probeTaterUpscalingFilter(parent context.Context, ffmpegPath, filter string) error {
	if _, err := exec.LookPath(ffmpegPath); err != nil {
		return fmt.Errorf("ffmpeg was not found: %w", err)
	}
	probeCtx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
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
		return fmt.Errorf("%s", reason)
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
