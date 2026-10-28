-- +goose Up
-- Group 13. Each savings goal exclusively owns an existing savings account
-- for allocation purposes; its progress is the live, ledger-derived balance.
CREATE TABLE savings_goals (
    id TEXT PRIMARY KEY CHECK (length(id)=32),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    currency TEXT NOT NULL CHECK (currency IN ('AED','PHP')),
    savings_account_id TEXT NOT NULL,
    name TEXT NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 100),
    target_minor BIGINT NOT NULL CHECK (target_minor BETWEEN 1 AND 99999999999999),
    target_on DATE CHECK (target_on IS NULL OR target_on BETWEEN DATE '2000-01-01' AND DATE '2100-12-31'),
    idempotency_key TEXT NOT NULL CHECK (length(idempotency_key)=32),
    fingerprint BYTEA NOT NULL CHECK (length(fingerprint)=32),
    archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id,savings_account_id,currency)
      REFERENCES accounts(workspace_id,id,currency) ON DELETE RESTRICT,
    UNIQUE (workspace_id,id,currency),
    UNIQUE (workspace_id,savings_account_id),
    UNIQUE (workspace_id,idempotency_key)
);
CREATE INDEX savings_goals_recent ON savings_goals(workspace_id,created_at DESC,id DESC);

CREATE TABLE savings_goal_movements (
    id TEXT PRIMARY KEY CHECK (length(id)=32),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    goal_id TEXT NOT NULL,
    currency TEXT NOT NULL CHECK(currency IN ('AED','PHP')),
    other_account_id TEXT NOT NULL,
    direction TEXT NOT NULL CHECK(direction IN ('contribution','withdrawal')),
    amount_minor BIGINT NOT NULL CHECK(amount_minor BETWEEN 1 AND 99999999999999),
    occurred_on DATE NOT NULL CHECK(occurred_on BETWEEN DATE '2000-01-01' AND DATE '2100-12-31'),
    journal_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL CHECK(length(idempotency_key)=32),
    fingerprint BYTEA NOT NULL CHECK(length(fingerprint)=32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY(workspace_id,goal_id,currency)
      REFERENCES savings_goals(workspace_id,id,currency) ON DELETE RESTRICT,
    FOREIGN KEY(workspace_id,other_account_id,currency)
      REFERENCES accounts(workspace_id,id,currency) ON DELETE RESTRICT,
    FOREIGN KEY(workspace_id,journal_id,currency)
      REFERENCES ledger_journals(workspace_id,id,currency) ON DELETE RESTRICT,
    UNIQUE(workspace_id,idempotency_key),
    UNIQUE(workspace_id,journal_id)
);
CREATE INDEX savings_goal_movements_recent ON savings_goal_movements(workspace_id,goal_id,occurred_on DESC,created_at DESC,id DESC);

-- +goose StatementBegin
CREATE FUNCTION savings_goal_guard() RETURNS trigger AS $$
BEGIN
    IF TG_OP='UPDATE' AND OLD.archived_at IS NULL AND NEW.archived_at IS NOT NULL
       AND (to_jsonb(OLD)-'archived_at')=(to_jsonb(NEW)-'archived_at') THEN
       RETURN NEW;
    END IF;
    RAISE EXCEPTION 'savings goal is immutable except for one-way archival';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER savings_goal_guard_trigger BEFORE UPDATE OR DELETE ON savings_goals
    FOR EACH ROW EXECUTE FUNCTION savings_goal_guard();

-- Database independently enforces the transfer's precise ledger effect.
-- +goose StatementBegin
CREATE FUNCTION savings_goal_validate_movement() RETURNS trigger AS $$
DECLARE
    saved_goal savings_goals%ROWTYPE;
    other_kind TEXT;
    ledger_valid BOOLEAN;
BEGIN
    SELECT * INTO saved_goal FROM savings_goals
      WHERE workspace_id=NEW.workspace_id AND id=NEW.goal_id AND currency=NEW.currency;
    IF NOT FOUND OR saved_goal.archived_at IS NOT NULL THEN
        RAISE EXCEPTION 'savings goal is missing or archived' USING ERRCODE='23514';
    END IF;
    IF saved_goal.savings_account_id=NEW.other_account_id THEN
        RAISE EXCEPTION 'source and target must differ' USING ERRCODE='23514';
    END IF;
    SELECT kind INTO other_kind FROM accounts WHERE workspace_id=NEW.workspace_id
      AND id=NEW.other_account_id AND currency=NEW.currency
      AND archived_at IS NULL;
    IF other_kind IS NULL OR other_kind NOT IN ('cash','bank','savings','ewallet') THEN
        RAISE EXCEPTION 'counterparty must be an active asset account' USING ERRCODE='23514';
    END IF;
    IF EXISTS (SELECT 1 FROM savings_goals
       WHERE workspace_id=NEW.workspace_id AND savings_account_id=NEW.other_account_id) THEN
        RAISE EXCEPTION 'another goal account cannot be used as counterparty' USING ERRCODE='23514';
    END IF;
    SELECT (
      COUNT(*)=2
      AND COALESCE(SUM(CASE WHEN a.source_account_id=saved_goal.savings_account_id
        AND ((NEW.direction='contribution' AND l.debit_minor=NEW.amount_minor AND l.credit_minor=0)
          OR (NEW.direction='withdrawal' AND l.credit_minor=NEW.amount_minor AND l.debit_minor=0))
        THEN 1 ELSE 0 END),0)=1
      AND COALESCE(SUM(CASE WHEN a.source_account_id=NEW.other_account_id
        AND ((NEW.direction='contribution' AND l.credit_minor=NEW.amount_minor AND l.debit_minor=0)
          OR (NEW.direction='withdrawal' AND l.debit_minor=NEW.amount_minor AND l.credit_minor=0))
        THEN 1 ELSE 0 END),0)=1
    ) INTO ledger_valid
    FROM ledger_lines l
      JOIN ledger_accounts a ON a.id=l.ledger_account_id AND a.workspace_id=l.workspace_id
    WHERE l.workspace_id=NEW.workspace_id AND l.currency=NEW.currency AND l.journal_id=NEW.journal_id;
    IF NOT COALESCE(ledger_valid,FALSE) THEN
        RAISE EXCEPTION 'savings goal movement must match its balanced journal' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER savings_goal_movement_verify BEFORE INSERT ON savings_goal_movements
    FOR EACH ROW EXECUTE FUNCTION savings_goal_validate_movement();
CREATE TRIGGER savings_goal_movement_immutable BEFORE UPDATE OR DELETE ON savings_goal_movements
    FOR EACH ROW EXECUTE FUNCTION ledger_immutable();

UPDATE app_metadata SET value='group13',updated_at=now() WHERE key='schema_marker';

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS(SELECT 1 FROM savings_goals) OR EXISTS(SELECT 1 FROM savings_goal_movements) THEN
        RAISE EXCEPTION 'Group 13 has immutable goals or transfers and cannot be rolled back';
    END IF;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER savings_goal_movement_immutable ON savings_goal_movements;
DROP TRIGGER savings_goal_movement_verify ON savings_goal_movements;
DROP FUNCTION savings_goal_validate_movement();
DROP TRIGGER savings_goal_guard_trigger ON savings_goals;
DROP FUNCTION savings_goal_guard();
DROP TABLE savings_goal_movements;
DROP TABLE savings_goals;
UPDATE app_metadata SET value='group12',updated_at=now() WHERE key='schema_marker';
