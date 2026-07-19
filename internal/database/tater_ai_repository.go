package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type TaterCorePairingCode struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CodeHash  string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type TaterCoreConnection struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	AssistantName string       `json:"assistant_name"`
	TokenHash     string       `json:"-"`
	CreatedAt     time.Time    `json:"created_at"`
	LastSeenAt    sql.NullTime `json:"-"`
	RevokedAt     sql.NullTime `json:"-"`
}

type TaterViewingEvent struct {
	EventID      string    `json:"event_id"`
	ProfileID    string    `json:"profile_id"`
	PlayerID     string    `json:"player_id"`
	Source       string    `json:"source"`
	MediaID      string    `json:"media_id"`
	MediaType    string    `json:"media_type"`
	Title        string    `json:"title"`
	SeriesTitle  string    `json:"series_title,omitempty"`
	Season       int       `json:"season,omitempty"`
	Episode      int       `json:"episode,omitempty"`
	PositionMS   int64     `json:"position_ms"`
	DurationMS   int64     `json:"duration_ms"`
	State        string    `json:"state"`
	OccurredAt   time.Time `json:"occurred_at"`
	MetadataJSON string    `json:"metadata_json,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type TaterRecommendationBatch struct {
	ID            string    `json:"id"`
	ProfileID     string    `json:"profile_id"`
	CoreID        string    `json:"core_id"`
	AssistantName string    `json:"assistant_name"`
	Summary       string    `json:"summary"`
	GeneratedAt   time.Time `json:"generated_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

type TaterRecommendation struct {
	ID          string       `json:"id"`
	BatchID     string       `json:"batch_id"`
	Rank        int          `json:"rank"`
	CandidateID string       `json:"candidate_id"`
	Title       string       `json:"title"`
	MediaType   string       `json:"media_type"`
	Source      string       `json:"source"`
	Reason      string       `json:"reason"`
	LaunchJSON  string       `json:"-"`
	Feedback    string       `json:"feedback,omitempty"`
	FeedbackAt  sql.NullTime `json:"-"`
	CreatedAt   time.Time    `json:"created_at"`
}

type TaterTTSRequest struct {
	ID          string    `json:"id"`
	ProfileID   string    `json:"profile_id"`
	PlayerID    string    `json:"player_id"`
	CoreID      string    `json:"core_id,omitempty"`
	Text        string    `json:"text"`
	Status      string    `json:"status"`
	AudioBase64 string    `json:"-"`
	ContentType string    `json:"content_type,omitempty"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func (r *Repository) CreateTaterCorePairingCode(ctx context.Context, code TaterCorePairingCode) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO tater_core_pairing_codes (id, name, code_hash, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?)
	`, code.ID, code.Name, code.CodeHash, code.CreatedAt, code.ExpiresAt)
	return err
}

func (r *Repository) ListTaterCorePairingCodes(ctx context.Context, now time.Time) ([]TaterCorePairingCode, error) {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM tater_core_pairing_codes WHERE expires_at <= ?`, now); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, code_hash, created_at, expires_at
		FROM tater_core_pairing_codes ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []TaterCorePairingCode{}
	for rows.Next() {
		var item TaterCorePairingCode
		if err := rows.Scan(&item.ID, &item.Name, &item.CodeHash, &item.CreatedAt, &item.ExpiresAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) PairTaterCore(ctx context.Context, codeHash string, now time.Time, connection TaterCoreConnection) (bool, error) {
	if connection.AssistantName == "" {
		connection.AssistantName = "Tater"
	}
	matched := false
	err := r.WithTransaction(ctx, func(tx *Repository) error {
		if _, err := tx.db.ExecContext(ctx, `DELETE FROM tater_core_pairing_codes WHERE expires_at <= ?`, now); err != nil {
			return err
		}
		var codeID string
		if err := tx.db.QueryRowContext(ctx, `
			DELETE FROM tater_core_pairing_codes
			WHERE code_hash = ? AND expires_at > ?
			RETURNING id
		`, codeHash, now).Scan(&codeID); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return err
		}
		if _, err := tx.db.ExecContext(ctx, `
			INSERT INTO tater_core_connections
				(id, name, assistant_name, token_hash, created_at, last_seen_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, connection.ID, connection.Name, connection.AssistantName, connection.TokenHash,
			connection.CreatedAt, connection.LastSeenAt); err != nil {
			return err
		}
		_ = codeID
		matched = true
		return nil
	})
	return matched, err
}

