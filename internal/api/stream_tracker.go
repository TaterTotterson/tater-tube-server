package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/database"
	"github.com/TaterTotterson/tater-tube-server/internal/nzbfilesystem"
	"github.com/TaterTotterson/tater-tube-server/internal/usenet"
	"github.com/google/uuid"
)

// Default timeout for stale streams (4 hours - covers most movie lengths)
const defaultStreamTimeout = 4 * time.Hour
const playbackHistoryLimit = 250

type playbackHistoryStore interface {
	UpsertPlaybackHistory(context.Context, *database.PlaybackHistoryEntry) error
	ListPlaybackHistory(context.Context, int) ([]database.PlaybackHistoryEntry, error)
}

// StreamChangeNotifier is notified whenever the active stream count changes.
// Implemented by pool.Manager; declared here to avoid an api -> pool import
// dependency for the StreamTracker itself.
type StreamChangeNotifier interface {
	NotifyStreamChange()
}

// StreamTracker tracks active streams
type StreamTracker struct {
	streams        sync.Map
	history        []nzbfilesystem.ActiveStream
	done           chan struct{}
	mu             sync.Mutex // For history protection
	timeout        time.Duration
	metricsTracker usenet.MetricsTracker
	historyStore   playbackHistoryStore
	persistQueue   chan nzbfilesystem.ActiveStream
	persistDone    chan struct{}

	// activeCount is the exact number of entries currently in the streams map.
	// Maintained as an int64 counter so ActiveStreams() is O(1) and safe to
	// call from hot paths (e.g. the pool admission gate).
	activeCount atomic.Int64

	// notifier, when set, is notified after every stream add/remove so the
	// import-admission cap can react to streams starting/stopping.
	notifier StreamChangeNotifier
}

type streamSample struct {
	bytesSent       int64
	bytesDownloaded int64
	timestamp       time.Time
}

type streamInternal struct {
	*nzbfilesystem.ActiveStream
	lastBytesSent int64
	lastSnapshot  time.Time
	lastReadAt    time.Time
	cancel        context.CancelFunc
	samples       []streamSample
}

// NewStreamTracker creates a new stream tracker
func NewStreamTracker(metricsTracker usenet.MetricsTracker, historyStores ...playbackHistoryStore) *StreamTracker {
	t := &StreamTracker{
		done:           make(chan struct{}),
		history:        make([]nzbfilesystem.ActiveStream, 0, playbackHistoryLimit),
		timeout:        defaultStreamTimeout,
		metricsTracker: metricsTracker,
	}
	if len(historyStores) > 0 {
		t.historyStore = historyStores[0]
		t.persistQueue = make(chan nzbfilesystem.ActiveStream, 256)
		t.persistDone = make(chan struct{})
		t.restorePlaybackHistory()
		go t.playbackPersistenceLoop()
	}
	go t.snapshotLoop()
	return t
}

func (t *StreamTracker) restorePlaybackHistory() {
	if t == nil || t.historyStore == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	entries, err := t.historyStore.ListPlaybackHistory(ctx, playbackHistoryLimit)
	if err != nil {
		slog.Warn("Failed to restore playback activity", "error", err)
		return
	}
	for i := len(entries) - 1; i >= 0; i-- {
		var record nzbfilesystem.ActiveStream
		if err := json.Unmarshal([]byte(entries[i].Payload), &record); err != nil {
			slog.Warn("Skipped invalid playback activity", "id", entries[i].ID, "error", err)
			continue
		}
		record.ID = entries[i].ID
		record.StartedAt = entries[i].StartedAt
		record.LastActivity = entries[i].LastActivity
		updateWatchedDuration(&record)
		t.history = append(t.history, record)
	}
}

func (t *StreamTracker) persistPlaybackHistory(record nzbfilesystem.ActiveStream) {
	if t == nil || t.historyStore == nil || strings.TrimSpace(record.ID) == "" {
		return
	}
	select {
	case t.persistQueue <- record:
	default:
		slog.Warn("Playback activity queue is full", "id", record.ID)
	}
}

func (t *StreamTracker) playbackPersistenceLoop() {
	defer close(t.persistDone)
	for {
		select {
		case record := <-t.persistQueue:
			t.writePlaybackHistory(record)
		case <-t.done:
			for {
				select {
				case record := <-t.persistQueue:
					t.writePlaybackHistory(record)
				default:
					return
				}
			}
		}
	}
}

