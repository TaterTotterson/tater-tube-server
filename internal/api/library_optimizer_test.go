package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/stretchr/testify/assert"
)

func boolPointer(value bool) *bool { return &value }

func TestOptimizerActionSkipsEfficientCodecsByDefault(t *testing.T) {
	cfg := config.LibraryOptimizerConfig{TargetCodec: "hevc", ConvertHEVCToAV1: boolPointer(false)}
	assert.Equal(t, "skip-efficient", optimizerAction("hevc", cfg))
	assert.Equal(t, "skip-efficient", optimizerAction("av1", cfg))
	assert.Equal(t, "transcode", optimizerAction("h264", cfg))

	cfg.TargetCodec = "av1"
	assert.Equal(t, "skip-efficient", optimizerAction("hevc", cfg))
	cfg.ConvertHEVCToAV1 = boolPointer(true)
	assert.Equal(t, "transcode", optimizerAction("hevc", cfg))
	assert.Equal(t, "skip-efficient", optimizerAction("av1", cfg))
}

func TestSelectOptimizerAudioPrefersBestEnglishMainTrack(t *testing.T) {
	probe := optimizerProbe{Streams: []optimizerProbeStream{
		{Index: 1, CodecType: "audio", CodecName: "ac3", Channels: 2, Tags: map[string]string{"language": "eng", "title": "Director Commentary"}, Disposition: optimizerDisposition{Default: 1}},
		{Index: 2, CodecType: "audio", CodecName: "truehd", Channels: 8, Tags: map[string]string{"language": "eng", "title": "Main Audio"}},
		{Index: 3, CodecType: "audio", CodecName: "dts", Channels: 6, Tags: map[string]string{"language": "jpn"}},
	}}

	selected := selectOptimizerAudio(probe, "best_english")
	if assert.Len(t, selected, 1) {
		assert.Equal(t, 2, selected[0].Index)
	}
}

func TestSelectOptimizerSubtitlesKeepsOnlyForcedEnglishByDefault(t *testing.T) {
	probe := optimizerProbe{Streams: []optimizerProbeStream{
		{Index: 4, CodecType: "subtitle", Tags: map[string]string{"language": "eng"}},
		{Index: 5, CodecType: "subtitle", Tags: map[string]string{"language": "eng"}, Disposition: optimizerDisposition{Forced: 1}},
		{Index: 6, CodecType: "subtitle", Tags: map[string]string{"language": "spa"}, Disposition: optimizerDisposition{Forced: 1}},
	}}

	selected := selectOptimizerSubtitles(probe, "forced_english")
	if assert.Len(t, selected, 1) {
		assert.Equal(t, 5, selected[0].Index)
	}
}

func TestCleanupOptimizerQuarantineUsesSafetyWindowDate(t *testing.T) {
	root := t.TempDir()
	oldDate := time.Now().UTC().AddDate(0, 0, -9).Format("2006-01-02")
	recentDate := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	oldFile := filepath.Join(root, ".tater-tube-originals", oldDate, "old.mkv")
	recentFile := filepath.Join(root, ".tater-tube-originals", recentDate, "recent.mkv")
	assert.NoError(t, os.MkdirAll(filepath.Dir(oldFile), 0755))
	assert.NoError(t, os.MkdirAll(filepath.Dir(recentFile), 0755))
	assert.NoError(t, os.WriteFile(oldFile, []byte("old"), 0644))
	assert.NoError(t, os.WriteFile(recentFile, []byte("recent"), 0644))
	days := 7
	enabled := true
	cfg := &config.Config{
		LibraryOptimizer: config.LibraryOptimizerConfig{QuarantineDays: &days},
		LocalMedia: config.LocalMediaConfig{Categories: []config.LocalMediaCategory{{
			ID: "movies", LibraryType: "movies", Paths: []string{root}, Enabled: &enabled,
		}}},
	}

	cleanupOptimizerQuarantine(cfg)

	_, oldErr := os.Stat(oldFile)
	assert.True(t, os.IsNotExist(oldErr))
	_, recentErr := os.Stat(recentFile)
	assert.NoError(t, recentErr)
}

func TestBuildLibraryOptimizerHistorySummarizesOnlyCompletedSavings(t *testing.T) {
	now := time.Now().UTC()
	store := libraryOptimizerStore{Records: map[string]libraryOptimizerRecord{
		"/media/completed.mkv": {
			Path: "/media/completed.mkv", Status: "completed", OriginalBytes: 1000,
			OutputBytes: 600, SavedBytes: 400, UpdatedAt: now,
		},
		"/media/skipped.mkv": {
			Path: "/media/skipped.mkv", Status: "skipped", OriginalBytes: 1000,
			OutputBytes: 900, SavedBytes: 100, UpdatedAt: now.Add(-time.Minute),
		},
		"/media/failed.mkv": {
			Path: "/media/failed.mkv", Status: "failed", OriginalBytes: 1000,
			Message: "encoder failed", UpdatedAt: now.Add(-2 * time.Minute),
		},
	}}

	history := buildLibraryOptimizerHistory(store, "", "", 0, 50)

	assert.Equal(t, 3, history.Summary.Total)
	assert.Equal(t, 1, history.Summary.Completed)
	assert.Equal(t, 1, history.Summary.Skipped)
	assert.Equal(t, 1, history.Summary.Failed)
	assert.Equal(t, int64(400), history.Summary.SavedBytes)
	assert.Equal(t, int64(1000), history.Summary.OriginalBytes)
	assert.Equal(t, int64(600), history.Summary.OutputBytes)
	if assert.Len(t, history.Records, 3) {
		assert.Equal(t, "/media/completed.mkv", history.Records[0].Path)
	}
}

func TestBuildLibraryOptimizerHistoryFiltersSearchesAndPaginates(t *testing.T) {
	now := time.Now().UTC()
	store := libraryOptimizerStore{Records: map[string]libraryOptimizerRecord{
		"/movies/alpha.mkv": {
			Path: "/movies/alpha.mkv", Status: "failed", Message: "No video stream",
			UpdatedAt: now,
		},
		"/movies/beta.mkv": {
			Path: "/movies/beta.mkv", Status: "failed", Message: "Encoder failed",
			UpdatedAt: now.Add(-time.Minute),
		},
		"/movies/gamma.mkv": {
			Path: "/movies/gamma.mkv", Status: "completed", UpdatedAt: now.Add(-2 * time.Minute),
		},
	}}

	history := buildLibraryOptimizerHistory(store, "failed", "video", 0, 1)

	assert.Equal(t, 3, history.Summary.Total)
	assert.Equal(t, 1, history.FilteredTotal)
	if assert.Len(t, history.Records, 1) {
		assert.Equal(t, "/movies/alpha.mkv", history.Records[0].Path)
	}
}