func (r *Repository) ListTaterCoreConnections(ctx context.Context) ([]TaterCoreConnection, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, assistant_name, token_hash, created_at, last_seen_at, revoked_at
		FROM tater_core_connections ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []TaterCoreConnection{}
	for rows.Next() {
		var item TaterCoreConnection
		if err := rows.Scan(&item.ID, &item.Name, &item.AssistantName, &item.TokenHash,
			&item.CreatedAt, &item.LastSeenAt, &item.RevokedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) FindTaterCoreByTokenHash(ctx context.Context, tokenHash string) (*TaterCoreConnection, error) {
	var item TaterCoreConnection
	err := r.db.QueryRowContext(ctx, `
		SELECT id, name, assistant_name, token_hash, created_at, last_seen_at, revoked_at
		FROM tater_core_connections
		WHERE token_hash = ? AND revoked_at IS NULL
	`, tokenHash).Scan(&item.ID, &item.Name, &item.AssistantName, &item.TokenHash,
		&item.CreatedAt, &item.LastSeenAt, &item.RevokedAt)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *Repository) TouchTaterCore(ctx context.Context, id, assistantName string, now time.Time) error {
	if assistantName == "" {
		_, err := r.db.ExecContext(ctx,
			`UPDATE tater_core_connections SET last_seen_at = ? WHERE id = ?`,
			now, id)
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE tater_core_connections
		SET assistant_name = ?, last_seen_at = ?
		WHERE id = ?
	`, assistantName, now, id)
	return err
}

func (r *Repository) RevokeTaterCore(ctx context.Context, id string, now time.Time) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE tater_core_connections SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL
	`, now, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *Repository) UpsertTaterViewingEvent(ctx context.Context, item TaterViewingEvent) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO tater_viewing_events (
			event_id, profile_id, player_id, source, media_id, media_type, title,
			series_title, season, episode, position_ms, duration_ms, state,
			occurred_at, metadata_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(event_id) DO UPDATE SET
			profile_id = excluded.profile_id,
			player_id = excluded.player_id,
			source = excluded.source,
			media_id = excluded.media_id,
			media_type = excluded.media_type,
			title = excluded.title,
			series_title = excluded.series_title,
			season = excluded.season,
			episode = excluded.episode,
			position_ms = excluded.position_ms,
			duration_ms = excluded.duration_ms,
			state = excluded.state,
			occurred_at = excluded.occurred_at,
			metadata_json = excluded.metadata_json
	`, item.EventID, item.ProfileID, item.PlayerID, item.Source, item.MediaID, item.MediaType,
		item.Title, item.SeriesTitle, item.Season, item.Episode, item.PositionMS, item.DurationMS,
		item.State, item.OccurredAt, item.MetadataJSON, item.CreatedAt)
	return err
}

func (r *Repository) ListTaterViewingEvents(ctx context.Context, profileID string, limit int) ([]TaterViewingEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT event_id, profile_id, player_id, source, media_id, media_type, title,
			series_title, season, episode, position_ms, duration_ms, state,
			occurred_at, metadata_json, created_at
		FROM tater_viewing_events
		WHERE (? = '' OR profile_id = ?)
		ORDER BY occurred_at DESC LIMIT ?
	`, profileID, profileID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []TaterViewingEvent{}
	for rows.Next() {
		var item TaterViewingEvent
		if err := rows.Scan(&item.EventID, &item.ProfileID, &item.PlayerID, &item.Source,
			&item.MediaID, &item.MediaType, &item.Title, &item.SeriesTitle, &item.Season,
			&item.Episode, &item.PositionMS, &item.DurationMS, &item.State, &item.OccurredAt,
			&item.MetadataJSON, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) ClearTaterViewingEvents(ctx context.Context, profileID string) error {
	query := `DELETE FROM tater_viewing_events`
	args := []any{}
	if profileID != "" {
		query += ` WHERE profile_id = ?`
		args = append(args, profileID)
	}
	_, err := r.db.ExecContext(ctx, query, args...)
	return err
}

func (r *Repository) SaveTaterRecommendations(ctx context.Context, batch TaterRecommendationBatch, items []TaterRecommendation) error {
	return r.WithTransaction(ctx, func(tx *Repository) error {
		if _, err := tx.db.ExecContext(ctx, `
			INSERT INTO tater_recommendation_batches
				(id, profile_id, core_id, summary, generated_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, batch.ID, batch.ProfileID, batch.CoreID, batch.Summary, batch.GeneratedAt, batch.ExpiresAt); err != nil {
			return err
		}
		for _, item := range items {
			if _, err := tx.db.ExecContext(ctx, `
				INSERT INTO tater_recommendations (
					id, batch_id, rank, candidate_id, title, media_type, source,
					reason, launch_json, feedback, created_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, item.ID, batch.ID, item.Rank, item.CandidateID, item.Title, item.MediaType,
				item.Source, item.Reason, item.LaunchJSON, item.Feedback, item.CreatedAt); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repository) GetActiveTaterRecommendations(ctx context.Context, profileID string, now time.Time) (*TaterRecommendationBatch, []TaterRecommendation, error) {
	var batch TaterRecommendationBatch
	err := r.db.QueryRowContext(ctx, `
		SELECT batch.id, batch.profile_id, batch.core_id, batch.summary,
			batch.generated_at, batch.expires_at,
			COALESCE(NULLIF(core.assistant_name, ''), 'Tater')
		FROM tater_recommendation_batches AS batch
		LEFT JOIN tater_core_connections AS core ON core.id = batch.core_id
		WHERE batch.profile_id = ? AND batch.expires_at > ?
		ORDER BY batch.generated_at DESC LIMIT 1
	`, profileID, now).Scan(
		&batch.ID, &batch.ProfileID, &batch.CoreID, &batch.Summary,
		&batch.GeneratedAt, &batch.ExpiresAt, &batch.AssistantName,
	)
	if err != nil {
		return nil, nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, batch_id, rank, candidate_id, title, media_type, source,
			reason, launch_json, feedback, feedback_at, created_at
		FROM tater_recommendations WHERE batch_id = ? ORDER BY rank ASC
	`, batch.ID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := []TaterRecommendation{}
	for rows.Next() {
		var item TaterRecommendation
		if err := rows.Scan(&item.ID, &item.BatchID, &item.Rank, &item.CandidateID, &item.Title,
			&item.MediaType, &item.Source, &item.Reason, &item.LaunchJSON, &item.Feedback,
			&item.FeedbackAt, &item.CreatedAt); err != nil {
			return nil, nil, err
		}
		items = append(items, item)
	}
	return &batch, items, rows.Err()
}

func (r *Repository) ListTaterRecommendationBatches(ctx context.Context, profileID string, limit int) ([]TaterRecommendationBatch, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT batch.id, batch.profile_id, batch.core_id, batch.summary,
			batch.generated_at, batch.expires_at,
			COALESCE(NULLIF(core.assistant_name, ''), 'Tater')
		FROM tater_recommendation_batches AS batch
		LEFT JOIN tater_core_connections AS core ON core.id = batch.core_id
		WHERE (? = '' OR batch.profile_id = ?)
		ORDER BY batch.generated_at DESC LIMIT ?
	`, profileID, profileID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []TaterRecommendationBatch{}
	for rows.Next() {
		var item TaterRecommendationBatch
		if err := rows.Scan(
			&item.ID, &item.ProfileID, &item.CoreID, &item.Summary,
			&item.GeneratedAt, &item.ExpiresAt, &item.AssistantName,
		); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) SetTaterRecommendationFeedback(ctx context.Context, id, feedback string, now time.Time) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE tater_recommendations SET feedback = ?, feedback_at = ? WHERE id = ?
	`, feedback, now, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return fmt.Errorf("recommendation not found")
	}
	return nil
}

func (r *Repository) GetActiveTaterRecommendationReason(
	ctx context.Context,
	id, profileID string,
	now time.Time,
) (string, error) {
	var reason string
	err := r.db.QueryRowContext(ctx, `
		SELECT recommendation.reason
		FROM tater_recommendations AS recommendation
		INNER JOIN tater_recommendation_batches AS batch
			ON batch.id = recommendation.batch_id
		WHERE recommendation.id = ?
			AND batch.profile_id = ?
			AND batch.expires_at > ?
			AND recommendation.feedback NOT IN ('dismissed', 'not_for_me')
		LIMIT 1
	`, id, profileID, now).Scan(&reason)
	return reason, err
}

func (r *Repository) GetActiveTaterRecommendationSummary(
	ctx context.Context,
	batchID, profileID string,
	now time.Time,
) (string, error) {
	var summary string
	err := r.db.QueryRowContext(ctx, `
		SELECT summary
		FROM tater_recommendation_batches
		WHERE id = ?
			AND profile_id = ?
			AND expires_at > ?
		LIMIT 1
	`, batchID, profileID, now).Scan(&summary)
	return summary, err
}

func (r *Repository) CreateTaterTTSRequest(ctx context.Context, item TaterTTSRequest) error {
	if _, err := r.db.ExecContext(ctx, `
		DELETE FROM tater_tts_requests WHERE expires_at <= ?
	`, item.CreatedAt.Add(-24*time.Hour)); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO tater_tts_requests (
			id, profile_id, player_id, core_id, text, status, audio_base64,
			content_type, error, created_at, updated_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, item.ID, item.ProfileID, item.PlayerID, item.CoreID, item.Text, item.Status,
		item.AudioBase64, item.ContentType, item.Error, item.CreatedAt, item.UpdatedAt,
		item.ExpiresAt)
	return err
}

func (r *Repository) GetTaterTTSRequest(ctx context.Context, id, playerID string) (*TaterTTSRequest, error) {
	var item TaterTTSRequest
	err := r.db.QueryRowContext(ctx, `
		SELECT id, profile_id, player_id, core_id, text, status, audio_base64,
			content_type, error, created_at, updated_at, expires_at
		FROM tater_tts_requests
		WHERE id = ? AND (? = '' OR player_id = ?)
	`, id, playerID, playerID).Scan(
		&item.ID, &item.ProfileID, &item.PlayerID, &item.CoreID, &item.Text,
		&item.Status, &item.AudioBase64, &item.ContentType, &item.Error,
		&item.CreatedAt, &item.UpdatedAt, &item.ExpiresAt,
	)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *Repository) ClaimTaterTTSRequests(ctx context.Context, coreID string, limit int, now time.Time) ([]TaterTTSRequest, error) {
	if limit <= 0 || limit > 4 {
		limit = 1
	}
	claimed := []TaterTTSRequest{}
	err := r.WithTransaction(ctx, func(tx *Repository) error {
		if _, err := tx.db.ExecContext(ctx, `
			DELETE FROM tater_tts_requests WHERE expires_at <= ?
		`, now.Add(-24*time.Hour)); err != nil {
			return err
		}
		if _, err := tx.db.ExecContext(ctx, `
			UPDATE tater_tts_requests
			SET status = 'pending', core_id = '', updated_at = ?
			WHERE status = 'processing' AND updated_at <= ? AND expires_at > ?
		`, now, now.Add(-2*time.Minute), now); err != nil {
			return err
		}
		if _, err := tx.db.ExecContext(ctx, `
			UPDATE tater_tts_requests
			SET status = 'failed', error = 'TTS request expired', updated_at = ?
			WHERE status IN ('pending', 'processing') AND expires_at <= ?
		`, now, now); err != nil {
			return err
		}
		rows, err := tx.db.QueryContext(ctx, `
			SELECT id, profile_id, player_id, core_id, text, status, audio_base64,
				content_type, error, created_at, updated_at, expires_at
			FROM tater_tts_requests
			WHERE status = 'pending' AND expires_at > ?
			ORDER BY created_at ASC LIMIT ?
		`, now, limit)
		if err != nil {
			return err
		}
		pending := []TaterTTSRequest{}
		for rows.Next() {
			var item TaterTTSRequest
			if err := rows.Scan(
				&item.ID, &item.ProfileID, &item.PlayerID, &item.CoreID, &item.Text,
				&item.Status, &item.AudioBase64, &item.ContentType, &item.Error,
				&item.CreatedAt, &item.UpdatedAt, &item.ExpiresAt,
			); err != nil {
				rows.Close()
				return err
			}
			pending = append(pending, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, item := range pending {
			result, err := tx.db.ExecContext(ctx, `
				UPDATE tater_tts_requests
				SET status = 'processing', core_id = ?, updated_at = ?
				WHERE id = ? AND status = 'pending'
			`, coreID, now, item.ID)
			if err != nil {
				return err
			}
			count, _ := result.RowsAffected()
			if count == 1 {
				item.CoreID = coreID
				item.Status = "processing"
				item.UpdatedAt = now
				claimed = append(claimed, item)
			}
		}
		return nil
	})
	return claimed, err
}

func (r *Repository) CompleteTaterTTSRequest(
	ctx context.Context,
	id, coreID, audioBase64, contentType, errorMessage string,
	now time.Time,
) error {
	status := "ready"
	if errorMessage != "" {
		status = "failed"
		audioBase64 = ""
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE tater_tts_requests
		SET status = ?, audio_base64 = ?, content_type = ?, error = ?, updated_at = ?
		WHERE id = ? AND core_id = ? AND status = 'processing'
	`, status, audioBase64, contentType, errorMessage, now, id, coreID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *Repository) CancelTaterTTSRequest(ctx context.Context, id, playerID string, now time.Time) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE tater_tts_requests
		SET status = 'canceled', audio_base64 = '', updated_at = ?
		WHERE id = ? AND player_id = ? AND status NOT IN ('canceled', 'failed')
	`, now, id, playerID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}
