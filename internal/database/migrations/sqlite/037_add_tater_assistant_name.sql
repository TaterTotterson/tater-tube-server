-- +goose Up
-- +goose StatementBegin
ALTER TABLE tater_core_connections
    ADD COLUMN assistant_name TEXT NOT NULL DEFAULT 'Tater';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tater_core_connections DROP COLUMN assistant_name;
-- +goose StatementEnd
