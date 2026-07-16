package database

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestPlaybackHistoryRepositoryUpsertAndList(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE playback_history (
			id TEXT PRIMARY KEY,
			started_at DATETIME NOT NULL,
			last_activity DATETIME NOT NULL,
			payload TEXT NOT NULL
		)
	`)
	require.NoError(t, err)

	repo := NewRepository(db, DialectSQLite)
	started := time.Now().UTC().Truncate(time.Second)
	entry := &PlaybackHistoryEntry{
		ID:           "session-1",
		StartedAt:    started,
		LastActivity: started.Add(time.Minute),
		Payload:      `{"status":"Streaming"}`,
	}
	require.NoError(t, repo.UpsertPlaybackHistory(context.Background(), entry))

	entry.LastActivity = started.Add(2 * time.Minute)
	entry.Payload = `{"status":"Completed"}`
	require.NoError(t, repo.UpsertPlaybackHistory(context.Background(), entry))

	rows, err := repo.ListPlaybackHistory(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "session-1", rows[0].ID)
	require.Equal(t, `{"status":"Completed"}`, rows[0].Payload)
	require.WithinDuration(t, entry.LastActivity, rows[0].LastActivity, time.Second)
}
