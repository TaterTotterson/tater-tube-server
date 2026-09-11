package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/database"
	"github.com/TaterTotterson/tater-tube-server/internal/nzbfilesystem"
	"github.com/stretchr/testify/assert"
)

type memoryPlaybackHistoryStore struct {
	entries map[string]database.PlaybackHistoryEntry
}

func (s *memoryPlaybackHistoryStore) UpsertPlaybackHistory(_ context.Context, entry *database.PlaybackHistoryEntry) error {
	if s.entries == nil {
		s.entries = make(map[string]database.PlaybackHistoryEntry)
	}
	s.entries[entry.ID] = *entry
	return nil
}

func (s *memoryPlaybackHistoryStore) ListPlaybackHistory(_ context.Context, limit int) ([]database.PlaybackHistoryEntry, error) {
	rows := make([]database.PlaybackHistoryEntry, 0, len(s.entries))
	for _, entry := range s.entries {
		rows = append(rows, entry)
		if len(rows) == limit {
			break
		}
	}
	return rows, nil
}

func TestStreamTracker_GetAll_Grouping(t *testing.T) {
	tracker := NewStreamTracker(nil)
	defer tracker.Stop()

	// Add 3 connections for the same file
	s1 := tracker.AddStream("/movies/movie.mkv", "Server", "user1", "127.0.0.1", "TestAgent", 1000)
	s2 := tracker.AddStream("/movies/movie.mkv", "Server", "user1", "127.0.0.1", "TestAgent", 1000)
	s3 := tracker.AddStream("/movies/movie.mkv", "Server", "user1", "127.0.0.1", "TestAgent", 1000)

	// Add some bytes sent to each
	atomic.AddInt64(&s1.BytesSent, 100)
	atomic.AddInt64(&s2.BytesSent, 200)
	atomic.AddInt64(&s3.BytesSent, 300)

	// Add another file for same user
	s4 := tracker.AddStream("/movies/other.mkv", "Server", "user1", "127.0.0.1", "TestAgent", 2000)
	atomic.AddInt64(&s4.BytesSent, 500)

	// Add same file for different user
	s5 := tracker.AddStream("/movies/movie.mkv", "Server", "user2", "127.0.0.1", "TestAgent", 1000)
	atomic.AddInt64(&s5.BytesSent, 50)

	streams := tracker.GetAll()

	// Should have 3 aggregated streams:
	// 1. movie.mkv for user1 (aggregated from 3)
	// 2. other.mkv for user1
	// 3. movie.mkv for user2
	assert.Len(t, streams, 3)

	// Find the aggregated stream for movie.mkv user1
	var movieUser1 *nzbfilesystem.ActiveStream
	for _, s := range streams {
		if s.FilePath == "/movies/movie.mkv" && s.UserName == "user1" {
			movieUser1 = &s
			break
		}
	}

	assert.NotNil(t, movieUser1)
	assert.Equal(t, int64(600), movieUser1.BytesSent) // 100 + 200 + 300
	assert.Equal(t, int64(1000), movieUser1.TotalSize)
	assert.Equal(t, "/movies/movie.mkv|user1|Server|127.0.0.1|TestAgent", movieUser1.ID)
}

func TestStreamTracker_GetAll_Sorting(t *testing.T) {
	tracker := NewStreamTracker(nil)
	defer tracker.Stop()

	// Add an older stream
	s1 := tracker.AddStream("/old.mkv", "Server", "user1", "127.0.0.1", "TestAgent", 1000)
	s1.StartedAt = time.Now().Add(-10 * time.Minute)

	// Add a newer stream
	s2 := tracker.AddStream("/new.mkv", "Server", "user1", "127.0.0.1", "TestAgent", 1000)
	s2.StartedAt = time.Now().Add(-1 * time.Minute)

	streams := tracker.GetAll()

	assert.Len(t, streams, 2)
	assert.Equal(t, "/new.mkv", streams[0].FilePath)
	assert.Equal(t, "/old.mkv", streams[1].FilePath)
}

