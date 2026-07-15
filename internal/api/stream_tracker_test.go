package api

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/nzbfilesystem"
	"github.com/stretchr/testify/assert"
)

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

func TestStreamTracker_GetAll_IncludesTranscodingInfo(t *testing.T) {
	tracker := NewStreamTracker(nil)
	defer tracker.Stop()

	stream := tracker.AddStream("/movies/movie.mkv", "Local", "Living Room", "127.0.0.1", "TestAgent", 1000)
	tracker.SetTranscodingInfo(stream.ID, "hdmi_1080p", "HDMI 1080p", "vaapi", "/dev/dri/renderD128", "h264_vaapi", true)

	streams := tracker.GetAll()

	assert.Len(t, streams, 1)
	assert.True(t, streams[0].Transcoded)
	assert.True(t, streams[0].HardwareActive)
	assert.Equal(t, "hdmi_1080p", streams[0].TranscodeProfile)
	assert.Equal(t, "HDMI 1080p", streams[0].TranscodeName)
	assert.Equal(t, "vaapi", streams[0].HardwareAccel)
	assert.Equal(t, "/dev/dri/renderD128", streams[0].HardwareDevice)
	assert.Equal(t, "h264_vaapi", streams[0].VideoCodec)
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
