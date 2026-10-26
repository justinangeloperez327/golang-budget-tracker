-- +goose Up
-- Clear, separately balanced journals for same-currency and AED/PHP settlement.
ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_check1;
ALTER TABLE ledger_accounts ADD CONSTRAINT ledger_accounts_check1 CHECK (
    system_key IS NULL
    OR (system_key = 'opening_equity' AND kind='equity')
    OR (left(system_key,9)='category:' AND kind IN ('income','expense'))
    OR (system_key = 'transfer_fee' AND kind='expense')
    OR (system_key = 'fx_clearing' AND kind='equity')
    OR (system_key = 'reconciliation_equity' AND kind='equity')
);

CREATE TABLE transfer_operations (
    id TEXT PRIMARY KEY CHECK (length(id)=32),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL CHECK (length(idempotency_key)=32),
    fingerprint BYTEA NOT NULL CHECK (length(fingerprint)=32),
    source_account_id TEXT NOT NULL,
    target_account_id TEXT NOT NULL,
    source_currency TEXT NOT NULL CHECK (source_currency IN ('AED','PHP')),
    target_currency TEXT NOT NULL CHECK (target_currency IN ('AED','PHP')),
    send_minor BIGINT NOT NULL CHECK (send_minor BETWEEN 1 AND 99999999999999),
    receive_minor BIGINT NOT NULL CHECK (receive_minor BETWEEN 1 AND 99999999999999),
    fee_minor BIGINT NOT NULL CHECK (fee_minor BETWEEN 0 AND 99999999999999),
    rate_units BIGINT,
    note TEXT NOT NULL DEFAULT '' CHECK (char_length(note)<=500),
    occurred_on DATE NOT NULL,
    source_journal_id TEXT NOT NULL,
    target_journal_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (source_account_id<>target_account_id),
    CHECK ((source_currency=target_currency AND rate_units IS NULL
            AND send_minor=receive_minor AND target_journal_id IS NULL)
       OR (source_currency<>target_currency AND rate_units>0
            AND target_journal_id IS NOT NULL)),
    FOREIGN KEY (workspace_id,source_account_id,source_currency)
      REFERENCES accounts(workspace_id,id,currency) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id,target_account_id,target_currency)
      REFERENCES accounts(workspace_id,id,currency) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id,source_journal_id,source_currency)
      REFERENCES ledger_journals(workspace_id,id,currency) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id,target_journal_id,target_currency)
      REFERENCES ledger_journals(workspace_id,id,currency) ON DELETE RESTRICT,
    UNIQUE(workspace_id,idempotency_key),
    UNIQUE(workspace_id,source_journal_id),
    UNIQUE(workspace_id,target_journal_id)
);
CREATE INDEX transfers_recent ON transfer_operations(workspace_id,occurred_on DESC,created_at DESC,id DESC);

CREATE TABLE account_reconciliations (
    id TEXT PRIMARY KEY CHECK (length(id)=32),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL CHECK(length(idempotency_key)=32),
    fingerprint BYTEA NOT NULL CHECK(length(fingerprint)=32),
    account_id TEXT NOT NULL,
    currency TEXT NOT NULL CHECK (currency IN ('AED','PHP')),
    previous_minor BIGINT NOT NULL,
    observed_minor BIGINT NOT NULL CHECK(observed_minor BETWEEN 0 AND 99999999999999),
    delta_minor BIGINT NOT NULL,
    note TEXT NOT NULL DEFAULT '' CHECK(char_length(note)<=500),
    occurred_on DATE NOT NULL,
    journal_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK(observed_minor::numeric-previous_minor::numeric=delta_minor::numeric),
    CHECK((delta_minor=0)=(journal_id IS NULL)),
    FOREIGN KEY (workspace_id,account_id,currency)
      REFERENCES accounts(workspace_id,id,currency) ON DELETE RESTRICT,
    FOREIGN KEY (workspace_id,journal_id,currency)
      REFERENCES ledger_journals(workspace_id,id,currency) ON DELETE RESTRICT,
    UNIQUE(workspace_id,idempotency_key),
    UNIQUE(workspace_id,journal_id)
);
CREATE INDEX reconciliations_recent ON account_reconciliations(workspace_id,occurred_on DESC,created_at DESC,id DESC);
CREATE TRIGGER transfers_append_only BEFORE UPDATE OR DELETE
    ON transfer_operations FOR EACH ROW EXECUTE FUNCTION ledger_immutable();
CREATE TRIGGER reconciliations_append_only BEFORE UPDATE OR DELETE
    ON account_reconciliations FOR EACH ROW EXECUTE FUNCTION ledger_immutable();

UPDATE app_metadata SET value='group9',updated_at=now() WHERE key='schema_marker';

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM transfer_operations)
       OR EXISTS (SELECT 1 FROM account_reconciliations)
       OR EXISTS (SELECT 1 FROM ledger_accounts WHERE system_key IN
                 ('transfer_fee','fx_clearing','reconciliation_equity')) THEN
       RAISE EXCEPTION 'Group 9 has financial records and cannot be rolled back';
    END IF;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER reconciliations_append_only ON account_reconciliations;
DROP TRIGGER transfers_append_only ON transfer_operations;
DROP TABLE account_reconciliations;
DROP TABLE transfer_operations;
ALTER TABLE ledger_accounts DROP CONSTRAINT ledger_accounts_check1;
ALTER TABLE ledger_accounts ADD CONSTRAINT ledger_accounts_check1 CHECK (
    system_key IS NULL
    OR (system_key='opening_equity' AND kind='equity')
    OR (left(system_key,9)='category:' AND kind IN ('income','expense'))
);
UPDATE app_metadata SET value='group8',updated_at=now() WHERE key='schema_marker';
