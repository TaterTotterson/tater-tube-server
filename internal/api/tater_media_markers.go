package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
)

const (
	taterMediaMarkerSchema            = 1
	taterMediaMarkerSampleSize        = int64(1024 * 1024)
	taterMediaMarkerMaxErrors         = 50
	taterMediaMarkerMaxWorkers        = 2
	taterMediaMarkerPlaybackPollDelay = 2 * time.Second
)

type taterMediaMarker struct {
	StartSeconds float64  `json:"start_seconds"`
	EndSeconds   float64  `json:"end_seconds,omitempty"`
	Confidence   float64  `json:"confidence"`
	Evidence     []string `json:"evidence,omitempty"`
}

type taterMediaMarkerSidecar struct {
	Schema             int                `json:"schema"`
	Scanner            string             `json:"scanner"`
	ScannedAt          time.Time          `json:"scanned_at"`
	SourceSizeBytes    int64              `json:"source_size_bytes"`
	SourceSampleSHA256 string             `json:"source_sample_sha256"`
	DurationSeconds    float64            `json:"duration_seconds"`
	CommercialBreaks   []taterMediaMarker `json:"commercial_breaks"`
	Credits            *taterMediaMarker  `json:"credits,omitempty"`
}

type taterMediaMarkerInterval struct {
	Start float64
	End   float64
}

type taterMediaMarkerChapter struct {
	StartTime string            `json:"start_time"`
	EndTime   string            `json:"end_time"`
	Tags      map[string]string `json:"tags"`
}

type taterMediaMarkerProbe struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
	} `json:"streams"`
	Chapters []taterMediaMarkerChapter `json:"chapters"`
	Format   struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

type taterMediaMarkerScanProgress struct {
	Processed        int
	Total            int
	Sidecars         int
	Reused           int
	Failed           int
	CommercialBreaks int
	Credits          int
	Errors           []string
	Message          string
}

type taterMediaMarkerScanSummary = taterMediaMarkerScanProgress

type taterMediaMarkerStatus struct {
	Enabled          bool      `json:"enabled"`
	Running          bool      `json:"running"`
	WaitingPlayback  bool      `json:"waiting_for_playback"`
	WaitingOptimizer bool      `json:"waiting_for_library_optimizer"`
	State            string    `json:"state"`
	Message          string    `json:"message,omitempty"`
	StartedAt        time.Time `json:"started_at,omitempty"`
	FinishedAt       time.Time `json:"finished_at,omitempty"`
	CurrentFile      string    `json:"current_file,omitempty"`
	LibraryVideos    int       `json:"library_videos"`
	Completed        int       `json:"completed"`
	Queued           int       `json:"queued"`
	Checked          int       `json:"checked"`
	Created          int       `json:"created"`
	Reused           int       `json:"reused"`
	Failed           int       `json:"failed"`
	CommercialBreaks int       `json:"commercial_breaks"`
	Credits          int       `json:"credits"`
	Errors           []string  `json:"errors,omitempty"`
}

type taterMediaMarkerInventory struct {
	Total            int
	Completed        int
	CommercialBreaks int
	Credits          int
}

type taterMediaMarkerService struct {
	server *Server
	wake   chan struct{}

	mu            sync.RWMutex
	status        taterMediaMarkerStatus
	pendingAll    bool
	pendingForce  bool
	pendingKeys   map[string]struct{}
	currentCancel context.CancelFunc
}

func newTaterMediaMarkerService(server *Server) *taterMediaMarkerService {
	return &taterMediaMarkerService{
		server:      server,
		wake:        make(chan struct{}, 1),
		status:      taterMediaMarkerStatus{State: "idle", Message: "Waiting for the local library"},
		pendingKeys: map[string]struct{}{},
	}
}

func taterMediaMarkersEnabled(cfg *config.Config) bool {
	return cfg != nil && taterLocalMediaEnabled(cfg) &&
		(cfg.MediaMarkers.Enabled == nil || *cfg.MediaMarkers.Enabled)
}

func taterMediaMarkersPauseDuringPlayback(cfg *config.Config) bool {
	return cfg == nil || cfg.MediaMarkers.PauseDuringPlayback == nil ||
		*cfg.MediaMarkers.PauseDuringPlayback
}

func (service *taterMediaMarkerService) snapshot() taterMediaMarkerStatus {
	if service == nil {
		return taterMediaMarkerStatus{State: "unavailable", Message: "Media marker service is unavailable"}
	}
	service.mu.RLock()
	status := service.status
	status.Errors = append([]string(nil), service.status.Errors...)
	service.mu.RUnlock()
	if service.server != nil && service.server.configManager != nil {
		status.Enabled = taterMediaMarkersEnabled(service.server.configManager.GetConfig())
	}
	return status
}

func (service *taterMediaMarkerService) signal() {
	select {
	case service.wake <- struct{}{}:
	default:
	}
}

func (service *taterMediaMarkerService) requestAll(force bool) {
	if service == nil {
		return
	}
	service.mu.Lock()
	service.pendingAll = true
	service.pendingForce = service.pendingForce || force
	service.pendingKeys = map[string]struct{}{}
	service.mu.Unlock()
	service.signal()
}

func (service *taterMediaMarkerService) requestFiles(keys []string) {
	if service == nil || len(keys) == 0 {
		return
	}
	service.mu.Lock()
	if !service.pendingAll {
		for _, key := range keys {
			if key = strings.TrimSpace(key); key != "" {
				service.pendingKeys[key] = struct{}{}
			}
		}
	}
	service.mu.Unlock()
	service.signal()
}

