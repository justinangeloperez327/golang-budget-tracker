-- +goose Up
-- Group 15 obligations are planning records, NOT new accounting entries.
-- PHP only; regular contributions reference expense splits, MP2 asset transfers.
CREATE TABLE contribution_obligations (
 id TEXT PRIMARY KEY CHECK(length(id)=32),
 workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
 currency TEXT NOT NULL DEFAULT 'PHP' CHECK(currency='PHP'),
 scheme TEXT NOT NULL CHECK(scheme IN ('sss','philhealth','pagibig','mp2')),
 period_on DATE NOT NULL CHECK (
   period_on BETWEEN DATE '2000-01-01' AND DATE '2100-12-01'
   AND EXTRACT(DAY FROM period_on)=1),
 due_on DATE CHECK (due_on IS NULL OR
   (due_on BETWEEN DATE '2000-01-01' AND DATE '2100-12-31' AND due_on>=period_on)),
 amount_minor BIGINT NOT NULL CHECK(amount_minor BETWEEN 1 AND 99999999999999),
 expense_category_id TEXT,
 mp2_savings_account_id TEXT,
 note TEXT NOT NULL DEFAULT '' CHECK(char_length(note)<=500),
 idempotency_key TEXT NOT NULL CHECK(length(idempotency_key)=32),
 fingerprint BYTEA NOT NULL CHECK(octet_length(fingerprint)=32),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK (
   (scheme='mp2' AND mp2_savings_account_id IS NOT NULL AND expense_category_id IS NULL)
   OR
   (scheme<>'mp2' AND expense_category_id IS NOT NULL AND mp2_savings_account_id IS NULL)
 ),
 FOREIGN KEY(workspace_id,expense_category_id,currency)
   REFERENCES finance_categories(workspace_id,id,currency) ON DELETE RESTRICT,
 FOREIGN KEY(workspace_id,mp2_savings_account_id,currency)
   REFERENCES accounts(workspace_id,id,currency) ON DELETE RESTRICT,
 UNIQUE(workspace_id,id),
 UNIQUE(workspace_id,scheme,period_on),
 UNIQUE(workspace_id,idempotency_key)
);
CREATE INDEX contribution_obligations_history ON contribution_obligations
 (workspace_id,period_on DESC,created_at DESC,id DESC);

CREATE TABLE contribution_resolutions (
 id TEXT PRIMARY KEY CHECK(length(id)=32),
 workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
 obligation_id TEXT NOT NULL,
 currency TEXT NOT NULL DEFAULT 'PHP' CHECK(currency='PHP'),
 action TEXT NOT NULL CHECK(action IN ('paid','skipped')),
 expense_transaction_id TEXT,
 transfer_id TEXT,
 resolved_on DATE NOT NULL CHECK(resolved_on BETWEEN DATE '2000-01-01' AND DATE '2100-12-31'),
 note TEXT NOT NULL DEFAULT '' CHECK(char_length(note)<=500),
 idempotency_key TEXT NOT NULL CHECK(length(idempotency_key)=32),
 fingerprint BYTEA NOT NULL CHECK(octet_length(fingerprint)=32),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK((action='paid') = (expense_transaction_id IS NOT NULL OR transfer_id IS NOT NULL)),
 CHECK(NOT (expense_transaction_id IS NOT NULL AND transfer_id IS NOT NULL)),
 FOREIGN KEY(workspace_id,obligation_id)
   REFERENCES contribution_obligations(workspace_id,id) ON DELETE RESTRICT,
 FOREIGN KEY(workspace_id,expense_transaction_id,currency)
   REFERENCES finance_transactions(workspace_id,id,currency) ON DELETE RESTRICT,
 FOREIGN KEY(workspace_id,transfer_id)
   REFERENCES transfer_operations(workspace_id,id) ON DELETE RESTRICT,
 UNIQUE(workspace_id,obligation_id),
 UNIQUE(workspace_id,idempotency_key),
 UNIQUE(workspace_id,expense_transaction_id),
 UNIQUE(workspace_id,transfer_id)
);
CREATE INDEX contribution_resolutions_history ON contribution_resolutions
 (workspace_id,resolved_on DESC,created_at DESC,id DESC);

-- Validate scheme-specific accounting links, even for direct database writers.
-- +goose StatementBegin
CREATE FUNCTION contribution_validate_obligation() RETURNS trigger AS $$
DECLARE category_kind TEXT;
DECLARE goal_kind TEXT;
BEGIN
 IF NEW.scheme='mp2' THEN
   SELECT kind INTO goal_kind FROM accounts
     WHERE workspace_id=NEW.workspace_id AND id=NEW.mp2_savings_account_id
       AND currency='PHP' AND archived_at IS NULL;
   IF goal_kind IS DISTINCT FROM 'savings' THEN
     RAISE EXCEPTION 'MP2 target must be an active PHP savings account' USING ERRCODE='23514';
   END IF;
 ELSE
   SELECT kind INTO category_kind FROM finance_categories
     WHERE workspace_id=NEW.workspace_id AND id=NEW.expense_category_id
       AND currency='PHP' AND archived_at IS NULL;
   IF category_kind IS DISTINCT FROM 'expense' THEN
     RAISE EXCEPTION 'contribution category must be an active PHP expense category' USING ERRCODE='23514';
   END IF;
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER contribution_obligation_validate BEFORE INSERT ON contribution_obligations
 FOR EACH ROW EXECUTE FUNCTION contribution_validate_obligation();

