package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaterTVHLSAppendPrunesSegmentsOutsideRollingWindow(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tube-tv-hls", "session")
	itemDir := filepath.Join(root, "item-00000")
	if err := os.MkdirAll(itemDir, 0o755); err != nil {
		t.Fatal(err)
	}

	segmentCount := taterTVHLSSegmentFileLimit + 3
	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n")
	for index := 0; index < segmentCount; index++ {
		name := fmt.Sprintf("seg-%05d.ts", index)
		if err := os.WriteFile(filepath.Join(itemDir, name), []byte("segment"), 0o644); err != nil {
			t.Fatal(err)
		}
		playlist.WriteString("#EXTINF:2.000,\n" + name + "\n")
	}
	playlistPath := filepath.Join(itemDir, "index.m3u8")
	if err := os.WriteFile(playlistPath, []byte(playlist.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	session := &taterTVHLSSession{
		root:     root,
		seen:     map[string]bool{},
		accessed: time.Now(),
	}
	session.appendItemSegments("item-00000", playlistPath, taterTVStreamItem{Title: "Test", Kind: "movie"})

	if got := session.segmentCount(); got != segmentCount {
		t.Fatalf("tracked segments = %d, want %d", got, segmentCount)
	}
	for index := 0; index < 3; index++ {
		path := filepath.Join(itemDir, fmt.Sprintf("seg-%05d.ts", index))
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("stale segment was not pruned: %s", path)
		}
	}
	if _, err := os.Stat(filepath.Join(itemDir, fmt.Sprintf("seg-%05d.ts", segmentCount-1))); err != nil {
		t.Fatalf("live segment was pruned: %v", err)
	}
}

func TestTaterTVHLSStopRemovesSessionDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tube-tv-hls", "session")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "segment.ts"), []byte("segment"), 0o644); err != nil {
		t.Fatal(err)
	}
	runDone := make(chan struct{})
	close(runDone)
	session := &taterTVHLSSession{root: root, runDone: runDone}
	session.stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("stopped Tube TV HLS session directory was not removed")
}

func TestCleanupStaleTaterTVHLSRootsPreservesActiveAndRecent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tube-tv-hls")
	active := filepath.Join(root, "active")
	stale := filepath.Join(root, "stale")
	recent := filepath.Join(root, "recent")
	for _, path := range []string{active, stale, recent} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now()
	old := now.Add(-2 * taterTVHLSStaleRootAge)
	for _, path := range []string{active, stale} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := cleanupStaleTaterTVHLSRoots(
		root,
		map[string]struct{}{filepath.Clean(active): {}},
		now,
		taterTVHLSStaleRootAge,
	)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed %d stale roots, want 1", removed)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale inactive session was not removed")
	}
	for _, path := range []string{active, recent} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("preserved session %s is unavailable: %v", path, err)
		}
	}
}

func TestCleanupStaleTaterTVHLSRootsRejectsUnsafePath(t *testing.T) {
	if _, err := cleanupStaleTaterTVHLSRoots(t.TempDir(), nil, time.Now(), 0); err == nil {
		t.Fatal("unsafe cleanup root was accepted")
	}
}