func (service *taterMediaMarkerService) configChanged(cfg *config.Config) {
	if service == nil {
		return
	}
	if !taterMediaMarkersEnabled(cfg) {
		service.mu.Lock()
		if service.currentCancel != nil {
			service.currentCancel()
		}
		service.mu.Unlock()
		service.signal()
		return
	}
	service.requestAll(false)
}

func (service *taterMediaMarkerService) takeRequest(defaultAll bool) (bool, bool, map[string]struct{}) {
	service.mu.Lock()
	defer service.mu.Unlock()
	all := defaultAll || service.pendingAll
	force := service.pendingForce
	keys := make(map[string]struct{}, len(service.pendingKeys))
	for key := range service.pendingKeys {
		keys[key] = struct{}{}
	}
	service.pendingAll = false
	service.pendingForce = false
	service.pendingKeys = map[string]struct{}{}
	return all, force, keys
}

func taterMediaMarkerChangedKeys(before, after taterLocalLibraryIndex) []string {
	previous := make(map[string]taterLocalLibraryFileIndex, len(before.Files))
	for _, file := range before.Files {
		previous[file.Key] = file
	}
	keys := make([]string, 0)
	for _, file := range after.Files {
		if strings.EqualFold(file.LibraryType, "music") {
			continue
		}
		old, ok := previous[file.Key]
		if !ok || old.SizeBytes != file.SizeBytes || old.ModifiedUnixNano != file.ModifiedUnixNano {
			keys = append(keys, file.Key)
		}
	}
	sort.Strings(keys)
	return keys
}

func taterMediaMarkerBatch(index taterLocalLibraryIndex, all bool, keys map[string]struct{}) taterLocalLibraryIndex {
	batch := index
	batch.Files = make([]taterLocalLibraryFileIndex, 0, len(index.Files))
	for _, file := range index.Files {
		if strings.EqualFold(file.LibraryType, "music") {
			continue
		}
		if all {
			batch.Files = append(batch.Files, file)
			continue
		}
		if _, ok := keys[file.Key]; ok {
			batch.Files = append(batch.Files, file)
		}
	}
	return batch
}

func taterMediaMarkerLibraryInventory(
	cfg *config.Config,
	index taterLocalLibraryIndex,
	excluded map[string]struct{},
) taterMediaMarkerInventory {
	inventory := taterMediaMarkerInventory{}
	for _, file := range index.Files {
		if strings.EqualFold(file.LibraryType, "music") {
			continue
		}
		inventory.Total++
		if _, skip := excluded[file.Key]; skip {
			continue
		}
		mediaPath, err := taterMediaMarkerAbsolutePath(cfg, file)
		if err != nil {
			continue
		}
		sidecar, err := readTaterMediaMarkerSidecar(taterMediaMarkerSidecarPath(mediaPath))
		if err != nil || sidecar.SourceSizeBytes != file.SizeBytes {
			continue
		}
		inventory.Completed++
		inventory.CommercialBreaks += len(sidecar.CommercialBreaks)
		if sidecar.Credits != nil {
			inventory.Credits++
		}
	}
	return inventory
}

var (
	taterBlackDetectPattern  = regexp.MustCompile(`black_start:([0-9.]+)\s+black_end:([0-9.]+)\s+black_duration:([0-9.]+)`)
	taterSilenceStartPattern = regexp.MustCompile(`silence_start:\s*([0-9.]+)`)
	taterSilenceEndPattern   = regexp.MustCompile(`silence_end:\s*([0-9.]+)`)
)

func taterMediaMarkerSidecarPath(mediaPath string) string {
	ext := filepath.Ext(mediaPath)
	return strings.TrimSuffix(mediaPath, ext) + ".tater-markers.json"
}

func taterMediaMarkerValues(sidecar taterMediaMarkerSidecar) ([]float64, float64) {
	breaks := make([]float64, 0, len(sidecar.CommercialBreaks))
	for _, marker := range sidecar.CommercialBreaks {
		if marker.StartSeconds > 0 {
			breaks = append(breaks, marker.StartSeconds)
		}
	}
	creditsStart := 0.0
	if sidecar.Credits != nil && sidecar.Credits.StartSeconds > 0 {
		creditsStart = sidecar.Credits.StartSeconds
	}
	return breaks, creditsStart
}

func readTaterMediaMarkerSidecar(path string) (taterMediaMarkerSidecar, error) {
	var sidecar taterMediaMarkerSidecar
	raw, err := os.ReadFile(path)
	if err != nil {
		return sidecar, err
	}
	if err := json.Unmarshal(raw, &sidecar); err != nil {
		return sidecar, err
	}
	if sidecar.Schema != taterMediaMarkerSchema {
		return taterMediaMarkerSidecar{}, fmt.Errorf("unsupported media marker schema %d", sidecar.Schema)
	}
	if sidecar.CommercialBreaks == nil {
		sidecar.CommercialBreaks = []taterMediaMarker{}
	}
	return sidecar, nil
}

