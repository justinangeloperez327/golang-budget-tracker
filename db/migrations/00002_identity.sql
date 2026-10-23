-- +goose Up
CREATE TABLE users (
    id TEXT PRIMARY KEY CHECK (length(id) = 32),
    email TEXT NOT NULL UNIQUE CHECK (length(email) BETWEEN 3 AND 254 AND email = lower(email)),
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE workspaces (
    id TEXT PRIMARY KEY CHECK (length(id) = 32),
    owner_user_id TEXT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    token_hash BYTEA PRIMARY KEY CHECK (length(token_hash) = 32),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token TEXT NOT NULL CHECK (length(csrf_token) = 64),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user_id_idx ON sessions(user_id);
CREATE INDEX sessions_expires_at_idx ON sessions(expires_at);

CREATE TABLE auth_attempts (
    key_hash BYTEA PRIMARY KEY CHECK (length(key_hash) = 32),
    count INTEGER NOT NULL CHECK (count >= 1),
    window_started TIMESTAMPTZ NOT NULL DEFAULT now()
);

UPDATE app_metadata SET value='group4', updated_at=now() WHERE key='schema_marker';

-- +goose Down
DELETE FROM auth_attempts;
DROP TABLE auth_attempts;
DROP TABLE sessions;
DROP TABLE workspaces;
DROP TABLE users;
UPDATE app_metadata SET value='group3', updated_at=now() WHERE key='schema_marker';
