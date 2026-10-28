-- +goose Up
-- Group 12: debt contracts and installments reference existing loan balances.
-- Originating a plan NEVER creates principal or interest in the ledger.
ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_check1;
ALTER TABLE ledger_accounts ADD CONSTRAINT ledger_accounts_check1 CHECK (
    system_key IS NULL
    OR (system_key='opening_equity' AND kind='equity')
    OR (left(system_key,9)='category:' AND kind IN ('income','expense'))
    OR (system_key IN ('transfer_fee','loan_interest') AND kind='expense')
    OR (system_key IN ('fx_clearing','reconciliation_equity') AND kind='equity')
);

CREATE TABLE debt_plans (
    id TEXT PRIMARY KEY CHECK(length(id)=32),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    currency TEXT NOT NULL CHECK(currency IN ('AED','PHP')),
    name TEXT NOT NULL CHECK(char_length(btrim(name)) BETWEEN 1 AND 100),
    loan_account_id TEXT NOT NULL,
    funding_account_id TEXT NOT NULL,
    principal_minor BIGINT NOT NULL CHECK(principal_minor BETWEEN 1 AND 99999999999999),
    annual_rate_bps INTEGER NOT NULL CHECK(annual_rate_bps BETWEEN 0 AND 100000),
    installments SMALLINT NOT NULL CHECK(installments BETWEEN 1 AND 360),
    first_due_on DATE NOT NULL CHECK(first_due_on BETWEEN DATE '2000-01-01' AND DATE '2100-12-31'),
    idempotency_key TEXT NOT NULL CHECK(length(idempotency_key)=32),
    fingerprint BYTEA NOT NULL CHECK(length(fingerprint)=32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(workspace_id,id,currency),
    UNIQUE(workspace_id,loan_account_id),
    UNIQUE(workspace_id,idempotency_key),
    FOREIGN KEY(workspace_id,loan_account_id,currency)
      REFERENCES accounts(workspace_id,id,currency) ON DELETE RESTRICT,
    FOREIGN KEY(workspace_id,funding_account_id,currency)
      REFERENCES accounts(workspace_id,id,currency) ON DELETE RESTRICT,
    CHECK(loan_account_id<>funding_account_id),
    CHECK(principal_minor>=installments)
);

CREATE TABLE debt_installments (
    id TEXT PRIMARY KEY CHECK(length(id)=32),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    plan_id TEXT NOT NULL,
    currency TEXT NOT NULL CHECK(currency IN ('AED','PHP')),
    installment_number SMALLINT NOT NULL CHECK(installment_number BETWEEN 1 AND 360),
    due_on DATE NOT NULL,
    principal_minor BIGINT NOT NULL CHECK(principal_minor BETWEEN 1 AND 99999999999999),
    interest_minor BIGINT NOT NULL CHECK(interest_minor BETWEEN 0 AND 99999999999999),
    amount_minor BIGINT NOT NULL CHECK(amount_minor BETWEEN 1 AND 99999999999999),
    FOREIGN KEY(workspace_id,plan_id,currency)
      REFERENCES debt_plans(workspace_id,id,currency) ON DELETE RESTRICT,
    UNIQUE(workspace_id,id,currency),
    UNIQUE(workspace_id,plan_id,installment_number),
    CHECK(amount_minor::numeric=principal_minor::numeric+interest_minor::numeric)
);
CREATE INDEX debt_installments_due ON debt_installments(workspace_id,due_on,plan_id);

CREATE TABLE debt_payments (
    id TEXT PRIMARY KEY CHECK(length(id)=32),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    plan_id TEXT NOT NULL,
    installment_id TEXT NOT NULL,
    currency TEXT NOT NULL CHECK(currency IN ('AED','PHP')),
    funding_account_id TEXT NOT NULL,
    loan_account_id TEXT NOT NULL,
    principal_minor BIGINT NOT NULL CHECK(principal_minor BETWEEN 1 AND 99999999999999),
    interest_minor BIGINT NOT NULL CHECK(interest_minor BETWEEN 0 AND 99999999999999),
    amount_minor BIGINT NOT NULL,
    journal_id TEXT NOT NULL,
    paid_on DATE NOT NULL CHECK(paid_on BETWEEN DATE '2000-01-01' AND DATE '2100-12-31'),
    idempotency_key TEXT NOT NULL CHECK(length(idempotency_key)=32),
    fingerprint BYTEA NOT NULL CHECK(length(fingerprint)=32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK(amount_minor::numeric=principal_minor::numeric+interest_minor::numeric),
    FOREIGN KEY(workspace_id,plan_id,currency)
      REFERENCES debt_plans(workspace_id,id,currency) ON DELETE RESTRICT,
    FOREIGN KEY(workspace_id,installment_id,currency)
      REFERENCES debt_installments(workspace_id,id,currency) ON DELETE RESTRICT,
    FOREIGN KEY(workspace_id,funding_account_id,currency)
      REFERENCES accounts(workspace_id,id,currency) ON DELETE RESTRICT,
    FOREIGN KEY(workspace_id,loan_account_id,currency)
      REFERENCES accounts(workspace_id,id,currency) ON DELETE RESTRICT,
    FOREIGN KEY(workspace_id,journal_id,currency)
      REFERENCES ledger_journals(workspace_id,id,currency) ON DELETE RESTRICT,
    UNIQUE(workspace_id,installment_id),
    UNIQUE(workspace_id,idempotency_key),
    UNIQUE(workspace_id,journal_id)
);
CREATE INDEX debt_payments_history ON debt_payments(workspace_id,created_at DESC,id DESC);

-- +goose StatementBegin
CREATE FUNCTION debt_verify_payment() RETURNS trigger AS $$
DECLARE
 p debt_plans%ROWTYPE;
 i debt_installments%ROWTYPE;
 lines_ok BOOLEAN;
BEGIN
 SELECT * INTO p FROM debt_plans WHERE workspace_id=NEW.workspace_id
    AND id=NEW.plan_id AND currency=NEW.currency;
 IF NOT FOUND THEN
   RAISE EXCEPTION 'debt plan not found' USING ERRCODE='23503';
 END IF;
 SELECT * INTO i FROM debt_installments WHERE workspace_id=NEW.workspace_id
    AND id=NEW.installment_id AND plan_id=NEW.plan_id AND currency=NEW.currency;
 IF NOT FOUND THEN
   RAISE EXCEPTION 'debt installment does not belong to plan' USING ERRCODE='23514';
 END IF;
 IF NEW.loan_account_id<>p.loan_account_id OR
    NEW.funding_account_id<>p.funding_account_id OR
    NEW.principal_minor<>i.principal_minor OR
    NEW.interest_minor<>i.interest_minor OR
    NEW.amount_minor<>i.amount_minor THEN
   RAISE EXCEPTION 'debt payment differs from scheduled installment' USING ERRCODE='23514';
 END IF;
 -- The linked immutable journal must have exactly the three expected postings
 -- (or two when interest is zero). No unaccounted credits or debits.
 SELECT (
  COUNT(*) = CASE WHEN NEW.interest_minor>0 THEN 3 ELSE 2 END
  AND COALESCE(SUM(CASE WHEN la.source_account_id=NEW.funding_account_id
       AND l.credit_minor=NEW.amount_minor AND l.debit_minor=0 THEN 1 ELSE 0 END),0)=1
  AND COALESCE(SUM(CASE WHEN la.source_account_id=NEW.loan_account_id
       AND l.debit_minor=NEW.principal_minor AND l.credit_minor=0 THEN 1 ELSE 0 END),0)=1
  AND COALESCE(SUM(CASE WHEN la.system_key='loan_interest'
       AND l.debit_minor=NEW.interest_minor AND l.credit_minor=0 THEN 1 ELSE 0 END),0)
       =CASE WHEN NEW.interest_minor>0 THEN 1 ELSE 0 END
 ) INTO lines_ok
 FROM ledger_lines l
 JOIN ledger_accounts la ON la.id=l.ledger_account_id AND la.workspace_id=l.workspace_id
 WHERE l.workspace_id=NEW.workspace_id AND l.journal_id=NEW.journal_id
   AND l.currency=NEW.currency;
 IF NOT COALESCE(lines_ok,FALSE) THEN
   RAISE EXCEPTION 'debt payment journal does not match installment'
     USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER debt_payment_validate BEFORE INSERT ON debt_payments
 FOR EACH ROW EXECUTE FUNCTION debt_verify_payment();
CREATE TRIGGER debt_plans_immutable BEFORE UPDATE OR DELETE ON debt_plans
 FOR EACH ROW EXECUTE FUNCTION ledger_immutable();
CREATE TRIGGER debt_installments_immutable BEFORE UPDATE OR DELETE ON debt_installments
 FOR EACH ROW EXECUTE FUNCTION ledger_immutable();
CREATE TRIGGER debt_payments_immutable BEFORE UPDATE OR DELETE ON debt_payments
 FOR EACH ROW EXECUTE FUNCTION ledger_immutable();
UPDATE app_metadata SET value='group12',updated_at=now() WHERE key='schema_marker';

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM debt_plans) OR EXISTS(SELECT 1 FROM debt_payments) THEN
   RAISE EXCEPTION 'Cannot drop immutable debt contracts or payment history';
 END IF;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER debt_payments_immutable ON debt_payments;
DROP TRIGGER debt_installments_immutable ON debt_installments;
DROP TRIGGER debt_plans_immutable ON debt_plans;
DROP TRIGGER debt_payment_validate ON debt_payments;
DROP FUNCTION debt_verify_payment();
DROP TABLE debt_payments;
DROP TABLE debt_installments;
DROP TABLE debt_plans;
ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_check1;
ALTER TABLE ledger_accounts ADD CONSTRAINT ledger_accounts_check1 CHECK (
    system_key IS NULL
    OR (system_key='opening_equity' AND kind='equity')
    OR (left(system_key,9)='category:' AND kind IN ('income','expense'))
    OR (system_key='transfer_fee' AND kind='expense')
    OR (system_key IN ('fx_clearing','reconciliation_equity') AND kind='equity')
);
UPDATE app_metadata SET value='group11',updated_at=now() WHERE key='schema_marker';