func taterMediaMarkersForIndexedFile(
	category config.LocalMediaCategory,
	file taterLocalLibraryFileIndex,
) ([]float64, float64) {
	roots := taterLocalMediaCategoryPaths(category)
	if file.SourceIndex < 0 || file.SourceIndex >= len(roots) {
		return nil, 0
	}
	mediaPath, err := safeLocalPath(roots[file.SourceIndex], file.Path)
	if err != nil {
		return nil, 0
	}
	info, err := os.Stat(mediaPath)
	if err != nil || info.IsDir() {
		return nil, 0
	}
	sidecar, err := readTaterMediaMarkerSidecar(taterMediaMarkerSidecarPath(mediaPath))
	if err != nil || sidecar.SourceSizeBytes != info.Size() {
		return nil, 0
	}
	return taterMediaMarkerValues(sidecar)
}

func checkTaterMediaMarkerWriteAccess(mediaPath string) error {
	directory := filepath.Dir(mediaPath)
	testFile, err := os.CreateTemp(directory, ".tater-marker-write-test-*")
	if err != nil {
		return fmt.Errorf("no write access to %s: %w", directory, err)
	}
	name := testFile.Name()
	if closeErr := testFile.Close(); closeErr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("cannot finish a sidecar write in %s: %w", directory, closeErr)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("cannot clean up a sidecar write test in %s: %w", directory, err)
	}
	return nil
}