func (t *StreamTracker) writePlaybackHistory(record nzbfilesystem.ActiveStream) {
	updateWatchedDuration(&record)
	payload, err := json.Marshal(record)
	if err != nil {
		slog.Warn("Failed to encode playback activity", "id", record.ID, "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := t.historyStore.UpsertPlaybackHistory(ctx, &database.PlaybackHistoryEntry{
		ID:           record.ID,
		StartedAt:    record.StartedAt,
		LastActivity: record.LastActivity,
		Payload:      string(payload),
	}); err != nil {
		slog.Warn("Failed to save playback activity", "id", record.ID, "error", err)
	}
}

// StartCleanup starts a background goroutine that periodically removes stale streams.
// Call this once during server startup. The cleanup runs every 5 minutes.
// The goroutine stops when the context is cancelled.
func (t *StreamTracker) StartCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				t.cleanupStale()
			}
		}
	}()
}

// cleanupStale removes streams that have been active longer than the timeout.
// This handles cases where client disconnections don't properly trigger cleanup.
func (t *StreamTracker) cleanupStale() {
	now := time.Now()
	var removed int

	t.streams.Range(func(key, value any) bool {
		internal := value.(*streamInternal)
		stream := internal.ActiveStream
		if now.Sub(stream.StartedAt) > t.timeout {
			t.Remove(key.(string))
			removed++
			slog.DebugContext(context.Background(), "Cleaned up stale stream",
				"stream_id", stream.ID,
				"file_path", stream.FilePath,
				"started_at", stream.StartedAt,
				"age", now.Sub(stream.StartedAt))
		}
		return true
	})

	if removed > 0 {
		slog.InfoContext(context.Background(), "Cleaned up stale streams", "count", removed)
	}
}

func (t *StreamTracker) Stop() {
	close(t.done)
	if t.persistDone != nil {
		<-t.persistDone
	}
}

func (t *StreamTracker) snapshotLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-t.done:
			return
		case <-ticker.C:
			t.streams.Range(func(key, value any) bool {
				s := value.(*streamInternal)
				now := time.Now()

				// Cleanup stale streams (no activity for 30 minutes)
				// This handles cases where clients disconnect without properly closing the stream
				if !s.lastSnapshot.IsZero() && now.Sub(s.lastSnapshot) > 30*time.Minute {
					t.Remove(key.(string))
					return true
				}

				currentBytes := atomic.LoadInt64(&s.BytesSent)
				currentDownloaded := atomic.LoadInt64(&s.BytesDownloaded)

				// Add current sample
				s.samples = append(s.samples, streamSample{
					bytesSent:       currentBytes,
					bytesDownloaded: currentDownloaded,
					timestamp:       now,
				})

				// Cleanup old samples (keep 60 seconds of history)
				cutoff := now.Add(-60 * time.Second)
				keepIndex := 0
				for i, sample := range s.samples {
					if sample.timestamp.After(cutoff) {
						keepIndex = i
						break
					}
				}
				if keepIndex > 0 {
					remaining := s.samples[keepIndex:]
					// Compact: if the underlying array is much larger than what
					// we need, copy into a right-sized slice to release excess
					// backing memory that re-slicing would retain.
					if cap(s.samples) > 2*len(remaining) {
						compacted := make([]streamSample, len(remaining), len(remaining)+10)
						copy(compacted, remaining)
						s.samples = compacted
					} else {
						s.samples = remaining
					}
				}

				// Calculate windowed speed (10 second window)
				if len(s.samples) > 1 {
					speedWindow := 10 * time.Second
					windowCutoff := now.Add(-speedWindow)

					var referenceSample *streamSample
					for i := len(s.samples) - 1; i >= 0; i-- {
						if s.samples[i].timestamp.Before(windowCutoff) {
							referenceSample = &s.samples[i]
							break
						}
					}

					if referenceSample == nil {
						// Fallback to oldest sample if we don't have enough history yet
						referenceSample = &s.samples[0]
					}

					duration := now.Sub(referenceSample.timestamp).Seconds()
					if duration > 0 {
						// Playback speed
						bytesDiff := currentBytes - referenceSample.bytesSent
						if bytesDiff >= 0 {
							s.BytesPerSecond = int64(float64(bytesDiff) / duration)
						}

						// Download speed
						downloadDiff := currentDownloaded - referenceSample.bytesDownloaded
						if downloadDiff >= 0 {
							s.DownloadSpeed = int64(float64(downloadDiff) / duration)
						}
					}
				}
				// Update Status
				if currentBytes == 0 {
					s.Status = "Buffering"
				} else if !s.lastReadAt.IsZero() && now.Sub(s.lastReadAt) > 10*time.Second {
					s.Status = "Stalled"
				} else {
					s.Status = "Streaming"
				}
				// Calculate Average Speed
				totalDuration := now.Sub(s.StartedAt).Seconds()
				if totalDuration > 0 {
					s.SpeedAvg = int64(float64(currentBytes) / totalDuration)
				}

				// Calculate ETA based on current speed
				if s.BytesPerSecond > 0 && s.TotalSize > 0 {
					currentOffset := atomic.LoadInt64(&s.CurrentOffset)
					// Use the greater of CurrentOffset or BytesSent to determine progress
					// This handles cases where offset tracking might be missing
					progress := max(currentBytes, currentOffset)

					remainingBytes := s.TotalSize - progress
					if remainingBytes > 0 {
						s.ETA = remainingBytes / s.BytesPerSecond
					} else {
						s.ETA = 0
					}
				} else {
					s.ETA = -1 // Unknown or Infinite
				}

				// Only update lastSnapshot if bytes were actually sent, otherwise it keeps the time of last activity
				if currentBytes > s.lastBytesSent || s.lastSnapshot.IsZero() {
					s.lastSnapshot = now
				}
				s.lastBytesSent = currentBytes
				return true
			})
		}
	}
}

