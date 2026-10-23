# Group 7 — Double-entry ledger foundation

## Accounting rules

The ledger is the source of truth for account balances. All journal amounts are **integer minor units** (fils / centavos), with no floating-point arithmetic. Every journal belongs to exactly one workspace and one currency (AED or PHP). Foreign keys prevent a line from referencing an account of another workspace or currency.

Ledger accounts have **asset**, **liability**, or **equity** normal sides:

| Class | Debit | Credit | Normal balance |
| --- | --- | --- | --- |
| Asset | Increases | Decreases | Debits minus credits |
| Liability | Decreases | Increases | Credits minus debits |
| Equity | Decreases | Increases | Credits minus debits |

Balances can be negative after adjustments; that does **not** turn a liability into an expense or create a conversion between currencies. Do not add AED and PHP values together.

## Posting protocol and database guarantees

- `ledger_journals` records workspace, currency, kind, opaque ID, idempotency key, fingerprint, optional reversed journal and a one-way sealed flag.
- `ledger_lines` contains positive debit or credit amounts for linked ledger accounts. One or the other must be nonzero. No floating point or mutable balance column.
- Journal creation, all lines and sealing occur within **one PostgreSQL transaction**. A deferred constraint trigger rejects empty or unbalanced journals or unsealed journals at COMMIT. Foreign keys bind every line to the same workspace and currency as the journal and account.
- Database triggers block updates and deletes of journal history and lines. Additional lines can only be inserted before sealing, with a parent-row lock; post-seal inserts are rejected, including raw SQL.
- `(workspace_id,idempotency_key)` is unique. The SHA-256 fingerprint includes canonicalized account IDs, exact debit/credit amounts, journal kind, currency and reversal target. Retrying exactly the same request returns the original journal ID, including under concurrent submissions. Reusing the key with different monetary intent returns `ErrConflict`.
- `Reverse(ctx,workspace,original,key)` writes a **new** inverse journal. The original remains unchanged; `UNIQUE (workspace_id,reverses_journal_id)` prevents reversing the same journal twice, even under racing workers. Reversal of a reversal is deliberately rejected.
- There are no implicit writes on read endpoints. Expenses, transfers, refunds and budget operations will use this internal service in subsequent groups, with domain-specific validation.

## Group 6 opening balance conversion

- New accounts: account row, opening snapshot, ledger account and opening journal are created **atomically** inside the original account-creation transaction.
- Existing Group 6 accounts: server startup invokes `ledger.SyncOpenings` with a bounded context before HTTP serving. It locks each opening snapshot and safely imports it once. The import marker prevents repeats and supports competing application instances.
- Asset opening: debit asset and credit opening equity in the **same currency**.
- Liability opening: debit opening equity and credit liability in the **same currency**.
- Zero opening: create a ledger account and persistent import marker, but no meaningless zero-amount journal.
- Opening records remain immutable. Imports are not counted as income or expenses. The opening-equity counter-account is per workspace and per currency.

## API and UI

- `GET /api/v1/ledger/balances` returns only the authenticated workspace's signed, currency-specific balances. The response uses integer minor units.
- `GET /api/v1/ledger/journals` returns at most the most recent 50 immutable journals with their postings, in the authenticated workspace.
- `/accounts` displays both opening snapshots and **ledger-derived balances**. Neither one is a combined multi-currency total.

**No public generic posting endpoint is exposed in Group 7.** Domain-specific authenticated mutations (expenses, transfer, debt, etc.) will use the internal transaction service, with CSRF and proper authorization in later groups. This avoids allowing arbitrary balance manipulation through a generic browser API.

## Migration and tests

Run migration `00004_ledger.sql` via Goose explicitly. Server startup does **not** run database migrations. Group 7 readiness requires schema marker `group7`; if old accounts cannot be reconciled, the server fails startup rather than serving inconsistent balances.

The isolated PostgreSQL suite verifies: asset and debt openings; zero, AED and PHP postings; balanced journals; concurrency/idempotency; reversals; cross-workspace/currency rejection; database-enforced sealing and immutability; legacy opening backfill; and auth-scoped GET routes. CI also checks Go 1.27.3 on Linux/Windows, Node 24, sqlc generation, Docker and migration down/up behavior.

## Limitations

- Ledger journal access is internal for writes; account setup posts opening entries, while user-driven transactions begin Group 8.
- No FX exchange rate, currency conversion, reconciled bank feed, accounting period lock or tax reporting.
- The database records no transaction income/category until the later financial modules.
- PostgreSQL administrators can change schema and disable triggers; production permissions should restrict the application to minimum grants, and operational migration privileges must be separate.