func taterMediaSampleFingerprint(path string, size int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	_, _ = io.WriteString(hash, strconv.FormatInt(size, 10))
	_, _ = io.WriteString(hash, "\x00")
	if _, err := io.CopyN(hash, file, min(size, taterMediaMarkerSampleSize)); err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if size > taterMediaMarkerSampleSize {
		start := max(int64(0), size-taterMediaMarkerSampleSize)
		if _, err := file.Seek(start, io.SeekStart); err != nil {
			return "", err
		}
		if _, err := io.Copy(hash, io.LimitReader(file, taterMediaMarkerSampleSize)); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func taterMediaMarkerProbeFile(ctx context.Context, cfg *config.Config, mediaPath string) (taterMediaMarkerProbe, error) {
	var probe taterMediaMarkerProbe
	ffprobePath := effectiveFFprobePath(effectiveFFmpegPath(cfg.Transcoding.FFmpegPath))
	if ffprobePath == "" {
		return probe, fmt.Errorf("FFprobe is unavailable")
	}
	command := exec.CommandContext(ctx, ffprobePath,
		"-v", "error",
		"-show_entries", "format=duration:stream=codec_type:chapter=start_time,end_time:chapter_tags=title",
		"-of", "json",
		mediaPath,
	)
	output, err := command.Output()
	if err != nil {
		return probe, fmt.Errorf("read chapters: %w", err)
	}
	if err := json.Unmarshal(output, &probe); err != nil {
		return probe, fmt.Errorf("decode chapter data: %w", err)
	}
	return probe, nil
}

func taterMediaMarkerHasAudio(probe taterMediaMarkerProbe) bool {
	for _, stream := range probe.Streams {
		if strings.EqualFold(stream.CodecType, "audio") {
			return true
		}
	}
	return false
}

func parseTaterMarkerIntervals(output string) ([]taterMediaMarkerInterval, []taterMediaMarkerInterval) {
	black := []taterMediaMarkerInterval{}
	for _, match := range taterBlackDetectPattern.FindAllStringSubmatch(output, -1) {
		if len(match) < 4 {
			continue
		}
		start, startErr := strconv.ParseFloat(match[1], 64)
		end, endErr := strconv.ParseFloat(match[2], 64)
		if startErr == nil && endErr == nil && end > start {
			black = append(black, taterMediaMarkerInterval{Start: start, End: end})
		}
	}
	silence := []taterMediaMarkerInterval{}
	starts := taterSilenceStartPattern.FindAllStringSubmatch(output, -1)
	ends := taterSilenceEndPattern.FindAllStringSubmatch(output, -1)
	for index := 0; index < min(len(starts), len(ends)); index++ {
		start, startErr := strconv.ParseFloat(starts[index][1], 64)
		end, endErr := strconv.ParseFloat(ends[index][1], 64)
		if startErr == nil && endErr == nil && end > start {
			silence = append(silence, taterMediaMarkerInterval{Start: start, End: end})
		}
	}
	return black, silence
}

func taterMarkerIntervalsTouch(a, b taterMediaMarkerInterval, tolerance float64) bool {
	return a.Start <= b.End+tolerance && b.Start <= a.End+tolerance
}

func taterMarkerHasSilence(marker taterMediaMarkerInterval, silence []taterMediaMarkerInterval) bool {
	for _, interval := range silence {
		if taterMarkerIntervalsTouch(marker, interval, 0.4) {
			return true
		}
	}
	return false
}

func taterChapterCreditsMarker(chapters []taterMediaMarkerChapter, duration float64) *taterMediaMarker {
	for _, chapter := range chapters {
		title := strings.ToLower(strings.TrimSpace(chapter.Tags["title"]))
		if !strings.Contains(title, "credit") && !strings.Contains(title, "outro") &&
			!strings.Contains(title, "end title") {
			continue
		}
		start, err := strconv.ParseFloat(chapter.StartTime, 64)
		if err != nil || start <= duration*0.4 || start >= duration {
			continue
		}
		end, _ := strconv.ParseFloat(chapter.EndTime, 64)
		return &taterMediaMarker{
			StartSeconds: start, EndSeconds: end, Confidence: 1,
			Evidence: []string{"embedded chapter: " + strings.TrimSpace(chapter.Tags["title"])},
		}
	}
	return nil
}

func detectTaterCredits(
	chapters []taterMediaMarkerChapter,
	black []taterMediaMarkerInterval,
	silence []taterMediaMarkerInterval,
	duration float64,
) *taterMediaMarker {
	if marker := taterChapterCreditsMarker(chapters, duration); marker != nil {
		return marker
	}
	if duration <= 0 {
		return nil
	}
	window := min(600.0, max(90.0, duration*0.15))
	windowStart := duration - window
	for _, interval := range black {
		blackDuration := interval.End - interval.Start
		remaining := duration - interval.Start
		if interval.Start < windowStart || remaining < 15 || remaining > window || blackDuration < 8 {
			continue
		}
		return &taterMediaMarker{
			StartSeconds: interval.Start, EndSeconds: duration, Confidence: 0.86,
			Evidence: []string{"sustained dark credit sequence near the end"},
		}
	}
	for _, interval := range black {
		remaining := duration - interval.End
		if interval.Start < windowStart || remaining < 20 || remaining > window ||
			interval.End-interval.Start < 0.25 || !taterMarkerHasSilence(interval, silence) {
			continue
		}
		return &taterMediaMarker{
			StartSeconds: interval.End, EndSeconds: duration, Confidence: 0.68,
			Evidence: []string{"dark and silent transition near the end"},
		}
	}
	return nil
}

func detectTaterCommercialBreaks(
	black []taterMediaMarkerInterval,
	silence []taterMediaMarkerInterval,
	duration float64,
	credits *taterMediaMarker,
) []taterMediaMarker {
	if duration < 1200 {
		return []taterMediaMarker{}
	}
	endLimit := duration - 120
	if credits != nil && credits.StartSeconds > 0 {
		endLimit = min(endLimit, credits.StartSeconds-30)
	}
	markers := []taterMediaMarker{}
	last := -600.0
	for _, interval := range black {
		blackDuration := interval.End - interval.Start
		point := (interval.Start + interval.End) / 2
		if point < max(120, duration*0.05) || point > endLimit || blackDuration < 0.15 || blackDuration > 4 ||
			point-last < 300 || !taterMarkerHasSilence(interval, silence) {
			continue
		}
		markers = append(markers, taterMediaMarker{
			StartSeconds: point, Confidence: 0.9,
			Evidence: []string{"brief dark and silent transition"},
		})
		last = point
		if len(markers) >= 8 {
			break
		}
	}
	return markers
}

func scanTaterMediaMarkerFile(
	ctx context.Context,
	cfg *config.Config,
	mediaPath string,
	info os.FileInfo,
	knownDuration float64,
) (taterMediaMarkerSidecar, error) {
	probe, err := taterMediaMarkerProbeFile(ctx, cfg, mediaPath)
	if err != nil {
		return taterMediaMarkerSidecar{}, err
	}
	duration, _ := strconv.ParseFloat(probe.Format.Duration, 64)
	if duration <= 0 {
		duration = knownDuration
	}
	ffmpegPath := effectiveFFmpegPath(cfg.Transcoding.FFmpegPath)
	hardwareArgs := taterMediaMarkerHardwareDecodeArgs(cfg)
	output, scanErr := runTaterMediaMarkerFFmpeg(ctx, ffmpegPath, mediaPath, taterMediaMarkerHasAudio(probe), hardwareArgs)
	if scanErr != nil && len(hardwareArgs) > 0 && ctx.Err() == nil {
		// Hardware decoding is an acceleration only. Unsupported codecs, drivers,
		// containers, or filters must never prevent a portable marker sidecar from
		// being created, so retry the same file with software decoding.
		output, scanErr = runTaterMediaMarkerFFmpeg(ctx, ffmpegPath, mediaPath, taterMediaMarkerHasAudio(probe), nil)
	}
	if scanErr != nil {
		return taterMediaMarkerSidecar{}, scanErr
	}
	black, silence := parseTaterMarkerIntervals(output)
	credits := detectTaterCredits(probe.Chapters, black, silence, duration)
	breaks := detectTaterCommercialBreaks(black, silence, duration, credits)
	fingerprint, err := taterMediaSampleFingerprint(mediaPath, info.Size())
	if err != nil {
		return taterMediaMarkerSidecar{}, fmt.Errorf("fingerprint media: %w", err)
	}
	return taterMediaMarkerSidecar{
		Schema: taterMediaMarkerSchema, Scanner: "Tater Tube media markers v1",
		ScannedAt: time.Now().UTC(), SourceSizeBytes: info.Size(),
		SourceSampleSHA256: fingerprint, DurationSeconds: duration,
		CommercialBreaks: breaks, Credits: credits,
	}, nil
}

func taterMediaMarkerHardwareDecodeArgs(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	accel := strings.ToLower(strings.TrimSpace(cfg.Transcoding.HardwareAcceleration))
	switch accel {
	case "nvenc":
		accel = "cuda"
	case "videotoolbox", "qsv", "vaapi", "auto":
	case "none", "software", "", "v4l2m2m":
		return nil
	default:
		return nil
	}
	args := []string{"-hwaccel", accel}
	device := strings.TrimSpace(cfg.Transcoding.HardwareDevice)
	if device != "" && (accel == "cuda" || accel == "qsv" || accel == "vaapi") {
		args = append(args, "-hwaccel_device", device)
	}
	return args
}

func runTaterMediaMarkerFFmpeg(
	ctx context.Context,
	ffmpegPath string,
	mediaPath string,
	hasAudio bool,
	decodeArgs []string,
) (string, error) {
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "info", "-threads", "2"}
	args = append(args, decodeArgs...)
	args = append(args,
		"-i", mediaPath,
		"-map", "0:v:0",
		"-vf", "scale=320:-2,blackdetect=d=0.15:pic_th=0.97:pix_th=0.10",
	)
	if hasAudio {
		args = append(args, "-map", "0:a:0", "-af", "silencedetect=n=-45dB:d=0.15")
	}
	args = append(args, "-sn", "-dn", "-f", "null", "-")
	var output bytes.Buffer
	command := exec.CommandContext(ctx, ffmpegPath, args...)
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		detail := strings.TrimSpace(output.String())
		if detail == "" {
			detail = err.Error()
		}
		return output.String(), fmt.Errorf(
			"FFmpeg marker scan failed: %s",
			conciseUpscalingProbeError(errors.New(detail)),
		)
	}
	return output.String(), nil
}