// AddStream adds a new stream and returns the stream object for updates
func (t *StreamTracker) AddStream(filePath, source, userName, clientIP, userAgent string, totalSize int64) *nzbfilesystem.ActiveStream {
	id := uuid.New().String()
	now := time.Now()
	stream := &nzbfilesystem.ActiveStream{
		ID:           id,
		FilePath:     filePath,
		StartedAt:    now,
		LastActivity: now,
		Source:       source,
		UserName:     userName,
		ClientIP:     clientIP,
		UserAgent:    userAgent,
		TotalSize:    totalSize,
		Status:       "Starting",
	}
	internal := &streamInternal{
		ActiveStream: stream,
		lastSnapshot: now,
		lastReadAt:   now,
		samples:      make([]streamSample, 0, 30), // Preallocate for 1 minute of samples (every 2s)
	}
	t.streams.Store(id, internal)
	t.activeCount.Add(1)
	t.notifyChange()
	return stream
}

// SetChangeNotifier wires a notifier (typically a pool.Manager) that will be
// signalled whenever the active stream count changes. Pass nil to clear.
func (t *StreamTracker) SetChangeNotifier(n StreamChangeNotifier) {
	t.notifier = n
}

// ActiveStreams returns the current number of tracked streams.
// Implements pool.StreamActivitySource (structurally).
func (t *StreamTracker) ActiveStreams() int {
	return int(t.activeCount.Load())
}

func (t *StreamTracker) notifyChange() {
	if t.notifier != nil {
		t.notifier.NotifyStreamChange()
	}
}

// Add adds a new stream and returns its ID (implements nzbfilesystem.StreamTracker)
func (t *StreamTracker) Add(filePath, source, userName, clientIP, userAgent string, totalSize int64) string {
	return t.AddStream(filePath, source, userName, clientIP, userAgent, totalSize).ID
}

// SetCancelFunc sets the cancellation function for a stream
func (t *StreamTracker) SetCancelFunc(id string, cancel context.CancelFunc) {
	if val, ok := t.streams.Load(id); ok {
		internal := val.(*streamInternal)
		internal.cancel = cancel
	}
}

func (t *StreamTracker) SetPlayerID(id, playerID string) {
	if val, ok := t.streams.Load(id); ok {
		stream := val.(*streamInternal)
		stream.PlayerID = strings.TrimSpace(playerID)
	}
}

// UpdateProgress updates the bytes sent for a stream by ID
func (t *StreamTracker) UpdateProgress(id string, bytesRead int64) {
	if val, ok := t.streams.Load(id); ok {
		stream := val.(*streamInternal)
		atomic.AddInt64(&stream.BytesSent, bytesRead)
		stream.lastReadAt = time.Now()
	}
}

