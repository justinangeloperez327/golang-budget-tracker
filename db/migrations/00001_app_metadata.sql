-- +goose Up
-- Group 3 marker: future migrations introduce users and financial tables.
CREATE TABLE app_metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO app_metadata (key, value) VALUES ('schema_marker', 'group3');

-- +goose Down
DROP TABLE app_metadata;