func taterMediaMarkerAbsolutePath(cfg *config.Config, file taterLocalLibraryFileIndex) (string, error) {
	categoryID := strings.TrimSpace(file.CategoryID)
	for _, category := range cfg.LocalMedia.Categories {
		if strings.TrimSpace(category.ID) != categoryID {
			continue
		}
		roots := taterLocalMediaCategoryPaths(category)
		if file.SourceIndex < 0 || file.SourceIndex >= len(roots) {
			return "", fmt.Errorf("library source %d is unavailable", file.SourceIndex)
		}
		return safeLocalPath(roots[file.SourceIndex], file.Path)
	}
	return "", fmt.Errorf("library %s is unavailable", categoryID)
}

func appendTaterMediaMarkerError(progress *taterMediaMarkerScanProgress, path string, err error) {
	progress.Failed++
	if len(progress.Errors) < taterMediaMarkerMaxErrors {
		progress.Errors = append(progress.Errors, fmt.Sprintf("%s: %v", path, err))
	}
}

type taterMediaMarkerScanOptions struct {
	Workers            int
	PlaybackActive     func() bool
	PlaybackWaiting    func(bool)
	TryStartProcessing func() bool
	FinishProcessing   func()
	ProcessingWaiting  func(bool)
}

type taterMediaMarkerScanJob struct {
	File        taterLocalLibraryFileIndex
	MediaPath   string
	SidecarPath string
	Info        os.FileInfo
}

func scanTaterMediaMarkers(
	ctx context.Context,
	cfg *config.Config,
	index *taterLocalLibraryIndex,
	progressFn func(taterMediaMarkerScanProgress),
) (taterMediaMarkerScanSummary, error) {
	return scanTaterMediaMarkersWithOptions(ctx, cfg, index, taterMediaMarkerScanOptions{Workers: 1}, progressFn)
}

