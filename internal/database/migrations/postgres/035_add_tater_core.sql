-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS tater_core_pairing_codes (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    code_hash  TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS tater_core_connections (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    created_at   TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS tater_viewing_events (
    event_id      TEXT PRIMARY KEY,
    profile_id    TEXT NOT NULL DEFAULT 'household',
    player_id     TEXT NOT NULL,
    source        TEXT NOT NULL,
    media_id      TEXT NOT NULL,
    media_type    TEXT NOT NULL,
    title         TEXT NOT NULL,
    series_title  TEXT NOT NULL DEFAULT '',
    season        INTEGER NOT NULL DEFAULT 0,
    episode       INTEGER NOT NULL DEFAULT 0,
    position_ms   BIGINT NOT NULL DEFAULT 0,
    duration_ms   BIGINT NOT NULL DEFAULT 0,
    state         TEXT NOT NULL,
    occurred_at   TIMESTAMPTZ NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tater_viewing_profile_time
    ON tater_viewing_events(profile_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_tater_viewing_media
    ON tater_viewing_events(profile_id, media_id, occurred_at DESC);

CREATE TABLE IF NOT EXISTS tater_recommendation_batches (
    id           TEXT PRIMARY KEY,
    profile_id   TEXT NOT NULL DEFAULT 'household',
    core_id      TEXT NOT NULL,
    summary      TEXT NOT NULL DEFAULT '',
    generated_at TIMESTAMPTZ NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tater_recommendation_batches_active
    ON tater_recommendation_batches(profile_id, expires_at DESC, generated_at DESC);

CREATE TABLE IF NOT EXISTS tater_recommendations (
    id           TEXT PRIMARY KEY,
    batch_id     TEXT NOT NULL REFERENCES tater_recommendation_batches(id) ON DELETE CASCADE,
    rank         INTEGER NOT NULL,
    candidate_id TEXT NOT NULL,
    title        TEXT NOT NULL,
    media_type   TEXT NOT NULL,
    source       TEXT NOT NULL,
    reason       TEXT NOT NULL,
    launch_json  TEXT NOT NULL,
    feedback     TEXT NOT NULL DEFAULT '',
    feedback_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tater_recommendations_batch_rank
    ON tater_recommendations(batch_id, rank ASC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_tater_recommendations_batch_rank;
DROP TABLE IF EXISTS tater_recommendations;
DROP INDEX IF EXISTS idx_tater_recommendation_batches_active;
DROP TABLE IF EXISTS tater_recommendation_batches;
DROP INDEX IF EXISTS idx_tater_viewing_media;
DROP INDEX IF EXISTS idx_tater_viewing_profile_time;
DROP TABLE IF EXISTS tater_viewing_events;
DROP TABLE IF EXISTS tater_core_connections;
DROP TABLE IF EXISTS tater_core_pairing_codes;
-- +goose StatementEnd
