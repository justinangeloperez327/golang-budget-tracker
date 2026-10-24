-- +goose Up
-- Categories have dedicated income/expense ledger accounts, never opening equity.
ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_kind_check;
ALTER TABLE ledger_accounts ADD CONSTRAINT ledger_accounts_kind_check
  CHECK (kind IN ('asset','liability','equity','income','expense'));
ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_check1;
ALTER TABLE ledger_accounts ADD CONSTRAINT ledger_accounts_check1
  CHECK (system_key IS NULL
    OR (system_key = 'opening_equity' AND kind = 'equity')
    OR (left(system_key,9) = 'category:' AND kind IN ('income','expense')));
ALTER TABLE ledger_accounts ADD CONSTRAINT ledger_accounts_workspace_currency_kind_unique
  UNIQUE (workspace_id,id,currency,kind);

CREATE TABLE finance_categories (
    id TEXT PRIMARY KEY CHECK (length(id)=32),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 80),
    kind TEXT NOT NULL CHECK (kind IN ('income','expense')),
    currency TEXT NOT NULL CHECK (currency IN ('AED','PHP')),
    ledger_account_id TEXT NOT NULL UNIQUE,
    archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id,ledger_account_id,currency,kind)
      REFERENCES ledger_accounts(workspace_id,id,currency,kind) ON DELETE RESTRICT,
    UNIQUE(workspace_id,id,currency),
    UNIQUE(workspace_id,kind,currency,name)
);
CREATE INDEX finance_categories_list ON finance_categories (workspace_id,kind,currency,created_at);

CREATE TABLE finance_transactions (
    id TEXT PRIMARY KEY CHECK(length(id)=32),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL,
    currency TEXT NOT NULL CHECK(currency IN ('AED','PHP')),
    kind TEXT NOT NULL CHECK (kind IN ('income','expense','refund')),
    total_minor BIGINT NOT NULL CHECK(total_minor BETWEEN 1 AND 99999999999999),
    journal_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 128),
    request_hash BYTEA NOT NULL CHECK(length(request_hash)=32),
    refund_of TEXT,
    note TEXT NOT NULL DEFAULT '' CHECK(char_length(note)<=500),
    occurred_on DATE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((kind='refund')=(refund_of IS NOT NULL)),
    FOREIGN KEY (workspace_id,account_id,currency)
      REFERENCES accounts(workspace_id,id,currency) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id,journal_id,currency)
      REFERENCES ledger_journals(workspace_id,id,currency) ON DELETE RESTRICT,
    UNIQUE(workspace_id,id,currency),
    UNIQUE(workspace_id,idempotency_key),
    UNIQUE(workspace_id,journal_id),
    FOREIGN KEY (workspace_id,refund_of,currency)
      REFERENCES finance_transactions(workspace_id,id,currency) ON DELETE RESTRICT
);
CREATE INDEX finance_transactions_history ON finance_transactions
  (workspace_id,occurred_on DESC,created_at DESC,id DESC);
CREATE INDEX finance_transactions_refund ON finance_transactions(workspace_id,refund_of);

CREATE TABLE finance_transaction_splits (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    transaction_id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    currency TEXT NOT NULL CHECK(currency IN ('AED','PHP')),
    category_id TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK(amount_minor BETWEEN 1 AND 99999999999999),
    FOREIGN KEY (workspace_id,transaction_id,currency)
      REFERENCES finance_transactions(workspace_id,id,currency) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id,category_id,currency)
      REFERENCES finance_categories(workspace_id,id,currency) ON DELETE RESTRICT,
    UNIQUE(transaction_id,category_id)
);

-- +goose StatementBegin
CREATE FUNCTION finance_assert_splits() RETURNS trigger AS $$
DECLARE actual NUMERIC;
BEGIN
    SELECT COALESCE(SUM(amount_minor::numeric),0) INTO actual
      FROM finance_transaction_splits WHERE transaction_id=NEW.id;
    IF actual <> NEW.total_minor THEN
        RAISE EXCEPTION 'transaction split total differs from journal amount'
        USING ERRCODE='23514';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER finance_splits_total
    AFTER INSERT ON finance_transactions DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION finance_assert_splits();

CREATE TRIGGER finance_transactions_immutable BEFORE UPDATE OR DELETE
    ON finance_transactions FOR EACH ROW EXECUTE FUNCTION ledger_immutable();
CREATE TRIGGER finance_splits_immutable BEFORE UPDATE OR DELETE
    ON finance_transaction_splits FOR EACH ROW EXECUTE FUNCTION ledger_immutable();

-- +goose StatementBegin
CREATE FUNCTION finance_category_archive_only() RETURNS trigger AS $$
BEGIN
    IF TG_OP='UPDATE' AND OLD.archived_at IS NULL AND NEW.archived_at IS NOT NULL
       AND (to_jsonb(OLD)-'archived_at')=(to_jsonb(NEW)-'archived_at') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'categories can only be archived';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER finance_categories_guard BEFORE UPDATE OR DELETE
    ON finance_categories FOR EACH ROW EXECUTE FUNCTION finance_category_archive_only();

UPDATE app_metadata SET value='group8',updated_at=now() WHERE key='schema_marker';

-- +goose Down
-- A down migration with real posted category journals is intentionally blocked.
-- This protects immutable ledger history from losing its classification.
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS(SELECT 1 FROM finance_transactions) OR EXISTS(SELECT 1 FROM finance_categories) THEN
     RAISE EXCEPTION 'Group 8 rollback requires an unused database';
  END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER finance_categories_guard ON finance_categories;
DROP FUNCTION finance_category_archive_only();
DROP TRIGGER finance_splits_immutable ON finance_transaction_splits;
DROP TRIGGER finance_transactions_immutable ON finance_transactions;
DROP TRIGGER finance_splits_total ON finance_transactions;
DROP FUNCTION finance_assert_splits();
DROP TABLE finance_transaction_splits;
DROP TABLE finance_transactions;
DROP TABLE finance_categories;
ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_workspace_currency_kind_unique;
ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_check1;
ALTER TABLE ledger_accounts ADD CONSTRAINT ledger_accounts_check1
 CHECK(system_key IS NULL OR (system_key='opening_equity' AND kind='equity'));
ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_kind_check;
ALTER TABLE ledger_accounts ADD CONSTRAINT ledger_accounts_kind_check
 CHECK(kind IN ('asset','liability','equity'));
UPDATE app_metadata SET value='group7',updated_at=now() WHERE key='schema_marker';