func TestStreamTracker_GetAll_SeparatesPairedPlayersWithSameName(t *testing.T) {
	tracker := NewStreamTracker(nil)
	defer tracker.Stop()

	first := tracker.AddStream("/movies/movie.mkv", "Local", "Tater Tube Player", "127.0.0.1", "TestAgent", 1000)
	tracker.SetPlayerID(first.ID, "player-1")
	second := tracker.AddStream("/movies/movie.mkv", "Local", "Tater Tube Player", "127.0.0.1", "TestAgent", 1000)
	tracker.SetPlayerID(second.ID, "player-2")

	streams := tracker.GetAll()

	assert.Len(t, streams, 2)
	playerIDs := map[string]bool{}
	for _, stream := range streams {
		playerIDs[stream.PlayerID] = true
	}
	assert.True(t, playerIDs["player-1"])
	assert.True(t, playerIDs["player-2"])
}

func TestStreamTracker_GetAll_IncludesTranscodingInfo(t *testing.T) {
	tracker := NewStreamTracker(nil)
	defer tracker.Stop()

	stream := tracker.AddStream("/movies/movie.mkv", "Local", "Living Room", "127.0.0.1", "TestAgent", 1000)
	tracker.SetTranscodingInfo(stream.ID, "hdmi_1080p", "HDMI 1080p", "vaapi", "/dev/dri/renderD128", "h264_vaapi", true)
	tracker.SetVideoResolutionInfo(stream.ID, 3840, 2160, 1920, 1080)

	streams := tracker.GetAll()

	assert.Len(t, streams, 1)
	assert.True(t, streams[0].Transcoded)
	assert.True(t, streams[0].HardwareActive)
	assert.Equal(t, "hdmi_1080p", streams[0].TranscodeProfile)
	assert.Equal(t, "HDMI 1080p", streams[0].TranscodeName)
	assert.Equal(t, "vaapi", streams[0].HardwareAccel)
	assert.Equal(t, "/dev/dri/renderD128", streams[0].HardwareDevice)
	assert.Equal(t, "h264_vaapi", streams[0].VideoCodec)
	assert.Equal(t, "transcode", streams[0].VideoMode)
	assert.Equal(t, "transcode", streams[0].AudioMode)
	assert.Equal(t, "aac", streams[0].AudioCodec)
	assert.Equal(t, 3840, streams[0].SourceWidth)
	assert.Equal(t, 2160, streams[0].SourceHeight)
	assert.Equal(t, 1920, streams[0].OutputWidth)
	assert.Equal(t, 1080, streams[0].OutputHeight)
}

func TestStreamTracker_GetAll_ReportsAudioOnlyTranscoding(t *testing.T) {
	tracker := NewStreamTracker(nil)
	defer tracker.Stop()

	stream := tracker.AddStream("/movies/movie.mkv", "Local", "Living Room", "127.0.0.1", "TestAgent", 1000)
	tracker.SetTranscodingInfo(stream.ID, audioOnlyProfileID, audioOnlyProfileName, "none", "", "copy", false)
	tracker.SetTrackProcessingInfo(stream.ID, "direct", "transcode", "aac", "Transcoding audio")

	streams := tracker.GetAll()

	assert.Len(t, streams, 1)
	assert.True(t, streams[0].Transcoded)
	assert.False(t, streams[0].HardwareActive)
	assert.Equal(t, "direct", streams[0].VideoMode)
	assert.Equal(t, "transcode", streams[0].AudioMode)
	assert.Equal(t, "aac", streams[0].AudioCodec)
	assert.Equal(t, "Transcoding audio", streams[0].Status)
}

func TestStreamTracker_KillStreamsForPlayer(t *testing.T) {
	tracker := NewStreamTracker(nil)
	defer tracker.Stop()

	owned := tracker.AddStream("/movies/owned.mkv", "Local", "Xbox", "127.0.0.1", "TestAgent", 1000)
	tracker.SetPlayerID(owned.ID, "player-1")
	fallback := tracker.AddStream("/movies/fallback.mkv", "Local", "Xbox", "127.0.0.1", "TestAgent", 1000)
	other := tracker.AddStream("/movies/other.mkv", "Local", "Xbox", "127.0.0.1", "TestAgent", 1000)
	tracker.SetPlayerID(other.ID, "player-2")

	stopped := tracker.KillStreamsForPlayer("player-1", "Xbox")

	assert.Equal(t, 2, stopped)
	assert.Equal(t, 1, tracker.ActiveStreams())
	streams := tracker.GetAll()
	assert.Len(t, streams, 1)
	assert.Equal(t, "/movies/other.mkv", streams[0].FilePath)
	assert.Equal(t, "player-2", streams[0].PlayerID)
	assert.NotEqual(t, fallback.FilePath, streams[0].FilePath)
}

