-- +goose Up
-- Group 14. Pending remittances are commitments only; actual accounting is
-- posted once, by the preexisting Group 9 transfer engine at settlement.
ALTER TABLE transfer_operations
 ADD CONSTRAINT transfer_operations_workspace_id_uq UNIQUE (workspace_id,id);

CREATE TABLE remittance_orders (
 id TEXT PRIMARY KEY CHECK(length(id)=32),
 workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
 idempotency_key TEXT NOT NULL CHECK(length(idempotency_key)=32),
 fingerprint BYTEA NOT NULL CHECK(octet_length(fingerprint)=32),
 source_account_id TEXT NOT NULL,
 destination_account_id TEXT NOT NULL,
 source_currency TEXT NOT NULL DEFAULT 'AED' CHECK(source_currency='AED'),
 destination_currency TEXT NOT NULL DEFAULT 'PHP' CHECK(destination_currency='PHP'),
 send_minor BIGINT NOT NULL CHECK(send_minor BETWEEN 1 AND 99999999999999),
 fee_minor BIGINT NOT NULL CHECK(fee_minor BETWEEN 0 AND 99999999999999),
 receive_minor BIGINT NOT NULL CHECK(receive_minor BETWEEN 1 AND 99999999999999),
 rate_units BIGINT NOT NULL CHECK(rate_units BETWEEN 1 AND 999999999999999),
 effective_rate_units BIGINT NOT NULL CHECK(effective_rate_units BETWEEN 1 AND 999999999999999),
 note TEXT NOT NULL DEFAULT '' CHECK(char_length(note)<=500),
 requested_on DATE NOT NULL CHECK(requested_on BETWEEN DATE '2000-01-01' AND DATE '2100-12-31'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(workspace_id,source_account_id,source_currency)
    REFERENCES accounts(workspace_id,id,currency) ON DELETE RESTRICT,
 FOREIGN KEY(workspace_id,destination_account_id,destination_currency)
    REFERENCES accounts(workspace_id,id,currency) ON DELETE RESTRICT,
 UNIQUE(workspace_id,id),
 UNIQUE(workspace_id,idempotency_key),
 CHECK(source_account_id<>destination_account_id),
 CHECK(send_minor::numeric+fee_minor::numeric<=99999999999999)
);
CREATE INDEX remittance_orders_recent ON remittance_orders(workspace_id,created_at DESC,id DESC);

CREATE TABLE remittance_resolutions (
 id TEXT PRIMARY KEY CHECK(length(id)=32),
 workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
 order_id TEXT NOT NULL,
 action TEXT NOT NULL CHECK(action IN ('settled','cancelled')),
 transfer_id TEXT,
 settled_on DATE NOT NULL CHECK(settled_on BETWEEN DATE '2000-01-01' AND DATE '2100-12-31'),
 idempotency_key TEXT NOT NULL CHECK(length(idempotency_key)=32),
 fingerprint BYTEA NOT NULL CHECK(octet_length(fingerprint)=32),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK((action='settled')=(transfer_id IS NOT NULL)),
 FOREIGN KEY(workspace_id,order_id) REFERENCES remittance_orders(workspace_id,id) ON DELETE RESTRICT,
 FOREIGN KEY(workspace_id,transfer_id) REFERENCES transfer_operations(workspace_id,id) ON DELETE RESTRICT,
 UNIQUE(workspace_id,order_id),
 UNIQUE(workspace_id,idempotency_key),
 UNIQUE(workspace_id,transfer_id)
);
CREATE INDEX remittance_resolutions_recent ON remittance_resolutions(workspace_id,created_at DESC,id DESC);

-- The metadata for settlement cannot claim an unrelated or incorrect transfer.
-- +goose StatementBegin
CREATE FUNCTION remittance_validate_resolution() RETURNS trigger AS $$
DECLARE
    o remittance_orders%ROWTYPE;
    matches BOOLEAN;
BEGIN
    SELECT * INTO o FROM remittance_orders WHERE id=NEW.order_id
        AND workspace_id=NEW.workspace_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'remittance not found' USING ERRCODE='23503';
    END IF;
    IF NEW.action='settled' THEN
        SELECT EXISTS (
            SELECT 1 FROM transfer_operations t
            WHERE t.id=NEW.transfer_id AND t.workspace_id=NEW.workspace_id
              AND t.source_account_id=o.source_account_id
              AND t.target_account_id=o.destination_account_id
              AND t.source_currency='AED' AND t.target_currency='PHP'
              AND t.send_minor=o.send_minor AND t.fee_minor=o.fee_minor
              AND t.receive_minor=o.receive_minor AND t.rate_units=o.rate_units
              AND t.occurred_on=NEW.settled_on
              AND t.idempotency_key=o.id
        ) INTO matches;
        IF NOT matches THEN
            RAISE EXCEPTION 'remittance settlement does not match original transfer'
                USING ERRCODE='23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER remittance_resolution_check BEFORE INSERT ON remittance_resolutions
 FOR EACH ROW EXECUTE FUNCTION remittance_validate_resolution();
CREATE TRIGGER remittance_orders_immutable BEFORE UPDATE OR DELETE ON remittance_orders
 FOR EACH ROW EXECUTE FUNCTION ledger_immutable();
CREATE TRIGGER remittance_resolutions_immutable BEFORE UPDATE OR DELETE ON remittance_resolutions
 FOR EACH ROW EXECUTE FUNCTION ledger_immutable();
UPDATE app_metadata SET value='group14',updated_at=now() WHERE key='schema_marker';

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM remittance_orders) OR EXISTS(SELECT 1 FROM remittance_resolutions) THEN
   RAISE EXCEPTION 'Group 14 contains remittance history; rollback is unsafe';
 END IF;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER remittance_resolutions_immutable ON remittance_resolutions;
DROP TRIGGER remittance_orders_immutable ON remittance_orders;
DROP TRIGGER remittance_resolution_check ON remittance_resolutions;
DROP FUNCTION remittance_validate_resolution();
DROP TABLE remittance_resolutions;
DROP TABLE remittance_orders;
ALTER TABLE transfer_operations DROP CONSTRAINT transfer_operations_workspace_id_uq;
UPDATE app_metadata SET value='group13',updated_at=now() WHERE key='schema_marker';
