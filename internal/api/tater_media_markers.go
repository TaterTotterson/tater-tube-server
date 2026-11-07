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
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

const (
	taterMediaMarkerSchema     = 1
	taterMediaMarkerSampleSize = int64(1024 * 1024)
	taterMediaMarkerMaxErrors  = 50
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
	args := []string{
		"-hide_banner", "-nostdin", "-loglevel", "info", "-threads", "1",
		"-i", mediaPath,
		"-map", "0:v:0",
		"-vf", "scale=320:-2,blackdetect=d=0.15:pic_th=0.97:pix_th=0.10",
	}
	if taterMediaMarkerHasAudio(probe) {
		args = append(args, "-map", "0:a:0", "-af", "silencedetect=n=-45dB:d=0.15")
	}
	args = append(args, "-sn", "-dn", "-f", "null", "-")
	var output bytes.Buffer
	command := exec.CommandContext(ctx, ffmpegPath, args...)
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return taterMediaMarkerSidecar{}, fmt.Errorf("FFmpeg marker scan failed: %s", conciseUpscalingProbeError(errors.New(output.String())))
	}
	black, silence := parseTaterMarkerIntervals(output.String())
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

func scanTaterMediaMarkers(
	ctx context.Context,
	cfg *config.Config,
	index *taterLocalLibraryIndex,
	progressFn func(taterMediaMarkerScanProgress),
) (taterMediaMarkerScanSummary, error) {
	progress := taterMediaMarkerScanProgress{Errors: []string{}}
	if cfg == nil || index == nil {
		return progress, fmt.Errorf("local media index is unavailable")
	}
	for _, file := range index.Files {
		if !strings.EqualFold(file.LibraryType, "music") {
			progress.Total++
		}
	}
	writableDirectories := map[string]error{}
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
			appendTaterMediaMarkerError(&progress, file.Path, err)
			progress.Processed++
			continue
		}
		info, err := os.Stat(mediaPath)
		if err != nil || info.IsDir() {
			if err == nil {
				err = fmt.Errorf("media path is not a file")
			}
			appendTaterMediaMarkerError(&progress, file.Path, err)
			progress.Processed++
			continue
		}
		sidecarPath := taterMediaMarkerSidecarPath(mediaPath)
		if existing, readErr := readTaterMediaMarkerSidecar(sidecarPath); readErr == nil &&
			existing.SourceSizeBytes == info.Size() {
			fingerprint, fingerprintErr := taterMediaSampleFingerprint(mediaPath, info.Size())
			if fingerprintErr == nil && existing.SourceSampleSHA256 == fingerprint {
				progress.Reused++
				progress.CommercialBreaks += len(existing.CommercialBreaks)
				if existing.Credits != nil {
					progress.Credits++
				}
				progress.Processed++
				progress.Message = "Reused markers for " + filepath.Base(mediaPath)
				if progressFn != nil {
					progressFn(progress)
				}
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
			appendTaterMediaMarkerError(&progress, file.Path, writeErr)
			progress.Processed++
			progress.Message = "Skipped " + filepath.Base(mediaPath) + ": no sidecar write access"
			if progressFn != nil {
				progressFn(progress)
			}
			continue
		}

		progress.Message = "Analyzing " + filepath.Base(mediaPath)
		if progressFn != nil {
			progressFn(progress)
		}
		sidecar, scanErr := scanTaterMediaMarkerFile(ctx, cfg, mediaPath, info, file.DurationSeconds)
		if scanErr == nil {
			scanErr = writeTaterJSON(sidecarPath, sidecar)
		}
		if scanErr != nil {
			appendTaterMediaMarkerError(&progress, file.Path, scanErr)
		} else {
			progress.Sidecars++
			progress.CommercialBreaks += len(sidecar.CommercialBreaks)
			if sidecar.Credits != nil {
				progress.Credits++
			}
		}
		progress.Processed++
		if progressFn != nil {
			progressFn(progress)
		}
	}
	sort.Strings(progress.Errors)
	return progress, nil
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
