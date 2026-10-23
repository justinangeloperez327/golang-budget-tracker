-- +goose Up
-- Group 6: account identity and immutable initial snapshots; NOT ledger postings.
CREATE TABLE accounts (
    id TEXT PRIMARY KEY CHECK (length(id) = 32),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 80),
    kind TEXT NOT NULL CHECK (kind IN ('cash', 'bank', 'savings', 'ewallet', 'credit', 'loan')),
    currency TEXT NOT NULL CHECK (currency IN ('AED', 'PHP')),
    setup_key TEXT NOT NULL CHECK (length(setup_key) = 32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at TIMESTAMPTZ,
    UNIQUE (workspace_id, id, currency),
    UNIQUE (workspace_id, setup_key)
);
CREATE INDEX accounts_workspace_idx ON accounts (workspace_id, created_at DESC, id DESC);

CREATE TABLE account_openings (
    account_id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    currency TEXT NOT NULL CHECK (currency IN ('AED', 'PHP')),
    amount_minor BIGINT NOT NULL CHECK (amount_minor BETWEEN 0 AND 99999999999999),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id, account_id, currency)
        REFERENCES accounts (workspace_id, id, currency) ON DELETE RESTRICT
);

-- Group 7 will consume these durable opening records into balanced opening
-- equity/liability journal entries. Disallow silent changes to the snapshot.
-- +goose StatementBegin
CREATE FUNCTION prevent_account_opening_change() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'account openings are immutable; record a ledger adjustment instead';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER account_opening_immutable BEFORE UPDATE OR DELETE
    ON account_openings FOR EACH ROW EXECUTE FUNCTION prevent_account_opening_change();

UPDATE app_metadata SET value = 'group6', updated_at = now() WHERE key = 'schema_marker';

-- +goose Down
DROP TRIGGER account_opening_immutable ON account_openings;
DROP FUNCTION prevent_account_opening_change();
DROP TABLE account_openings;
DROP TABLE accounts;
UPDATE app_metadata SET value = 'group4', updated_at = now() WHERE key = 'schema_marker';
