-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS tater_tts_requests (
    id           TEXT PRIMARY KEY,
    profile_id   TEXT NOT NULL DEFAULT 'household',
    player_id    TEXT NOT NULL,
    core_id      TEXT NOT NULL DEFAULT '',
    text         TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    audio_base64 TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL DEFAULT 'audio/wav',
    error        TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL,
    expires_at   DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tater_tts_pending
    ON tater_tts_requests(status, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_tater_tts_player
    ON tater_tts_requests(player_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_tater_tts_player;
DROP INDEX IF EXISTS idx_tater_tts_pending;
DROP TABLE IF EXISTS tater_tts_requests;
-- +goose StatementEnd
