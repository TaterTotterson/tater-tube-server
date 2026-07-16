package database

import (
	"context"
	"fmt"
)

// UpsertPlaybackHistory creates or refreshes one playback session.
func (r *Repository) UpsertPlaybackHistory(ctx context.Context, entry *PlaybackHistoryEntry) error {
	if entry == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO playback_history (id, started_at, last_activity, payload)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			started_at = excluded.started_at,
			last_activity = excluded.last_activity,
			payload = excluded.payload
	`, entry.ID, entry.StartedAt, entry.LastActivity, entry.Payload)
	if err != nil {
		return fmt.Errorf("upsert playback history: %w", err)
	}
	return nil
}

// ListPlaybackHistory returns the most recently active playback sessions.
func (r *Repository) ListPlaybackHistory(ctx context.Context, limit int) ([]PlaybackHistoryEntry, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, started_at, last_activity, payload
		FROM playback_history
		ORDER BY last_activity DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list playback history: %w", err)
	}
	defer rows.Close()

	entries := make([]PlaybackHistoryEntry, 0, limit)
	for rows.Next() {
		var entry PlaybackHistoryEntry
		if err := rows.Scan(&entry.ID, &entry.StartedAt, &entry.LastActivity, &entry.Payload); err != nil {
			return nil, fmt.Errorf("scan playback history: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate playback history: %w", err)
	}
	return entries, nil
}
