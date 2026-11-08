package api

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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

func TestTaterMediaMarkerChangedKeysOnlyQueuesNewOrChangedVideo(t *testing.T) {
	before := taterLocalLibraryIndex{Files: []taterLocalLibraryFileIndex{
		{Key: "movie:one", LibraryType: "movies", SizeBytes: 100, ModifiedUnixNano: 10},
		{Key: "episode:one", LibraryType: "tv", SizeBytes: 200, ModifiedUnixNano: 20},
		{Key: "song:one", LibraryType: "music", SizeBytes: 10, ModifiedUnixNano: 10},
	}}
	after := taterLocalLibraryIndex{Files: []taterLocalLibraryFileIndex{
		{Key: "movie:one", LibraryType: "movies", SizeBytes: 100, ModifiedUnixNano: 10},
		{Key: "episode:one", LibraryType: "tv", SizeBytes: 200, ModifiedUnixNano: 21},
		{Key: "movie:two", LibraryType: "movies", SizeBytes: 300, ModifiedUnixNano: 30},
		{Key: "song:two", LibraryType: "music", SizeBytes: 10, ModifiedUnixNano: 10},
	}}

	assert.Equal(t, []string{"episode:one", "movie:two"}, taterMediaMarkerChangedKeys(before, after))
}

func TestTaterMediaMarkerBatchKeepsOnlyQueuedVideo(t *testing.T) {
	index := taterLocalLibraryIndex{Files: []taterLocalLibraryFileIndex{
		{Key: "movie:one", LibraryType: "movies"},
		{Key: "episode:one", LibraryType: "tv"},
		{Key: "song:one", LibraryType: "music"},
	}}

	batch := taterMediaMarkerBatch(index, false, map[string]struct{}{"episode:one": {}})
	require.Len(t, batch.Files, 1)
	assert.Equal(t, "episode:one", batch.Files[0].Key)

	all := taterMediaMarkerBatch(index, true, nil)
	require.Len(t, all.Files, 2)
}

func TestMediaMarkersEnabledByDefault(t *testing.T) {
	cfg := config.DefaultConfig(t.TempDir())
	require.NotNil(t, cfg.MediaMarkers.Enabled)
	assert.True(t, *cfg.MediaMarkers.Enabled)
	require.NotNil(t, cfg.MediaMarkers.PauseDuringPlayback)
	assert.True(t, *cfg.MediaMarkers.PauseDuringPlayback)
	assert.True(t, taterMediaMarkersPauseDuringPlayback(cfg))

	disabled := false
	cfg.MediaMarkers.PauseDuringPlayback = &disabled
	assert.False(t, taterMediaMarkersPauseDuringPlayback(cfg))
}

func TestTaterMediaMarkerHardwareDecodeArgsUsesConfiguredDecoder(t *testing.T) {
	cfg := config.DefaultConfig(t.TempDir())
	cfg.Transcoding.HardwareAcceleration = "nvenc"
	cfg.Transcoding.HardwareDevice = "0"
	assert.Equal(t, []string{"-hwaccel", "cuda", "-hwaccel_device", "0"}, taterMediaMarkerHardwareDecodeArgs(cfg))

	cfg.Transcoding.HardwareAcceleration = "videotoolbox"
	cfg.Transcoding.HardwareDevice = ""
	assert.Equal(t, []string{"-hwaccel", "videotoolbox"}, taterMediaMarkerHardwareDecodeArgs(cfg))

	cfg.Transcoding.HardwareAcceleration = "none"
	assert.Empty(t, taterMediaMarkerHardwareDecodeArgs(cfg))
}

func TestTaterMediaMarkerScanFallsBackToSoftwareDecode(t *testing.T) {
	root := t.TempDir()
	mediaPath := filepath.Join(root, "Movie.mkv")
	require.NoError(t, os.WriteFile(mediaPath, []byte("video"), 0644))
	info, err := os.Stat(mediaPath)
	require.NoError(t, err)

	ffmpegPath := filepath.Join(root, "ffmpeg")
	ffprobePath := filepath.Join(root, "ffprobe")
	ffmpegScript := `#!/bin/sh
count_file="${0}.count"
count=0
if [ -f "$count_file" ]; then count=$(cat "$count_file"); fi
count=$((count + 1))
printf '%s' "$count" > "$count_file"
for arg in "$@"; do
  if [ "$arg" = "-hwaccel" ]; then
    echo "hardware decoder unavailable" >&2
    exit 1
  fi
done
echo "[blackdetect] black_start:60 black_end:60.5 black_duration:0.5" >&2
exit 0
`
	ffprobeScript := `#!/bin/sh
printf '%s\n' '{"streams":[{"codec_type":"video"}],"chapters":[],"format":{"duration":"120"}}'
`
	require.NoError(t, os.WriteFile(ffmpegPath, []byte(ffmpegScript), 0755))
	require.NoError(t, os.WriteFile(ffprobePath, []byte(ffprobeScript), 0755))

	cfg := config.DefaultConfig(root)
	cfg.Transcoding.FFmpegPath = ffmpegPath
	cfg.Transcoding.HardwareAcceleration = "auto"
	_, err = scanTaterMediaMarkerFile(t.Context(), cfg, mediaPath, info, 120)
	require.NoError(t, err)
	invocations, err := os.ReadFile(ffmpegPath + ".count")
	require.NoError(t, err)
	assert.Equal(t, "2", string(invocations), "hardware failure should retry the file once in software")
}

