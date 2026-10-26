-- +goose Up
-- Immutable budget plan revisions. No mutable cash/balance or duplicate ledger postings.
ALTER TABLE finance_categories ADD CONSTRAINT finance_categories_kind_ref
    UNIQUE (workspace_id,id,currency,kind);

CREATE TABLE monthly_budget_events (
    id TEXT PRIMARY KEY CHECK(length(id)=32),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    currency TEXT NOT NULL CHECK(currency IN ('AED','PHP')),
    budget_month DATE NOT NULL CHECK(
        budget_month BETWEEN DATE '2000-01-01' AND DATE '2100-12-01'
        AND EXTRACT(DAY FROM budget_month)=1
    ),
    action TEXT NOT NULL CHECK(action IN ('allocation','policy')),
    category_id TEXT,
    category_kind TEXT NOT NULL DEFAULT 'expense' CHECK(category_kind='expense'),
    amount_minor BIGINT CHECK(amount_minor BETWEEN 0 AND 99999999999999),
    rollover_policy TEXT CHECK(rollover_policy IN ('none','carry_positive')),
    idempotency_key TEXT NOT NULL CHECK(length(idempotency_key)=32),
    fingerprint BYTEA NOT NULL CHECK(length(fingerprint)=32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (action='allocation' AND category_id IS NOT NULL
         AND amount_minor IS NOT NULL AND rollover_policy IS NULL)
        OR
        (action='policy' AND category_id IS NULL
         AND amount_minor IS NULL AND rollover_policy IS NOT NULL)
    ),
    FOREIGN KEY(workspace_id,category_id,currency,category_kind)
        REFERENCES finance_categories(workspace_id,id,currency,kind) ON DELETE RESTRICT,
    UNIQUE(workspace_id,idempotency_key)
);
CREATE INDEX monthly_budget_history ON monthly_budget_events
    (workspace_id,currency,budget_month,created_at,id);
CREATE TRIGGER monthly_budget_immutable BEFORE UPDATE OR DELETE
    ON monthly_budget_events FOR EACH ROW EXECUTE FUNCTION ledger_immutable();

UPDATE app_metadata SET value='group10',updated_at=now() WHERE key='schema_marker';

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS(SELECT 1 FROM monthly_budget_events) THEN
        RAISE EXCEPTION 'Cannot roll back Group 10: immutable budget history exists';
    END IF;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER monthly_budget_immutable ON monthly_budget_events;
DROP TABLE monthly_budget_events;
ALTER TABLE finance_categories DROP CONSTRAINT finance_categories_kind_ref;
UPDATE app_metadata SET value='group9',updated_at=now() WHERE key='schema_marker';
