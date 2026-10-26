-- +goose Up
-- A recurring obligation is a schedule, not a ledger journal or an expense.
CREATE TABLE recurring_schedules (
    id TEXT PRIMARY KEY CHECK (length(id)=32),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL CHECK (length(idempotency_key)=32),
    fingerprint BYTEA NOT NULL CHECK (length(fingerprint)=32),
    name TEXT NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 100),
    kind TEXT NOT NULL CHECK (kind IN ('bill','subscription')),
    currency TEXT NOT NULL CHECK (currency IN ('AED','PHP')),
    account_id TEXT NOT NULL,
    category_id TEXT NOT NULL,
    category_kind TEXT NOT NULL DEFAULT 'expense' CHECK(category_kind='expense'),
    amount_minor BIGINT NOT NULL CHECK (amount_minor BETWEEN 1 AND 99999999999999),
    frequency TEXT NOT NULL CHECK (frequency IN ('daily','weekly','monthly','quarterly','yearly')),
    start_on DATE NOT NULL CHECK (start_on BETWEEN DATE '2000-01-01' AND DATE '2100-12-31'),
    end_on DATE CHECK(end_on IS NULL OR end_on>=start_on),
    archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id,account_id,currency)
        REFERENCES accounts(workspace_id,id,currency) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id,category_id,currency,category_kind)
        REFERENCES finance_categories(workspace_id,id,currency,kind) ON DELETE RESTRICT,
    UNIQUE (workspace_id,id,currency),
    UNIQUE (workspace_id,idempotency_key)
);
CREATE INDEX recurring_schedules_workspace ON recurring_schedules(workspace_id,archived_at,start_on,id);

-- Occurrence outcomes are append-only. 'paid' is only a reference to an
-- existing finance expense; recording a payment never posts a second journal.
CREATE TABLE recurring_occurrence_actions (
    id TEXT PRIMARY KEY CHECK (length(id)=32),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    schedule_id TEXT NOT NULL,
    currency TEXT NOT NULL CHECK(currency IN ('AED','PHP')),
    due_on DATE NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('paid','skipped')),
    expense_transaction_id TEXT,
    idempotency_key TEXT NOT NULL CHECK(length(idempotency_key)=32),
    fingerprint BYTEA NOT NULL CHECK(length(fingerprint)=32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((action='paid')=(expense_transaction_id IS NOT NULL)),
    FOREIGN KEY(workspace_id,schedule_id,currency)
      REFERENCES recurring_schedules(workspace_id,id,currency) ON DELETE RESTRICT,
    FOREIGN KEY(workspace_id,expense_transaction_id,currency)
      REFERENCES finance_transactions(workspace_id,id,currency) ON DELETE RESTRICT,
    UNIQUE(workspace_id,schedule_id,due_on),
    UNIQUE(workspace_id,idempotency_key),
    UNIQUE(workspace_id,expense_transaction_id)
);
CREATE INDEX recurring_actions_schedule ON recurring_occurrence_actions(workspace_id,schedule_id,due_on DESC);
CREATE TRIGGER recurring_actions_immutable BEFORE UPDATE OR DELETE
    ON recurring_occurrence_actions FOR EACH ROW EXECUTE FUNCTION ledger_immutable();

-- The schedule configuration cannot be rewritten after creation. Archiving
-- may only transition NULL -> non-NULL; pre-existing obligations remain visible.
-- +goose StatementBegin
CREATE FUNCTION recurring_schedule_archive_only() RETURNS trigger AS $$
BEGIN
    IF TG_OP='UPDATE' AND OLD.archived_at IS NULL AND NEW.archived_at IS NOT NULL
        AND (to_jsonb(OLD)-'archived_at')=(to_jsonb(NEW)-'archived_at') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'schedule config is immutable; archive and recreate';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER recurring_schedules_guard BEFORE UPDATE OR DELETE
    ON recurring_schedules FOR EACH ROW EXECUTE FUNCTION recurring_schedule_archive_only();

-- Verify paid occurrence link even for direct SQL writers, preventing any
-- cross-category, cross-account, wrong-kind or amount-mismatched payment link.
-- +goose StatementBegin
CREATE FUNCTION recurring_validate_payment() RETURNS trigger AS $$
DECLARE
    s recurring_schedules%ROWTYPE;
    ok_match BOOLEAN;
BEGIN
    SELECT * INTO s FROM recurring_schedules
      WHERE id=NEW.schedule_id AND workspace_id=NEW.workspace_id
       AND currency=NEW.currency;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'recurring schedule not found' USING ERRCODE='23503';
    END IF;
    IF NEW.action='paid' THEN
        SELECT EXISTS (
            SELECT 1 FROM finance_transactions t
            JOIN finance_transaction_splits sp
                ON sp.transaction_id=t.id AND sp.workspace_id=t.workspace_id
            WHERE t.id=NEW.expense_transaction_id
              AND t.workspace_id=NEW.workspace_id
              AND t.currency=NEW.currency
              AND t.kind='expense' AND t.account_id=s.account_id
              AND sp.category_id=s.category_id
              AND sp.amount_minor=s.amount_minor
        ) INTO ok_match;
        IF NOT ok_match THEN
            RAISE EXCEPTION 'linked expense does not match recurring obligation'
                USING ERRCODE='23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER recurring_validate_payment_insert BEFORE INSERT ON recurring_occurrence_actions
    FOR EACH ROW EXECUTE FUNCTION recurring_validate_payment();

UPDATE app_metadata SET value='group11',updated_at=now() WHERE key='schema_marker';

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM recurring_schedules) OR EXISTS (SELECT 1 FROM recurring_occurrence_actions) THEN
        RAISE EXCEPTION 'Group 11 contains recurring history; downgrade is not safe';
    END IF;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER recurring_validate_payment_insert ON recurring_occurrence_actions;
DROP FUNCTION recurring_validate_payment();
DROP TRIGGER recurring_schedules_guard ON recurring_schedules;
DROP FUNCTION recurring_schedule_archive_only();
DROP TRIGGER recurring_actions_immutable ON recurring_occurrence_actions;
DROP TABLE recurring_occurrence_actions;
DROP TABLE recurring_schedules;
UPDATE app_metadata SET value='group10',updated_at=now() WHERE key='schema_marker';