func (t *StreamTracker) Touch(id string) {
	if val, ok := t.streams.Load(id); ok {
		stream := val.(*streamInternal)
		stream.lastReadAt = time.Now()
	}
}

func (t *StreamTracker) SetTranscodingInfo(id, profileID, profileName, hardwareAccel, hardwareDevice, videoCodec string, hardwareActive bool) {
	if val, ok := t.streams.Load(id); ok {
		stream := val.(*streamInternal)
		stream.Transcoded = true
		stream.TranscodeProfile = profileID
		stream.TranscodeName = profileName
		stream.HardwareAccel = hardwareAccel
		stream.HardwareDevice = hardwareDevice
		stream.VideoCodec = videoCodec
		stream.HardwareActive = hardwareActive
		stream.Status = "Transcoding"
		stream.lastReadAt = time.Now()
	}
}

func (t *StreamTracker) SetMediaInfo(id string, durationSeconds, playbackStartSeconds float64) {
	if val, ok := t.streams.Load(id); ok {
		stream := val.(*streamInternal)
		if durationSeconds > 0 && !math.IsNaN(durationSeconds) && !math.IsInf(durationSeconds, 0) {
			stream.MediaDuration = durationSeconds
		}
		if playbackStartSeconds > 0 && !math.IsNaN(playbackStartSeconds) && !math.IsInf(playbackStartSeconds, 0) {
			stream.PlaybackStart = playbackStartSeconds
			stream.PlaybackPosition = playbackStartSeconds
		}
		stream.lastReadAt = time.Now()
	}
}

// UpdateDownloadProgress updates the bytes downloaded for a stream by ID
func (t *StreamTracker) UpdateDownloadProgress(id string, bytesDownloaded int64) {
	if val, ok := t.streams.Load(id); ok {
		stream := val.(*streamInternal)
		atomic.AddInt64(&stream.BytesDownloaded, bytesDownloaded)
	}

	// Also update global metrics
	if t.metricsTracker != nil {
		t.metricsTracker.UpdateDownloadProgress(id, bytesDownloaded)
	}
}

// IncArticlesDownloaded satisfies the usenet.MetricsTracker interface
func (t *StreamTracker) IncArticlesDownloaded() {
	if t.metricsTracker != nil {
		t.metricsTracker.IncArticlesDownloaded()
	}
}

// IncArticlesPosted satisfies the usenet.MetricsTracker interface
func (t *StreamTracker) IncArticlesPosted() {}

// UpdateCurrentOffset updates the current playback offset for a stream by ID
func (t *StreamTracker) UpdateCurrentOffset(id string, offset int64) {
	if val, ok := t.streams.Load(id); ok {
		stream := val.(*streamInternal)
		atomic.StoreInt64(&stream.CurrentOffset, offset)
	}
}

// UpdateBufferedOffset updates the buffered offset for a stream by ID
func (t *StreamTracker) UpdateBufferedOffset(id string, offset int64) {
	if val, ok := t.streams.Load(id); ok {
		stream := val.(*streamInternal)
		atomic.StoreInt64(&stream.BufferedOffset, offset)
	}
}

// RecordPlayback stores a lightweight playback event that did not necessarily
// map to a long-lived tracked stream, such as a Tube TV HLS segment request.
func (t *StreamTracker) RecordPlayback(record nzbfilesystem.ActiveStream) {
	if t == nil {
		return
	}
	now := time.Now()
	hasStableID := strings.TrimSpace(record.ID) != ""
	if record.ID == "" {
		record.ID = uuid.New().String()
	}
	if record.StartedAt.IsZero() {
		record.StartedAt = now
	}
	if record.LastActivity.IsZero() {
		record.LastActivity = now
	}
	if record.Status == "" {
		record.Status = "Streaming"
	}
	if record.PlaybackPosition <= 0 {
		updatePlaybackPosition(&record, record.LastActivity)
	}
	updateWatchedDuration(&record)

	key := playbackRecordKey(record)
	if hasStableID {
		key = "id|" + strings.ToLower(strings.TrimSpace(record.ID))
	}
	t.mu.Lock()
	for i := range t.history {
		existingKey := playbackRecordKey(t.history[i])
		if hasStableID {
			existingKey = "id|" + strings.ToLower(strings.TrimSpace(t.history[i].ID))
		}
		if existingKey == key {
			mergePlaybackRecord(&t.history[i], record)
			persisted := t.history[i]
			t.mu.Unlock()
			t.persistPlaybackHistory(persisted)
			return
		}
	}

	if len(t.history) >= playbackHistoryLimit {
		t.history = t.history[1:]
	}
	t.history = append(t.history, record)
	t.mu.Unlock()
	t.persistPlaybackHistory(record)
}