func scanTaterMediaMarkersWithOptions(
	ctx context.Context,
	cfg *config.Config,
	index *taterLocalLibraryIndex,
	options taterMediaMarkerScanOptions,
	progressFn func(taterMediaMarkerScanProgress),
) (taterMediaMarkerScanSummary, error) {
	progress := taterMediaMarkerScanProgress{Errors: []string{}}
	if cfg == nil || index == nil {
		return progress, fmt.Errorf("local media index is unavailable")
	}
	preparationSlot := false
	if options.TryStartProcessing != nil {
		for !options.TryStartProcessing() {
			if options.ProcessingWaiting != nil {
				options.ProcessingWaiting(true)
			}
			select {
			case <-ctx.Done():
				if options.ProcessingWaiting != nil {
					options.ProcessingWaiting(false)
				}
				return progress, ctx.Err()
			case <-time.After(taterMediaMarkerPlaybackPollDelay):
			}
		}
		preparationSlot = true
		if options.ProcessingWaiting != nil {
			options.ProcessingWaiting(false)
		}
	}
	defer func() {
		if preparationSlot && options.FinishProcessing != nil {
			options.FinishProcessing()
		}
	}()
	var progressMu sync.Mutex
	report := func(update func(*taterMediaMarkerScanProgress)) {
		progressMu.Lock()
		if update != nil {
			update(&progress)
		}
		snapshot := progress
		snapshot.Errors = append([]string(nil), progress.Errors...)
		progressMu.Unlock()
		if progressFn != nil {
			progressFn(snapshot)
		}
	}
	for _, file := range index.Files {
		if !strings.EqualFold(file.LibraryType, "music") {
			progress.Total++
		}
	}
	writableDirectories := map[string]error{}
	jobs := make([]taterMediaMarkerScanJob, 0, progress.Total)
	for fileIndex := range index.Files {
		if err := ctx.Err(); err != nil {
			return progress, err
		}
		file := &index.Files[fileIndex]
		if strings.EqualFold(file.LibraryType, "music") {
			continue
		}
		mediaPath, err := taterMediaMarkerAbsolutePath(cfg, *file)
		if err != nil {
			report(func(progress *taterMediaMarkerScanProgress) {
				appendTaterMediaMarkerError(progress, file.Path, err)
				progress.Processed++
				progress.Message = "Skipped " + filepath.Base(file.Path)
			})
			continue
		}
		info, err := os.Stat(mediaPath)
		if err != nil || info.IsDir() {
			if err == nil {
				err = fmt.Errorf("media path is not a file")
			}
			report(func(progress *taterMediaMarkerScanProgress) {
				appendTaterMediaMarkerError(progress, file.Path, err)
				progress.Processed++
				progress.Message = "Skipped " + filepath.Base(file.Path)
			})
			continue
		}
		sidecarPath := taterMediaMarkerSidecarPath(mediaPath)
		if existing, readErr := readTaterMediaMarkerSidecar(sidecarPath); readErr == nil &&
			existing.SourceSizeBytes == info.Size() {
			fingerprint, fingerprintErr := taterMediaSampleFingerprint(mediaPath, info.Size())
			if fingerprintErr == nil && existing.SourceSampleSHA256 == fingerprint {
				report(func(progress *taterMediaMarkerScanProgress) {
					progress.Reused++
					progress.CommercialBreaks += len(existing.CommercialBreaks)
					if existing.Credits != nil {
						progress.Credits++
					}
					progress.Processed++
					progress.Message = "Reused markers for " + filepath.Base(mediaPath)
				})
				continue
			}
		}

		// The sidecar directory is verified before FFprobe or FFmpeg opens the
		// media for analysis. There is deliberately no /config fallback.
		directory := filepath.Dir(mediaPath)
		writeErr, checked := writableDirectories[directory]
		if !checked {
			writeErr = checkTaterMediaMarkerWriteAccess(mediaPath)
			writableDirectories[directory] = writeErr
		}
		if writeErr != nil {
			report(func(progress *taterMediaMarkerScanProgress) {
				appendTaterMediaMarkerError(progress, file.Path, writeErr)
				progress.Processed++
				progress.Message = "Skipped " + filepath.Base(mediaPath) + ": no sidecar write access"
			})
			continue
		}
		jobs = append(jobs, taterMediaMarkerScanJob{
			File: *file, MediaPath: mediaPath, SidecarPath: sidecarPath, Info: info,
		})
	}
	if preparationSlot && options.FinishProcessing != nil {
		options.FinishProcessing()
		preparationSlot = false
	}

	workers := options.Workers
	if workers <= 0 {
		workers = 1
	}
	workers = min(workers, len(jobs))
	var jobsMu sync.Mutex
	nextJob := 0
	takeJob := func() (taterMediaMarkerScanJob, bool) {
		jobsMu.Lock()
		defer jobsMu.Unlock()
		if nextJob >= len(jobs) {
			return taterMediaMarkerScanJob{}, false
		}
		job := jobs[nextJob]
		nextJob++
		return job, true
	}
	jobsRemaining := func() bool {
		jobsMu.Lock()
		defer jobsMu.Unlock()
		return nextJob < len(jobs)
	}
	newWaitTracker := func(callback func(bool)) func(*bool, bool) {
		var waitingMu sync.Mutex
		waitingWorkers := 0
		return func(waiting *bool, next bool) {
			if *waiting == next {
				return
			}
			waitingMu.Lock()
			wasWaiting := waitingWorkers > 0
			if next {
				waitingWorkers++
			} else {
				waitingWorkers = max(0, waitingWorkers-1)
			}
			isWaiting := waitingWorkers > 0
			*waiting = next
			waitingMu.Unlock()
			if wasWaiting != isWaiting && callback != nil {
				callback(isWaiting)
			}
		}
	}
	setPlaybackWaiting := newWaitTracker(options.PlaybackWaiting)
	setProcessingWaiting := newWaitTracker(options.ProcessingWaiting)

	var workersGroup sync.WaitGroup
	for range workers {
		workersGroup.Add(1)
		go func() {
			defer workersGroup.Done()
			playbackWaiting := false
			processingWaiting := false
			defer setPlaybackWaiting(&playbackWaiting, false)
			defer setProcessingWaiting(&processingWaiting, false)
			for {
				// Match Library Optimization: finish any file already being analyzed,
				// then keep every worker between files until playback stops.
				if options.PlaybackActive != nil {
					for jobsRemaining() && options.PlaybackActive() {
						setPlaybackWaiting(&playbackWaiting, true)
						select {
						case <-ctx.Done():
							return
						case <-time.After(taterMediaMarkerPlaybackPollDelay):
						}
					}
				}
				setPlaybackWaiting(&playbackWaiting, false)

				processingStarted := options.TryStartProcessing == nil
				for jobsRemaining() && !processingStarted {
					processingStarted = options.TryStartProcessing()
					if processingStarted {
						break
					}
					setProcessingWaiting(&processingWaiting, true)
					select {
					case <-ctx.Done():
						return
					case <-time.After(taterMediaMarkerPlaybackPollDelay):
					}
				}
				setProcessingWaiting(&processingWaiting, false)
				job, ok := takeJob()
				if !ok {
					if processingStarted && options.FinishProcessing != nil {
						options.FinishProcessing()
					}
					return
				}
				if ctx.Err() != nil {
					if processingStarted && options.FinishProcessing != nil {
						options.FinishProcessing()
					}
					return
				}
				report(func(progress *taterMediaMarkerScanProgress) {
					progress.Message = "Analyzing " + filepath.Base(job.MediaPath)
				})
				sidecar, scanErr := scanTaterMediaMarkerFile(
					ctx, cfg, job.MediaPath, job.Info, job.File.DurationSeconds,
				)
				if scanErr == nil {
					scanErr = writeTaterJSON(job.SidecarPath, sidecar)
				}
				if processingStarted && options.FinishProcessing != nil {
					options.FinishProcessing()
				}
				if ctx.Err() != nil {
					return
				}
				report(func(progress *taterMediaMarkerScanProgress) {
					if scanErr != nil {
						appendTaterMediaMarkerError(progress, job.File.Path, scanErr)
					} else {
						progress.Sidecars++
						progress.CommercialBreaks += len(sidecar.CommercialBreaks)
						if sidecar.Credits != nil {
							progress.Credits++
						}
					}
					progress.Processed++
					progress.Message = "Checked " + filepath.Base(job.MediaPath)
				})
			}
		}()
	}
	workersGroup.Wait()
	if err := ctx.Err(); err != nil {
		return progress, err
	}
	sort.Strings(progress.Errors)
	return progress, nil
}