func TestStreamTracker_GetHistory_IncludesActivePlayback(t *testing.T) {
	tracker := NewStreamTracker(nil)
	defer tracker.Stop()

	stream := tracker.AddStream("/media/local/movie.mkv", "Local", "Tater Tube CRT", "10.0.0.2", "TaterTube", 2048)
	tracker.SetPlayerID(stream.ID, "player-crt")
	tracker.UpdateProgress(stream.ID, 512)

	history := tracker.GetHistory()

	assert.Len(t, history, 1)
	assert.Equal(t, "/media/local/movie.mkv", history[0].FilePath)
	assert.Equal(t, "Local", history[0].Source)
	assert.Equal(t, "Tater Tube CRT", history[0].UserName)
	assert.Equal(t, "player-crt", history[0].PlayerID)
	assert.Equal(t, int64(512), history[0].BytesSent)
	assert.True(t, history[0].IsActive)
	assert.GreaterOrEqual(t, history[0].ActivityAgeSeconds, int64(0))
}

func TestStreamTracker_GetHistory_KeepsCompletedPlayback(t *testing.T) {
	tracker := NewStreamTracker(nil)
	defer tracker.Stop()

	stream := tracker.AddStream("/stream/nzb/show.mkv", "API", "Tater Tube Gamer Room", "10.0.0.3", "TaterTube", 4096)
	tracker.SetPlayerID(stream.ID, "player-game")
	tracker.SetMediaInfo(stream.ID, 120, 0)
	tracker.UpdateProgress(stream.ID, 2048)
	tracker.UpdateCurrentOffset(stream.ID, 2048)
	tracker.Remove(stream.ID)

	history := tracker.GetHistory()

	assert.Len(t, history, 1)
	assert.Equal(t, "/stream/nzb/show.mkv", history[0].FilePath)
	assert.Equal(t, "Completed", history[0].Status)
	assert.Equal(t, "player-game", history[0].PlayerID)
	assert.Equal(t, int64(2048), history[0].BytesSent)
	assert.Equal(t, int64(2048), history[0].CurrentOffset)
	assert.NotZero(t, history[0].LastActivity)
	assert.Greater(t, history[0].PlaybackPosition, 0.0)
}

func TestStreamTracker_RecordPlayback_CoalescesPlaybackEvents(t *testing.T) {
	tracker := NewStreamTracker(nil)
	defer tracker.Stop()

	started := time.Now().Add(-2 * time.Minute)
	tracker.RecordPlayback(nzbfilesystem.ActiveStream{
		FilePath:         "Tube TV CH 02 - Cartoons",
		StartedAt:        started,
		LastActivity:     started.Add(time.Minute),
		Source:           "Tube TV",
		PlayerID:         "player-crt",
		UserName:         "CRT",
		ClientIP:         "10.0.0.2",
		BytesSent:        1024,
		Status:           "Streaming",
		PlaybackPosition: 60,
		MediaDuration:    1800,
		Transcoded:       true,
		HardwareAccel:    "qsv",
		VideoCodec:       "h264_qsv",
		HardwareActive:   true,
	})
	tracker.RecordPlayback(nzbfilesystem.ActiveStream{
		FilePath:         "Tube TV CH 02 - Cartoons",
		LastActivity:     started.Add(2 * time.Minute),
		Source:           "Tube TV",
		PlayerID:         "player-crt",
		UserName:         "CRT",
		ClientIP:         "10.0.0.2",
		BytesSent:        2048,
		Status:           "Streaming",
		PlaybackPosition: 120,
		MediaDuration:    1800,
		Transcoded:       true,
		HardwareAccel:    "qsv",
		VideoCodec:       "h264_qsv",
		HardwareActive:   true,
	})

	history := tracker.GetHistory()

	assert.Len(t, history, 1)
	assert.Equal(t, "Tube TV", history[0].Source)
	assert.Equal(t, int64(3072), history[0].BytesSent)
	assert.Equal(t, 120.0, history[0].PlaybackPosition)
	assert.Equal(t, 1800.0, history[0].MediaDuration)
	assert.True(t, history[0].HardwareActive)
}