func TestTaterMediaMarkerScanUsesTwoWorkersWhenPlaybackIsIdle(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"One.mkv", "Two.mkv"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("video"), 0644))
	}

	ffmpegPath := filepath.Join(root, "ffmpeg")
	ffprobePath := filepath.Join(root, "ffprobe")
	ffmpegScript := `#!/bin/sh
workers="${0}.workers"
mkdir -p "$workers"
touch "$workers/$$"
count=$(find "$workers" -type f | wc -l | tr -d ' ')
if [ "$count" -gt 1 ]; then touch "${0}.parallel"; fi
sleep 0.2
rm -f "$workers/$$"
exit 0
`
	ffprobeScript := `#!/bin/sh
printf '%s\n' '{"streams":[{"codec_type":"video"}],"chapters":[],"format":{"duration":"120"}}'
`
	require.NoError(t, os.WriteFile(ffmpegPath, []byte(ffmpegScript), 0755))
	require.NoError(t, os.WriteFile(ffprobePath, []byte(ffprobeScript), 0755))

	cfg := config.DefaultConfig(root)
	cfg.Transcoding.FFmpegPath = ffmpegPath
	cfg.Transcoding.HardwareAcceleration = "auto"
	cfg.LocalMedia.Categories = []config.LocalMediaCategory{{
		ID: "movies", Name: "Movies", LibraryType: "movies", Paths: []string{root}, Enabled: boolPtr(true),
	}}
	index := taterLocalLibraryIndex{Files: []taterLocalLibraryFileIndex{
		{CategoryID: "movies", LibraryType: "movies", SourceIndex: 0, Path: "One.mkv", DurationSeconds: 120},
		{CategoryID: "movies", LibraryType: "movies", SourceIndex: 0, Path: "Two.mkv", DurationSeconds: 120},
	}}

	summary, err := scanTaterMediaMarkersWithOptions(
		t.Context(),
		cfg,
		&index,
		taterMediaMarkerScanOptions{Workers: 2},
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, 2, summary.Sidecars)
	_, err = os.Stat(ffmpegPath + ".parallel")
	assert.NoError(t, err, "idle marker scan should analyze two files concurrently")
}

func TestTaterMediaMarkerScanWaitsForPlaybackBetweenFiles(t *testing.T) {
	root := t.TempDir()
	mediaPath := filepath.Join(root, "Movie.mkv")
	require.NoError(t, os.WriteFile(mediaPath, []byte("video"), 0644))

	ffmpegPath := filepath.Join(root, "ffmpeg")
	ffprobePath := filepath.Join(root, "ffprobe")
	ffmpegScript := `#!/bin/sh
touch "${0}.started"
exit 0
`
	ffprobeScript := `#!/bin/sh
printf '%s\n' '{"streams":[{"codec_type":"video"}],"chapters":[],"format":{"duration":"120"}}'
`
	require.NoError(t, os.WriteFile(ffmpegPath, []byte(ffmpegScript), 0755))
	require.NoError(t, os.WriteFile(ffprobePath, []byte(ffprobeScript), 0755))

	cfg := config.DefaultConfig(root)
	cfg.Transcoding.FFmpegPath = ffmpegPath
	cfg.Transcoding.HardwareAcceleration = "auto"
	cfg.LocalMedia.Categories = []config.LocalMediaCategory{{
		ID: "movies", Name: "Movies", LibraryType: "movies", Paths: []string{root}, Enabled: boolPtr(true),
	}}
	index := taterLocalLibraryIndex{Files: []taterLocalLibraryFileIndex{{
		CategoryID: "movies", LibraryType: "movies", SourceIndex: 0, Path: "Movie.mkv", DurationSeconds: 120,
	}}}

	var playbackActive atomic.Bool
	playbackActive.Store(true)
	waiting := make(chan bool, 2)
	type scanResult struct {
		summary taterMediaMarkerScanSummary
		err     error
	}
	finished := make(chan scanResult, 1)
	go func() {
		summary, err := scanTaterMediaMarkersWithOptions(
			t.Context(),
			cfg,
			&index,
			taterMediaMarkerScanOptions{
				Workers:        2,
				PlaybackActive: playbackActive.Load,
				PlaybackWaiting: func(value bool) {
					waiting <- value
				},
			},
			nil,
		)
		finished <- scanResult{summary: summary, err: err}
	}()

	select {
	case value := <-waiting:
		assert.True(t, value)
	case <-time.After(time.Second):
		t.Fatal("marker scan did not enter playback wait state")
	}
	_, err := os.Stat(ffmpegPath + ".started")
	assert.True(t, os.IsNotExist(err), "marker FFmpeg must not start during playback")

	playbackActive.Store(false)
	select {
	case result := <-finished:
		require.NoError(t, result.err)
		assert.Equal(t, 1, result.summary.Sidecars)
	case <-time.After(4 * time.Second):
		t.Fatal("marker scan did not resume after playback stopped")
	}
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