func (service *taterMediaMarkerService) run(ctx context.Context) {
	startup := time.NewTimer(30 * time.Second)
	defer startup.Stop()
	initial := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-startup.C:
		case <-service.wake:
		}

		all, force, keys := service.takeRequest(initial)
		initial = false
		cfg := service.server.configManager.GetConfig()
		if !force && !taterMediaMarkersEnabled(cfg) {
			service.mu.Lock()
			service.status.Enabled = false
			service.status.Running = false
			service.status.State = "disabled"
			service.status.Message = "Automatic media marker scanning is off"
			service.status.Queued = 0
			service.status.CurrentFile = ""
			service.mu.Unlock()
			continue
		}
		if !all && len(keys) == 0 {
			continue
		}
		service.scan(ctx, cfg.DeepCopy(), all, keys)
	}
}

func (service *taterMediaMarkerService) scan(
	parent context.Context,
	cfg *config.Config,
	all bool,
	keys map[string]struct{},
) {
	index, err := readTaterLocalLibraryIndex(cfg)
	if err != nil {
		service.mu.Lock()
		service.status.Enabled = taterMediaMarkersEnabled(cfg)
		service.status.Running = false
		service.status.State = "waiting"
		service.status.Message = "Scan the local library before finding breaks and credits"
		service.status.Errors = []string{err.Error()}
		service.mu.Unlock()
		return
	}
	batch := taterMediaMarkerBatch(index, all, keys)
	excluded := make(map[string]struct{}, len(batch.Files))
	for _, file := range batch.Files {
		excluded[file.Key] = struct{}{}
	}
	inventory := taterMediaMarkerLibraryInventory(cfg, index, excluded)
	if len(batch.Files) == 0 {
		service.mu.Lock()
		service.status = taterMediaMarkerStatus{
			Enabled: taterMediaMarkersEnabled(cfg), State: "idle",
			Message:    "Every local video has current media markers",
			FinishedAt: time.Now().UTC(), LibraryVideos: inventory.Total,
			Completed: inventory.Completed, CommercialBreaks: inventory.CommercialBreaks,
			Credits: inventory.Credits, Errors: []string{},
		}
		service.mu.Unlock()
		return
	}

	scanCtx, cancel := context.WithCancel(parent)
	service.mu.Lock()
	service.currentCancel = cancel
	service.status = taterMediaMarkerStatus{
		Enabled: taterMediaMarkersEnabled(cfg), Running: true, State: "scanning",
		Message: "Checking queued videos", StartedAt: time.Now().UTC(),
		LibraryVideos: inventory.Total, Completed: inventory.Completed,
		Queued: len(batch.Files), CommercialBreaks: inventory.CommercialBreaks,
		Credits: inventory.Credits, Errors: []string{},
	}
	service.mu.Unlock()

	summary, scanErr := scanTaterMediaMarkersWithOptions(
		scanCtx,
		cfg,
		&batch,
		taterMediaMarkerScanOptions{
			Workers: taterMediaMarkerMaxWorkers,
			PlaybackActive: func() bool {
				return service.server != nil && service.server.configManager != nil &&
					taterMediaMarkersPauseDuringPlayback(service.server.configManager.GetConfig()) &&
					service.server.streamTracker != nil &&
					service.server.streamTracker.ActiveStreams() > 0
			},
			PlaybackWaiting: func(waiting bool) {
				service.mu.Lock()
				service.status.WaitingPlayback = waiting
				if waiting {
					service.status.State = "waiting"
					service.status.Message = "Paused while Tater Tube playback is active"
					service.status.CurrentFile = ""
				} else if service.status.Running && !service.status.WaitingOptimizer {
					service.status.State = "scanning"
					service.status.Message = "Resuming media marker scanning"
				}
				service.mu.Unlock()
			},
			TryStartProcessing: func() bool {
				return service.server == nil || service.server.processingCoordinator == nil ||
					service.server.processingCoordinator.tryStartMarker()
			},
			FinishProcessing: func() {
				if service.server != nil && service.server.processingCoordinator != nil {
					service.server.processingCoordinator.finishMarker()
				}
			},
			ProcessingWaiting: func(waiting bool) {
				service.mu.Lock()
				service.status.WaitingOptimizer = waiting
				if waiting {
					service.status.State = "waiting"
					service.status.Message = "Paused while Library Optimization is running"
					service.status.CurrentFile = ""
				} else if service.status.Running && !service.status.WaitingPlayback {
					service.status.State = "scanning"
					service.status.Message = "Resuming media marker scanning"
				}
				service.mu.Unlock()
			},
		},
		func(progress taterMediaMarkerScanProgress) {
			service.mu.Lock()
			service.status.Checked = progress.Processed
			service.status.Queued = max(0, progress.Total-progress.Processed)
			service.status.Created = progress.Sidecars
			service.status.Reused = progress.Reused
			service.status.Failed = progress.Failed
			service.status.Completed = inventory.Completed + progress.Sidecars + progress.Reused
			service.status.CommercialBreaks = inventory.CommercialBreaks + progress.CommercialBreaks
			service.status.Credits = inventory.Credits + progress.Credits
			if service.status.WaitingPlayback {
				service.status.State = "waiting"
				service.status.Message = "Paused while Tater Tube playback is active"
				service.status.CurrentFile = ""
			} else if service.status.WaitingOptimizer {
				service.status.State = "waiting"
				service.status.Message = "Paused while Library Optimization is running"
				service.status.CurrentFile = ""
			} else {
				service.status.State = "scanning"
				service.status.Message = progress.Message
				service.status.CurrentFile = progress.Message
			}
			service.status.Errors = append([]string(nil), progress.Errors...)
			service.mu.Unlock()
		},
	)
	cancel()

	service.mu.Lock()
	service.currentCancel = nil
	service.status.Running = false
	service.status.WaitingPlayback = false
	service.status.WaitingOptimizer = false
	service.status.FinishedAt = time.Now().UTC()
	service.status.Checked = summary.Processed
	service.status.Queued = 0
	service.status.Created = summary.Sidecars
	service.status.Reused = summary.Reused
	service.status.Failed = summary.Failed
	service.status.Completed = inventory.Completed + summary.Sidecars + summary.Reused
	service.status.CommercialBreaks = inventory.CommercialBreaks + summary.CommercialBreaks
	service.status.Credits = inventory.Credits + summary.Credits
	service.status.CurrentFile = ""
	service.status.Errors = append([]string(nil), summary.Errors...)
	if scanErr != nil {
		if errors.Is(scanErr, context.Canceled) && !taterMediaMarkersEnabled(service.server.configManager.GetConfig()) {
			service.status.State = "disabled"
			service.status.Message = "Automatic media marker scanning is off"
		} else {
			service.status.State = "error"
			service.status.Message = "Media marker scan stopped: " + scanErr.Error()
		}
	} else if summary.Failed > 0 {
		service.status.State = "warning"
		service.status.Message = fmt.Sprintf(
			"Marker scan finished with %d files skipped", summary.Failed,
		)
	} else {
		service.status.State = "idle"
		service.status.Message = "Media markers are up to date"
	}
	service.mu.Unlock()
}