// Remove removes a stream by ID and adds it to history
func (t *StreamTracker) Remove(id string) {
	if val, ok := t.streams.Load(id); ok {
		internal := valueToInternal(val)

		// Cancel the context to stop underlying readers and release resources
		if internal.cancel != nil {
			internal.cancel()
		}

		// Capture final stats
		finalStream := *internal.ActiveStream
		finalStream.BytesSent = atomic.LoadInt64(&internal.BytesSent)
		finalStream.BytesDownloaded = atomic.LoadInt64(&internal.BytesDownloaded)
		finalStream.CurrentOffset = atomic.LoadInt64(&internal.CurrentOffset)
		finalStream.BufferedOffset = atomic.LoadInt64(&internal.BufferedOffset)
		finalStream.BytesPerSecond = 0
		finalStream.DownloadSpeed = 0
		finalStream.Status = "Completed"
		if !internal.lastReadAt.IsZero() {
			finalStream.LastActivity = internal.lastReadAt
		} else {
			finalStream.LastActivity = time.Now()
		}
		updatePlaybackPosition(&finalStream, finalStream.LastActivity)
		updateWatchedDuration(&finalStream)

		t.mu.Lock()
		if len(t.history) >= playbackHistoryLimit {
			t.history = t.history[1:]
		}
		t.history = append(t.history, finalStream)
		t.mu.Unlock()
		t.persistPlaybackHistory(finalStream)

		t.streams.Delete(id)
		t.activeCount.Add(-1)
		t.notifyChange()
	}
}

func playbackRecordKey(stream nzbfilesystem.ActiveStream) string {
	playerKey := strings.ToLower(strings.TrimSpace(stream.PlayerID))
	if playerKey == "" {
		playerKey = strings.Join([]string{
			strings.ToLower(strings.TrimSpace(stream.UserName)),
			strings.ToLower(strings.TrimSpace(playbackClientHost(stream.ClientIP))),
		}, "|")
	}
	parts := []string{
		strings.ToLower(strings.TrimSpace(stream.Source)),
		playerKey,
		strings.ToLower(strings.TrimSpace(stream.FilePath)),
	}
	return strings.Join(parts, "|")
}

func playbackClientHost(value string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(value))
	if err == nil && host != "" {
		return host
	}
	return value
}

func mergePlaybackRecord(existing *nzbfilesystem.ActiveStream, next nzbfilesystem.ActiveStream) {
	if existing == nil {
		return
	}
	if existing.StartedAt.IsZero() || (!next.StartedAt.IsZero() && next.StartedAt.Before(existing.StartedAt)) {
		existing.StartedAt = next.StartedAt
	}
	if !next.LastActivity.IsZero() {
		existing.LastActivity = next.LastActivity
	}
	if next.Status != "" {
		existing.Status = next.Status
	}
	if next.BytesSent > 0 {
		existing.BytesSent += next.BytesSent
	}
	if next.BytesDownloaded > 0 {
		existing.BytesDownloaded += next.BytesDownloaded
	}
	if next.TotalSize > 0 {
		existing.TotalSize = next.TotalSize
	}
	if next.CurrentOffset > 0 {
		existing.CurrentOffset = next.CurrentOffset
	}
	if next.BufferedOffset > 0 {
		existing.BufferedOffset = next.BufferedOffset
	}
	if next.PlaybackPosition > 0 {
		existing.PlaybackPosition = next.PlaybackPosition
	}
	if next.PlaybackStart > 0 {
		existing.PlaybackStart = next.PlaybackStart
	}
	if next.MediaDuration > 0 {
		existing.MediaDuration = next.MediaDuration
	}
	if next.WatchedSeconds > 0 {
		existing.WatchedSeconds = next.WatchedSeconds
	}
	if next.UserName != "" {
		existing.UserName = next.UserName
	}
	if next.PlayerID != "" {
		existing.PlayerID = next.PlayerID
	}
	if next.ClientIP != "" {
		existing.ClientIP = next.ClientIP
	}
	if next.UserAgent != "" {
		existing.UserAgent = next.UserAgent
	}
	if next.Transcoded {
		existing.Transcoded = true
		existing.TranscodeProfile = next.TranscodeProfile
		existing.TranscodeName = next.TranscodeName
		existing.HardwareAccel = next.HardwareAccel
		existing.HardwareDevice = next.HardwareDevice
		existing.VideoCodec = next.VideoCodec
		existing.HardwareActive = next.HardwareActive
	}
	updateWatchedDuration(existing)
}

