package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTaterAIRepositoryLifecycle(t *testing.T) {
	db, err := NewDB(Config{
		Type:         "sqlite",
		DatabasePath: filepath.Join(t.TempDir(), "tater-ai.db"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := NewRepository(db.Connection(), DialectSQLite)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	require.NoError(t, repo.CreateTaterCorePairingCode(ctx, TaterCorePairingCode{
		ID: "code-1", Name: "Tater", CodeHash: "pin-hash",
		CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}))
	matched, err := repo.PairTaterCore(ctx, "wrong-hash", now, TaterCoreConnection{
		ID: "core-1", Name: "Tater", TokenHash: "token-hash", CreatedAt: now,
		LastSeenAt: sql.NullTime{Time: now, Valid: true},
	})
	require.NoError(t, err)
	require.False(t, matched)

	matched, err = repo.PairTaterCore(ctx, "pin-hash", now, TaterCoreConnection{
		ID: "core-1", Name: "Tater", TokenHash: "token-hash", CreatedAt: now,
		LastSeenAt: sql.NullTime{Time: now, Valid: true},
	})
	require.NoError(t, err)
	require.True(t, matched)
	core, err := repo.FindTaterCoreByTokenHash(ctx, "token-hash")
	require.NoError(t, err)
	require.Equal(t, "core-1", core.ID)

	event := TaterViewingEvent{
		EventID: "watch-1", ProfileID: "household", PlayerID: "player-1",
		Source: "local_media", MediaID: "movie-1", MediaType: "movie",
		Title: "A Movie", State: "started", OccurredAt: now,
		MetadataJSON: "{}", CreatedAt: now,
	}
	require.NoError(t, repo.UpsertTaterViewingEvent(ctx, event))
	event.State = "completed"
	event.PositionMS = 7_200_000
	event.DurationMS = 7_200_000
	event.OccurredAt = now.Add(2 * time.Hour)
	require.NoError(t, repo.UpsertTaterViewingEvent(ctx, event))
	events, err := repo.ListTaterViewingEvents(ctx, "household", 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "completed", events[0].State)

	batch := TaterRecommendationBatch{
		ID: "batch-1", ProfileID: "household", CoreID: "core-1",
		Summary: "Movie night", GeneratedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}
	require.NoError(t, repo.SaveTaterRecommendations(ctx, batch, []TaterRecommendation{{
		ID: "pick-1", BatchID: batch.ID, Rank: 1, CandidateID: "candidate-1",
		Title: "Another Movie", MediaType: "movie", Source: "local_media",
		Reason: "A good follow-up.", LaunchJSON: `{"type":"localFile"}`, CreatedAt: now,
	}}))
	activeBatch, picks, err := repo.GetActiveTaterRecommendations(ctx, "household", now)
	require.NoError(t, err)
	require.Equal(t, batch.ID, activeBatch.ID)
	require.Len(t, picks, 1)
	require.Equal(t, "pick-1", picks[0].ID)
	require.NoError(t, repo.SetTaterRecommendationFeedback(ctx, "pick-1", "played", now))
}