func TestStreamTracker_GetHistory_DerivesHLSActivityFromServerTime(t *testing.T) {
	tracker := NewStreamTracker(nil)
	defer tracker.Stop()

	tracker.RecordPlayback(nzbfilesystem.ActiveStream{
		ID:           "tube-recent",
		FilePath:     "Tube TV CH 01 - Recent",
		StartedAt:    time.Now().Add(-time.Minute),
		LastActivity: time.Now().Add(-5 * time.Second),
		Source:       "Tube TV",
		Status:       "Streaming",
	})
	tracker.RecordPlayback(nzbfilesystem.ActiveStream{
		ID:           "tube-stale",
		FilePath:     "Tube TV CH 02 - Stale",
		StartedAt:    time.Now().Add(-time.Hour),
		LastActivity: time.Now().Add(-time.Minute),
		Source:       "Tube TV",
		Status:       "Streaming",
	})
	tracker.RecordPlayback(nzbfilesystem.ActiveStream{
		ID:           "tube-future",
		FilePath:     "Tube TV CH 03 - Future",
		StartedAt:    time.Now().Add(-time.Minute),
		LastActivity: time.Now().Add(time.Hour),
		Source:       "Tube TV",
		Status:       "Streaming",
	})

	history := tracker.GetHistory()
	activity := make(map[string]nzbfilesystem.ActiveStream, len(history))
	for _, stream := range history {
		activity[stream.ID] = stream
	}

	assert.True(t, activity["tube-recent"].IsActive)
	assert.False(t, activity["tube-stale"].IsActive)
	assert.False(t, activity["tube-future"].IsActive)
	assert.Equal(t, "Streaming", activity["tube-recent"].Status)
	assert.Equal(t, "Completed", activity["tube-stale"].Status)
	assert.Equal(t, "Completed", activity["tube-future"].Status)
	assert.GreaterOrEqual(t, activity["tube-recent"].ActivityAgeSeconds, int64(0))
	assert.Greater(t, activity["tube-stale"].ActivityAgeSeconds, int64(20))
	assert.Less(t, activity["tube-future"].ActivityAgeSeconds, int64(0))
}

func TestStreamTracker_GetActive_IncludesTrackedAndRecentHLSPlayback(t *testing.T) {
	tracker := NewStreamTracker(nil)
	defer tracker.Stop()

	local := tracker.AddStream(
		"/media/local/movie.mkv",
		"Local",
		"Tater Tube Player",
		"10.0.0.2",
		"TaterTube",
		2048,
	)
	tracker.SetPlayerID(local.ID, "player-local")
	tracker.RecordPlayback(nzbfilesystem.ActiveStream{
		ID:           "tube-recent",
		FilePath:     "Tube TV CH 07 - SCI-FI MOVIES",
		StartedAt:    time.Now().Add(-time.Minute),
		LastActivity: time.Now().Add(-5 * time.Second),
		Source:       "Tube TV",
		PlayerID:     "player-live",
		UserName:     "Tater Tube Player",
		Status:       "Streaming",
	})
	tracker.RecordPlayback(nzbfilesystem.ActiveStream{
		ID:           "tube-stale",
		FilePath:     "Tube TV CH 14 - 90S MOVIES",
		StartedAt:    time.Now().Add(-time.Hour),
		LastActivity: time.Now().Add(-time.Minute),
		Source:       "Tube TV",
		PlayerID:     "player-stale",
		UserName:     "Tater Tube Player",
		Status:       "Streaming",
	})

	active := tracker.GetActive()
	byPath := make(map[string]nzbfilesystem.ActiveStream, len(active))
	for _, stream := range active {
		byPath[stream.FilePath] = stream
	}

	assert.Len(t, active, 2)
	assert.True(t, byPath["Tube TV CH 07 - SCI-FI MOVIES"].IsActive)
	assert.Equal(t, "player-live", byPath["Tube TV CH 07 - SCI-FI MOVIES"].PlayerID)
	assert.Equal(t, "player-local", byPath["/media/local/movie.mkv"].PlayerID)
	_, hasStale := byPath["Tube TV CH 14 - 90S MOVIES"]
	assert.False(t, hasStale)
}