// KillStream cancels the context associated with a stream
func (t *StreamTracker) KillStream(id string) bool {
	if val, ok := t.streams.Load(id); ok {
		internal := val.(*streamInternal)
		if internal.cancel != nil {
			internal.cancel()
			return true
		}
	}
	return false
}

// GetHistory returns active and recently completed stream history.
func (t *StreamTracker) GetHistory() []nzbfilesystem.ActiveStream {
	streams := t.GetAll()

	t.mu.Lock()
	for _, s := range t.history {
		streams = append(streams, s)
	}
	t.mu.Unlock()

	sort.SliceStable(streams, func(i, j int) bool {
		return streamSortTime(streams[i]).After(streamSortTime(streams[j]))
	})
	if len(streams) > playbackHistoryLimit {
		streams = streams[:playbackHistoryLimit]
	}
	return streams
}

func valueToInternal(val any) *streamInternal {
	return val.(*streamInternal)
}

// GetAll returns all active streams, aggregated by file, user, and source
func (t *StreamTracker) GetAll() []nzbfilesystem.ActiveStream {
	// Map to group streams: key -> *nzbfilesystem.ActiveStream
	grouped := make(map[string]*nzbfilesystem.ActiveStream)

	t.streams.Range(func(key, value any) bool {
		internal := value.(*streamInternal)
		s := internal.ActiveStream

		// Create a composite key for grouping
		// We group by FilePath, UserName, Source, ClientIP and UserAgent to aggregate parallel connections
		// for the same playback session while keeping different devices separate
		groupKey := s.FilePath + "|" + s.UserName + "|" + s.Source + "|" + s.ClientIP + "|" + s.UserAgent

		if existing, ok := grouped[groupKey]; ok {
			// Aggregate with existing group

			// Sum up bytes sent from all connections
			currentBytes := atomic.LoadInt64(&s.BytesSent)
			currentDownloaded := atomic.LoadInt64(&s.BytesDownloaded)
			existing.BytesSent += currentBytes
			existing.BytesDownloaded += currentDownloaded
			existing.BytesPerSecond += internal.BytesPerSecond
			existing.DownloadSpeed += internal.DownloadSpeed
			// Average speed is complex to aggregate, but sum of averages approximates total throughput
			existing.SpeedAvg += internal.SpeedAvg

			// Use the current offset from the most recently active connection
			// This handles seek-back scenarios better than taking the max
			if internal.lastReadAt.After(existing.LastActivity) {
				existing.LastActivity = internal.lastReadAt
				existing.CurrentOffset = atomic.LoadInt64(&s.CurrentOffset)
				existing.BufferedOffset = atomic.LoadInt64(&s.BufferedOffset)
			}

			// For ETA, use the stream with the longest remaining time or re-calculate based on totals?
			// Re-calculating based on aggregated values is safer
			if existing.BytesPerSecond > 0 && existing.TotalSize > 0 {
				remaining := existing.TotalSize - existing.CurrentOffset

				if remaining > 0 {
					existing.ETA = remaining / existing.BytesPerSecond
				} else {
					existing.ETA = 0
				}
			}

			// Use the earliest start time to represent the session start
			if s.StartedAt.Before(existing.StartedAt) {
				existing.StartedAt = s.StartedAt
			}

			// Ensure we have the total size (should be consistent across connections)
			if existing.TotalSize == 0 && s.TotalSize > 0 {
				existing.TotalSize = s.TotalSize
			}
			if existing.MediaDuration == 0 && s.MediaDuration > 0 {
				existing.MediaDuration = s.MediaDuration
			}
			if existing.PlaybackStart == 0 && s.PlaybackStart > 0 {
				existing.PlaybackStart = s.PlaybackStart
			}

			// Use the "most active" status
			if existing.Status != "Streaming" && s.Status == "Streaming" {
				existing.Status = "Streaming"
			}
			if s.Transcoded {
				existing.Transcoded = true
				existing.TranscodeProfile = s.TranscodeProfile
				existing.TranscodeName = s.TranscodeName
				existing.HardwareAccel = s.HardwareAccel
				existing.HardwareDevice = s.HardwareDevice
				existing.VideoCodec = s.VideoCodec
				existing.HardwareActive = s.HardwareActive
				if existing.Status != "Streaming" {
					existing.Status = s.Status
				}
			}

			existing.TotalConnections++
			updatePlaybackPosition(existing, existing.LastActivity)
			updateWatchedDuration(existing)
		} else {
			// Initialize new group with this stream
			streamCopy := *s
			// Load current atomic value
			streamCopy.BytesSent = atomic.LoadInt64(&s.BytesSent)
			streamCopy.BytesDownloaded = atomic.LoadInt64(&s.BytesDownloaded)
			streamCopy.CurrentOffset = atomic.LoadInt64(&s.CurrentOffset)
			streamCopy.BufferedOffset = atomic.LoadInt64(&s.BufferedOffset)
			streamCopy.LastActivity = internal.lastReadAt
			streamCopy.BytesPerSecond = internal.BytesPerSecond
			streamCopy.DownloadSpeed = internal.DownloadSpeed
			streamCopy.SpeedAvg = internal.SpeedAvg
			streamCopy.ETA = internal.ETA
			// Use groupKey as stable ID to prevent UI flickering when underlying connections change
			streamCopy.ID = groupKey
			streamCopy.TotalConnections = 1
			updatePlaybackPosition(&streamCopy, internal.lastReadAt)
			updateWatchedDuration(&streamCopy)
			grouped[groupKey] = &streamCopy
		}
		return true
	})

	// Convert map to slice
	var streams []nzbfilesystem.ActiveStream
	for _, s := range grouped {
		streams = append(streams, *s)
	}

	// Sort by start time, newest first
	sort.Slice(streams, func(i, j int) bool {
		return streams[i].StartedAt.After(streams[j].StartedAt)
	})

	return streams
}

