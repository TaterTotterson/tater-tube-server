-- +goose Up
-- +goose StatementBegin
ALTER TABLE tater_recommendation_batches
    ADD COLUMN boot_summary TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tater_recommendation_batches DROP COLUMN boot_summary;
-- +goose StatementEnd