func TestStreamTracker_PlayerPresenceContinuesAfterTransferCompletes(t *testing.T) {
	tracker := NewStreamTracker(nil)
	defer tracker.Stop()

	transfer := tracker.AddStream(
		"Moonrise.Manor.2026.2160p.mkv",
		"API",
		"Living Room",
		"10.0.0.2",
		"TaterTubePlayer",
		4096,
	)
	tracker.SetPlayerID(transfer.ID, "player-living-room")
	tracker.SetTranscodingInfo(
		transfer.ID, "hdmi_1080p", "HDMI 1080p", "qsv", "/dev/dri/renderD128", "h264_qsv", true,
	)
	tracker.SetVideoResolutionInfo(transfer.ID, 3840, 2160, 1920, 1080)
	tracker.UpdateProgress(transfer.ID, 4096)
	tracker.Remove(transfer.ID)

	presence := nzbfilesystem.ActiveStream{
		FilePath:         "Moonrise.Manor.2026.2160p.mkv",
		Source:           "Discovery",
		PlayerID:         "player-living-room",
		UserName:         "Living Room",
		PlaybackPosition: 120,
		MediaDuration:    600,
	}
	tracker.SetPlayerPlaybackPresence(presence, true)

	active := tracker.GetActive()
	assert.Len(t, active, 1)
	assert.Equal(t, "player-playback:player-living-room", active[0].ID)
	assert.Equal(t, "Playing", active[0].Status)
	assert.Equal(t, 120.0, active[0].PlaybackPosition)
	assert.True(t, active[0].Transcoded)
	assert.True(t, active[0].HardwareActive)
	assert.Equal(t, 3840, active[0].SourceWidth)
	assert.Equal(t, 2160, active[0].SourceHeight)
	assert.Equal(t, 1920, active[0].OutputWidth)
	assert.Equal(t, 1080, active[0].OutputHeight)

	tracker.SetPlayerPlaybackPresence(presence, false)
	assert.Empty(t, tracker.GetActive())
}

func TestStreamTracker_PlayerPresenceDoesNotDuplicateActiveTransfer(t *testing.T) {
	tracker := NewStreamTracker(nil)
	defer tracker.Stop()

	transfer := tracker.AddStream(
		"/movies/Moonrise Manor.mkv",
		"Local",
		"Living Room",
		"10.0.0.2",
		"TaterTubePlayer",
		4096,
	)
	tracker.SetPlayerID(transfer.ID, "player-living-room")
	tracker.SetPlayerPlaybackPresence(nzbfilesystem.ActiveStream{
		FilePath: "Moonrise Manor.mkv",
		Source:   "Local",
		PlayerID: "player-living-room",
		UserName: "Living Room",
	}, true)

	active := tracker.GetActive()
	assert.Len(t, active, 1)
	assert.Equal(t, "/movies/Moonrise Manor.mkv", active[0].FilePath)
	assert.NotEqual(t, "player-playback:player-living-room", active[0].ID)
}

func TestStreamTracker_PlayerPresenceExpiresWithoutHeartbeat(t *testing.T) {
	tracker := NewStreamTracker(nil)
	defer tracker.Stop()

	tracker.playbackPresences.Store("player-away", nzbfilesystem.ActiveStream{
		ID:           "player-playback:player-away",
		FilePath:     "Old Movie.mkv",
		PlayerID:     "player-away",
		LastActivity: time.Now().Add(-playerPlaybackPresenceWindow),
		Status:       "Playing",
		IsActive:     true,
	})

	assert.Empty(t, tracker.GetActive())
	_, exists := tracker.playbackPresences.Load("player-away")
	assert.False(t, exists)
}

func TestStreamTracker_RestoresPersistentPlaybackActivity(t *testing.T) {
	store := &memoryPlaybackHistoryStore{}
	started := time.Now().Add(-90 * time.Second)

	tracker := NewStreamTracker(nil, store)
	tracker.RecordPlayback(nzbfilesystem.ActiveStream{
		ID:             "tube-session-1",
		FilePath:       "Tube TV CH 02 - Cartoons",
		StartedAt:      started,
		LastActivity:   time.Now(),
		Source:         "Tube TV",
		PlayerID:       "player-crt",
		UserName:       "CRT",
		Status:         "Streaming",
		Transcoded:     true,
		HardwareAccel:  "qsv",
		HardwareActive: true,
	})
	tracker.Stop()

	restored := NewStreamTracker(nil, store)
	defer restored.Stop()
	history := restored.GetHistory()

	assert.Len(t, history, 1)
	assert.Equal(t, "tube-session-1", history[0].ID)
	assert.Equal(t, "CRT", history[0].UserName)
	assert.True(t, history[0].HardwareActive)
	assert.GreaterOrEqual(t, history[0].WatchedSeconds, 89.0)
}
