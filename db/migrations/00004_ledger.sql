-- +goose Up
-- Every journal is single-currency, with immutable balanced postings.
CREATE TABLE ledger_accounts (
    id TEXT PRIMARY KEY CHECK (length(id) = 32),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    currency TEXT NOT NULL CHECK (currency IN ('AED', 'PHP')),
    kind TEXT NOT NULL CHECK (kind IN ('asset', 'liability', 'equity')),
    source_account_id TEXT,
    system_key TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((source_account_id IS NOT NULL) <> (system_key IS NOT NULL)),
    CHECK (system_key IS NULL OR (system_key = 'opening_equity' AND kind = 'equity')),
    CHECK (source_account_id IS NULL OR kind IN ('asset', 'liability')),
    FOREIGN KEY (workspace_id,source_account_id,currency)
        REFERENCES accounts(workspace_id,id,currency) ON DELETE RESTRICT,
    UNIQUE (workspace_id,id,currency),
    UNIQUE (workspace_id,currency,source_account_id),
    UNIQUE (workspace_id,currency,system_key)
);

CREATE TABLE ledger_journals (
    id TEXT PRIMARY KEY CHECK (length(id) = 32),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    currency TEXT NOT NULL CHECK (currency IN ('AED', 'PHP')),
    idempotency_key TEXT NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
    kind TEXT NOT NULL CHECK (kind IN ('opening', 'adjustment', 'reversal')),
    fingerprint BYTEA NOT NULL CHECK (length(fingerprint) = 32),
    reverses_journal_id TEXT,
    sealed BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((kind = 'reversal') = (reverses_journal_id IS NOT NULL)),
    UNIQUE (workspace_id,id,currency),
    UNIQUE (workspace_id,idempotency_key),
    UNIQUE (workspace_id,reverses_journal_id),
    FOREIGN KEY (workspace_id,reverses_journal_id,currency)
        REFERENCES ledger_journals(workspace_id,id,currency) ON DELETE RESTRICT
);

CREATE TABLE ledger_lines (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    journal_id TEXT NOT NULL,
    ledger_account_id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    currency TEXT NOT NULL CHECK (currency IN ('AED', 'PHP')),
    debit_minor BIGINT NOT NULL DEFAULT 0 CHECK (debit_minor BETWEEN 0 AND 99999999999999),
    credit_minor BIGINT NOT NULL DEFAULT 0 CHECK (credit_minor BETWEEN 0 AND 99999999999999),
    CHECK ((debit_minor > 0) <> (credit_minor > 0)),
    FOREIGN KEY (workspace_id,journal_id,currency)
        REFERENCES ledger_journals(workspace_id,id,currency) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id,ledger_account_id,currency)
        REFERENCES ledger_accounts(workspace_id,id,currency) ON DELETE RESTRICT,
    UNIQUE (journal_id,ledger_account_id)
);
CREATE INDEX ledger_lines_account_idx ON ledger_lines (workspace_id,ledger_account_id,id);

CREATE TABLE ledger_opening_imports (
    account_id TEXT PRIMARY KEY REFERENCES account_openings(account_id) ON DELETE RESTRICT,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    journal_id TEXT UNIQUE REFERENCES ledger_journals(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Reject UPDATE and DELETE for the entire accounting history.
-- +goose StatementBegin
CREATE FUNCTION ledger_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'ledger history is append-only';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER ledger_accounts_immutable BEFORE UPDATE OR DELETE ON ledger_accounts
    FOR EACH ROW EXECUTE FUNCTION ledger_immutable();
-- An entry may transition from draft to sealed only within its creating
-- transaction. Once sealed, its metadata and lines can never be changed.
-- +goose StatementBegin
CREATE FUNCTION ledger_guard_journal_update() RETURNS trigger AS $$
BEGIN
    IF TG_OP='UPDATE' AND OLD.sealed=FALSE AND NEW.sealed=TRUE
       AND (to_jsonb(OLD)-'sealed')=(to_jsonb(NEW)-'sealed') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'journal is immutable after sealing';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER ledger_journals_immutable BEFORE UPDATE OR DELETE ON ledger_journals
    FOR EACH ROW EXECUTE FUNCTION ledger_guard_journal_update();
CREATE TRIGGER ledger_lines_immutable BEFORE UPDATE OR DELETE ON ledger_lines
    FOR EACH ROW EXECUTE FUNCTION ledger_immutable();
CREATE TRIGGER ledger_openings_immutable BEFORE UPDATE OR DELETE ON ledger_opening_imports
    FOR EACH ROW EXECUTE FUNCTION ledger_immutable();

-- Acquire a lock on the journal before each line insert. No new lines may
-- ever attach to a previously sealed journal, even via direct SQL.
-- +goose StatementBegin
CREATE FUNCTION ledger_allow_line_insert() RETURNS trigger AS $$
DECLARE
    is_sealed BOOLEAN;
BEGIN
    SELECT sealed INTO is_sealed FROM ledger_journals
    WHERE id=NEW.journal_id AND workspace_id=NEW.workspace_id
    AND currency=NEW.currency FOR UPDATE;
    IF is_sealed IS DISTINCT FROM FALSE THEN
        RAISE EXCEPTION 'cannot append to sealed or missing journal';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER ledger_lines_insert_guard BEFORE INSERT ON ledger_lines
    FOR EACH ROW EXECUTE FUNCTION ledger_allow_line_insert();

-- Deferred journal-level check prevents committed unbalanced/empty journals,
-- including writers that bypass the Go application.
-- +goose StatementBegin
CREATE FUNCTION ledger_assert_balanced() RETURNS trigger AS $$
DECLARE
    line_count BIGINT;
    debit_total NUMERIC;
    credit_total NUMERIC;
    is_sealed BOOLEAN;
BEGIN
    SELECT sealed INTO is_sealed FROM ledger_journals WHERE id=NEW.id;
    SELECT COUNT(*), COALESCE(SUM(debit_minor::numeric),0),
           COALESCE(SUM(credit_minor::numeric),0)
    INTO line_count,debit_total,credit_total
    FROM ledger_lines WHERE journal_id=NEW.id;
    IF is_sealed IS DISTINCT FROM TRUE OR line_count < 2 OR debit_total <> credit_total THEN
        RAISE EXCEPTION 'journal % is not balanced', NEW.id
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER ledger_journal_balance
    AFTER INSERT ON ledger_journals DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION ledger_assert_balanced();

UPDATE app_metadata SET value='group7', updated_at=now() WHERE key='schema_marker';

-- +goose Down
DROP TRIGGER ledger_journal_balance ON ledger_journals;
DROP FUNCTION ledger_assert_balanced();
DROP TRIGGER ledger_lines_insert_guard ON ledger_lines;
DROP FUNCTION ledger_allow_line_insert();
DROP TABLE ledger_opening_imports;
DROP TABLE ledger_lines;
DROP TABLE ledger_journals;
DROP TABLE ledger_accounts;
DROP FUNCTION ledger_guard_journal_update();
DROP FUNCTION ledger_immutable();
UPDATE app_metadata SET value='group6', updated_at=now() WHERE key='schema_marker';