func (s *Server) handleMediaMarkerStatus(c *fiber.Ctx) error {
	if s.mediaMarkers == nil {
		return RespondServiceUnavailable(c, "Media marker service not available", "MEDIA_MARKERS_UNAVAILABLE")
	}
	return RespondSuccess(c, s.mediaMarkers.snapshot())
}

func (s *Server) handleMediaMarkerScan(c *fiber.Ctx) error {
	if s.mediaMarkers == nil {
		return RespondServiceUnavailable(c, "Media marker service not available", "MEDIA_MARKERS_UNAVAILABLE")
	}
	s.mediaMarkers.requestAll(true)
	return RespondSuccess(c, s.mediaMarkers.snapshot())
}

func runTaterMediaMarkerScan(cfg *config.Config) (taterLocalLibraryIndex, error) {
	index, err := readTaterLocalLibraryIndex(cfg)
	if err != nil {
		err = fmt.Errorf("scan the local library before finding media markers: %w", err)
		updateTaterLocalLibraryScanStatus(cfg, func(status *taterLocalLibraryScanStatus) {
			status.Running = false
			status.Phase = "error"
			status.Error = err.Error()
			status.Message = "Media marker scan could not start"
			status.FinishedAt = time.Now().UTC()
		})
		return index, err
	}
	updateTaterLocalLibraryScanStatus(cfg, func(status *taterLocalLibraryScanStatus) {
		status.Phase = "markers"
		status.Message = "Checking sidecars before analyzing media"
		setTaterLocalLibraryProgress(status, 0, 0)
	})
	summary, err := scanTaterMediaMarkers(context.Background(), cfg, &index, func(progress taterMediaMarkerScanProgress) {
		updateTaterLocalLibraryScanStatus(cfg, func(status *taterLocalLibraryScanStatus) {
			status.MarkersProcessed = progress.Processed
			status.MarkersTotal = progress.Total
			status.MarkerSidecars = progress.Sidecars
			status.MarkerSidecarsReused = progress.Reused
			status.MarkerFilesFailed = progress.Failed
			status.CommercialBreaksFound = progress.CommercialBreaks
			status.CreditsFound = progress.Credits
			status.MarkerErrors = append([]string(nil), progress.Errors...)
			status.Message = progress.Message
			setTaterLocalLibraryProgress(status, progress.Processed, progress.Total)
		})
	})
	finishedAt := time.Now().UTC()
	updateTaterLocalLibraryScanStatus(cfg, func(status *taterLocalLibraryScanStatus) {
		status.Running = false
		status.FinishedAt = finishedAt
		status.MarkersProcessed = summary.Processed
		status.MarkersTotal = summary.Total
		status.MarkerSidecars = summary.Sidecars
		status.MarkerSidecarsReused = summary.Reused
		status.MarkerFilesFailed = summary.Failed
		status.CommercialBreaksFound = summary.CommercialBreaks
		status.CreditsFound = summary.Credits
		status.MarkerErrors = append([]string(nil), summary.Errors...)
		if err != nil {
			status.Phase = "error"
			status.Error = err.Error()
			status.Message = "Media marker scan failed"
			return
		}
		status.Phase = "complete"
		status.ProgressCurrent = summary.Total
		status.ProgressTotal = summary.Total
		status.ProgressPercent = 100
		status.Message = fmt.Sprintf(
			"Media markers updated: %d sidecars written, %d reused, %d files skipped",
			summary.Sidecars,
			summary.Reused,
			summary.Failed,
		)
	})
	return index, err
}
