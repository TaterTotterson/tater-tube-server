-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS playback_history (
    id            TEXT PRIMARY KEY,
    started_at    DATETIME NOT NULL,
    last_activity DATETIME NOT NULL,
    payload       TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_playback_history_activity
    ON playback_history(last_activity DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_playback_history_activity;
DROP TABLE IF EXISTS playback_history;
-- +goose StatementEnd
