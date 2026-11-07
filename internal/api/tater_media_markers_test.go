package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaterMediaMarkerSidecarPathLivesBesideMedia(t *testing.T) {
	assert.Equal(t,
		filepath.Join("Library", "Movie (2026).tater-markers.json"),
		taterMediaMarkerSidecarPath(filepath.Join("Library", "Movie (2026).mkv")),
	)
}

func TestParseTaterMarkerIntervals(t *testing.T) {
	output := `
[blackdetect] black_start:599.8 black_end:600.4 black_duration:0.6
[silencedetect] silence_start: 599.7
[silencedetect] silence_end: 600.5 | silence_duration: 0.8
`
	black, silence := parseTaterMarkerIntervals(output)
	require.Len(t, black, 1)
	require.Len(t, silence, 1)
	assert.InDelta(t, 599.8, black[0].Start, 0.001)
	assert.InDelta(t, 600.5, silence[0].End, 0.001)
}

func TestDetectTaterCreditsPrefersEmbeddedChapter(t *testing.T) {
	marker := detectTaterCredits([]taterMediaMarkerChapter{{
		StartTime: "1500.25", EndTime: "1800", Tags: map[string]string{"title": "End Credits"},
	}}, nil, nil, 1800)
	require.NotNil(t, marker)
	assert.InDelta(t, 1500.25, marker.StartSeconds, 0.001)
	assert.Equal(t, 1.0, marker.Confidence)
}

func TestDetectTaterCreditsRecognizesLongDarkTail(t *testing.T) {
	marker := detectTaterCredits(nil, []taterMediaMarkerInterval{{Start: 1700, End: 1798}}, nil, 1800)
	require.NotNil(t, marker)
	assert.InDelta(t, 1700, marker.StartSeconds, 0.001)
	assert.Greater(t, marker.Confidence, 0.8)
}

func TestDetectTaterCommercialBreaksRequiresDarkAndSilentTransition(t *testing.T) {
	black := []taterMediaMarkerInterval{
		{Start: 599.8, End: 600.4},
		{Start: 1199.8, End: 1200.4},
		{Start: 1499.8, End: 1500.4},
	}
	silence := []taterMediaMarkerInterval{
		{Start: 599.7, End: 600.5},
		{Start: 1199.7, End: 1200.5},
	}
	markers := detectTaterCommercialBreaks(black, silence, 1800, nil)
	require.Len(t, markers, 2)
	assert.InDelta(t, 600.1, markers[0].StartSeconds, 0.01)
	assert.InDelta(t, 1200.1, markers[1].StartSeconds, 0.01)
}

func TestTaterMediaMarkersForIndexedFileReadsPortableSidecar(t *testing.T) {
	directory := t.TempDir()
	mediaPath := filepath.Join(directory, "Episode.mkv")
	require.NoError(t, os.WriteFile(mediaPath, []byte("video"), 0644))
	info, err := os.Stat(mediaPath)
	require.NoError(t, err)
	sidecar := taterMediaMarkerSidecar{
		Schema: taterMediaMarkerSchema, SourceSizeBytes: info.Size(),
		CommercialBreaks: []taterMediaMarker{{StartSeconds: 600}, {StartSeconds: 1200}},
		Credits:          &taterMediaMarker{StartSeconds: 1700},
	}
	require.NoError(t, writeTaterJSON(taterMediaMarkerSidecarPath(mediaPath), sidecar))

	breaks, credits := taterMediaMarkersForIndexedFile(
		config.LocalMediaCategory{Paths: []string{directory}},
		taterLocalLibraryFileIndex{SourceIndex: 0, Path: "Episode.mkv"},
	)
	assert.Equal(t, []float64{600, 1200}, breaks)
	assert.Equal(t, 1700.0, credits)
}

func TestTaterTVMidrollOffsetsUsesDetectedBreaks(t *testing.T) {
	item := taterUsenetItem{CommercialBreaks: []float64{600, 1200, 1800}}
	assert.Equal(t, []float64{600, 1200, 1800}, taterTVMidrollOffsets(item, 2400))
	assert.Equal(t, []float64{1200}, taterTVMidrollOffsets(taterUsenetItem{}, 2400))
}

func TestTaterMarkerScanChecksWriteAccessBeforeFFmpeg(t *testing.T) {
	configDir := t.TempDir()
	mediaRoot := filepath.Join(configDir, "readonly")
	require.NoError(t, os.MkdirAll(mediaRoot, 0755))
	mediaPath := filepath.Join(mediaRoot, "Movie.mkv")
	require.NoError(t, os.WriteFile(mediaPath, []byte("video"), 0644))

	ffmpegCalled := filepath.Join(configDir, "ffmpeg-called")
	binDir := filepath.Join(configDir, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0755))
	ffmpegPath := filepath.Join(binDir, "ffmpeg")
	ffprobePath := filepath.Join(binDir, "ffprobe")
	require.NoError(t, os.WriteFile(ffmpegPath, []byte("#!/bin/sh\ntouch \""+ffmpegCalled+"\"\nexit 0\n"), 0755))
	require.NoError(t, os.WriteFile(ffprobePath, []byte("#!/bin/sh\ntouch \""+ffmpegCalled+"\"\nexit 0\n"), 0755))

	require.NoError(t, os.Chmod(mediaRoot, 0555))
	defer os.Chmod(mediaRoot, 0755)
	if err := checkTaterMediaMarkerWriteAccess(mediaPath); err == nil {
		t.Skip("filesystem permissions do not enforce a read-only directory for this process")
	}

	cfg := config.DefaultConfig(configDir)
	cfg.Transcoding.FFmpegPath = ffmpegPath
	cfg.LocalMedia.Categories = []config.LocalMediaCategory{{
		ID: "movies", Name: "Movies", LibraryType: "movies", Paths: []string{mediaRoot}, Enabled: boolPtr(true),
	}}
	index := taterLocalLibraryIndex{Files: []taterLocalLibraryFileIndex{{
		CategoryID: "movies", LibraryType: "movies", SourceIndex: 0, Path: "Movie.mkv",
	}}}
	summary, err := scanTaterMediaMarkers(t.Context(), cfg, &index, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, summary.Failed)
	require.NotEmpty(t, summary.Errors)
	assert.Contains(t, summary.Errors[0], "no write access")
	_, statErr := os.Stat(ffmpegCalled)
	assert.True(t, os.IsNotExist(statErr), "FFmpeg/FFprobe must not run without sidecar write access")
}
