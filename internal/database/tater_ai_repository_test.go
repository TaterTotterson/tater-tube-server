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
		ID: "core-1", Name: "Tater", AssistantName: "Totty",
		TokenHash: "token-hash", CreatedAt: now,
		LastSeenAt: sql.NullTime{Time: now, Valid: true},
	})
	require.NoError(t, err)
	require.False(t, matched)

	matched, err = repo.PairTaterCore(ctx, "pin-hash", now, TaterCoreConnection{
		ID: "core-1", Name: "Tater", AssistantName: "Totty",
		TokenHash: "token-hash", CreatedAt: now,
		LastSeenAt: sql.NullTime{Time: now, Valid: true},
	})
	require.NoError(t, err)
	require.True(t, matched)
	core, err := repo.FindTaterCoreByTokenHash(ctx, "token-hash")
	require.NoError(t, err)
	require.Equal(t, "core-1", core.ID)
	require.Equal(t, "Totty", core.AssistantName)

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
	require.Equal(t, "Totty", activeBatch.AssistantName)
	require.Len(t, picks, 1)
	require.Equal(t, "pick-1", picks[0].ID)
	require.NoError(t, repo.TouchTaterCore(ctx, "core-1", "Spud", now.Add(time.Minute)))
	activeBatch, _, err = repo.GetActiveTaterRecommendations(ctx, "household", now)
	require.NoError(t, err)
	require.Equal(t, "Spud", activeBatch.AssistantName)
	reason, err := repo.GetActiveTaterRecommendationReason(ctx, "pick-1", "household", now)
	require.NoError(t, err)
	require.Equal(t, "A good follow-up.", reason)
	summary, err := repo.GetActiveTaterRecommendationSummary(ctx, "batch-1", "household", now)
	require.NoError(t, err)
	require.Equal(t, "Movie night", summary)
	require.NoError(t, repo.SetTaterRecommendationFeedback(ctx, "pick-1", "played", now))

	ttsRequest := TaterTTSRequest{
		ID: "tts-1", ProfileID: "household", PlayerID: "player-1",
		Text: "A short recommendation.", Status: "pending", ContentType: "audio/wav",
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}
	require.NoError(t, repo.CreateTaterTTSRequest(ctx, ttsRequest))
	claimed, err := repo.ClaimTaterTTSRequests(ctx, "core-1", 1, now)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, "processing", claimed[0].Status)
	require.Equal(t, "core-1", claimed[0].CoreID)
	require.NoError(t, repo.CompleteTaterTTSRequest(
		ctx, "tts-1", "core-1", "d2F2", "audio/wav", "", now.Add(time.Second),
	))
	completed, err := repo.GetTaterTTSRequest(ctx, "tts-1", "player-1")
	require.NoError(t, err)
	require.Equal(t, "ready", completed.Status)
	require.Equal(t, "d2F2", completed.AudioBase64)
	require.NoError(t, repo.CancelTaterTTSRequest(ctx, "tts-1", "player-1", now.Add(2*time.Second)))
	canceled, err := repo.GetTaterTTSRequest(ctx, "tts-1", "player-1")
	require.NoError(t, err)
	require.Equal(t, "canceled", canceled.Status)
	require.Empty(t, canceled.AudioBase64)
}