-- +goose StatementBegin
CREATE FUNCTION contribution_validate_resolution() RETURNS trigger AS $$
DECLARE o contribution_obligations%ROWTYPE;
DECLARE matches BOOLEAN;
BEGIN
 SELECT * INTO o FROM contribution_obligations
  WHERE workspace_id=NEW.workspace_id AND id=NEW.obligation_id;
 IF NOT FOUND THEN
   RAISE EXCEPTION 'contribution obligation not found' USING ERRCODE='23503';
 END IF;
 IF NEW.action='paid' AND o.scheme='mp2' THEN
   IF NEW.expense_transaction_id IS NOT NULL OR NEW.transfer_id IS NULL THEN
     RAISE EXCEPTION 'MP2 requires an existing transfer, not an expense' USING ERRCODE='23514';
   END IF;
   SELECT EXISTS(
     SELECT 1 FROM transfer_operations t
     JOIN accounts source ON source.workspace_id=t.workspace_id AND source.id=t.source_account_id
     JOIN accounts target ON target.workspace_id=t.workspace_id AND target.id=t.target_account_id
     WHERE t.workspace_id=NEW.workspace_id AND t.id=NEW.transfer_id
       AND t.source_currency='PHP' AND t.target_currency='PHP'
       AND t.send_minor=o.amount_minor AND t.receive_minor=o.amount_minor AND t.fee_minor=0
       AND t.target_account_id=o.mp2_savings_account_id
       AND source.kind IN ('cash','bank','savings','ewallet') AND target.kind='savings'
   ) INTO matches;
   IF NOT matches THEN
     RAISE EXCEPTION 'MP2 transfer does not match obligation' USING ERRCODE='23514';
   END IF;
 ELSIF NEW.action='paid' THEN
   IF NEW.transfer_id IS NOT NULL OR NEW.expense_transaction_id IS NULL THEN
     RAISE EXCEPTION 'regular contribution requires an expense, not a transfer' USING ERRCODE='23514';
   END IF;
   SELECT EXISTS(
     SELECT 1 FROM finance_transactions t
     JOIN finance_transaction_splits split
       ON split.transaction_id=t.id AND split.workspace_id=t.workspace_id
     WHERE t.id=NEW.expense_transaction_id AND t.workspace_id=NEW.workspace_id
       AND t.kind='expense' AND t.currency='PHP'
       AND split.category_id=o.expense_category_id AND split.amount_minor=o.amount_minor
       AND NOT EXISTS (
         SELECT 1 FROM recurring_occurrence_actions r
           WHERE r.workspace_id=NEW.workspace_id AND r.expense_transaction_id=t.id
       )
   ) INTO matches;
   IF NOT matches THEN
     RAISE EXCEPTION 'contribution expense/category amount does not match' USING ERRCODE='23514';
   END IF;
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER contribution_resolution_validate BEFORE INSERT ON contribution_resolutions
 FOR EACH ROW EXECUTE FUNCTION contribution_validate_resolution();

-- Avoid double-classification of an expense as both a recurring payment
-- and a government contribution, regardless of which status is created first.
-- +goose StatementBegin
CREATE FUNCTION contribution_guard_recurring_expense() RETURNS trigger AS $
BEGIN
 IF NEW.action='paid' AND NEW.expense_transaction_id IS NOT NULL
   AND EXISTS(SELECT 1 FROM contribution_resolutions r
    WHERE r.workspace_id=NEW.workspace_id AND r.expense_transaction_id=NEW.expense_transaction_id) THEN
   RAISE EXCEPTION 'expense already used by government contribution'
     USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER recurring_contribution_exclusive BEFORE INSERT ON recurring_occurrence_actions
 FOR EACH ROW EXECUTE FUNCTION contribution_guard_recurring_expense();

CREATE TRIGGER contribution_obligations_immutable BEFORE UPDATE OR DELETE
 ON contribution_obligations FOR EACH ROW EXECUTE FUNCTION ledger_immutable();
CREATE TRIGGER contribution_resolutions_immutable BEFORE UPDATE OR DELETE
 ON contribution_resolutions FOR EACH ROW EXECUTE FUNCTION ledger_immutable();

UPDATE app_metadata SET value='group15',updated_at=now() WHERE key='schema_marker';

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM contribution_obligations) OR EXISTS(SELECT 1 FROM contribution_resolutions) THEN
   RAISE EXCEPTION 'Group 15 has immutable contribution records; downgrade is unsafe';
 END IF;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER recurring_contribution_exclusive ON recurring_occurrence_actions;
DROP FUNCTION contribution_guard_recurring_expense();
DROP TRIGGER contribution_resolutions_immutable ON contribution_resolutions;
DROP TRIGGER contribution_obligations_immutable ON contribution_obligations;
DROP TRIGGER contribution_resolution_validate ON contribution_resolutions;
DROP TRIGGER contribution_obligation_validate ON contribution_obligations;
DROP FUNCTION contribution_validate_resolution();
DROP FUNCTION contribution_validate_obligation();
DROP TABLE contribution_resolutions;
DROP TABLE contribution_obligations;
UPDATE app_metadata SET value='group14',updated_at=now() WHERE key='schema_marker';
