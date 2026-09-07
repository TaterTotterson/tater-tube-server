-- +goose Up
-- +goose StatementBegin
ALTER TABLE tater_recommendation_batches
    ADD COLUMN picks_briefing TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tater_recommendation_batches DROP COLUMN picks_briefing;
-- +goose StatementEnd