func streamSortTime(stream nzbfilesystem.ActiveStream) time.Time {
	if !stream.LastActivity.IsZero() {
		return stream.LastActivity
	}
	return stream.StartedAt
}

func updatePlaybackPosition(stream *nzbfilesystem.ActiveStream, lastReadAt time.Time) {
	if stream == nil {
		return
	}
	position := stream.PlaybackPosition
	if stream.Transcoded {
		position = stream.PlaybackStart + time.Since(stream.StartedAt).Seconds()
	} else if stream.MediaDuration > 0 && stream.TotalSize > 0 && stream.CurrentOffset > 0 {
		progress := float64(stream.CurrentOffset) / float64(stream.TotalSize)
		if progress < 0 {
			progress = 0
		}
		if progress > 1 {
			progress = 1
		}
		position = stream.PlaybackStart + progress*(stream.MediaDuration-stream.PlaybackStart)
	} else if position <= 0 && !lastReadAt.IsZero() {
		position = time.Since(stream.StartedAt).Seconds()
	}
	if stream.MediaDuration > 0 && position > stream.MediaDuration {
		position = stream.MediaDuration
	}
	if position < 0 || math.IsNaN(position) || math.IsInf(position, 0) {
		position = 0
	}
	stream.PlaybackPosition = position
}

func updateWatchedDuration(stream *nzbfilesystem.ActiveStream) {
	if stream == nil || stream.StartedAt.IsZero() {
		return
	}
	end := stream.LastActivity
	if end.IsZero() {
		end = time.Now()
	}
	watched := end.Sub(stream.StartedAt).Seconds()
	if watched < 0 || math.IsNaN(watched) || math.IsInf(watched, 0) {
		watched = 0
	}
	stream.WatchedSeconds = watched
}

// GetStream returns an active stream by ID
func (t *StreamTracker) GetStream(id string) *nzbfilesystem.ActiveStream {
	if val, ok := t.streams.Load(id); ok {
		return val.(*streamInternal).ActiveStream
	}
	return nil
}
