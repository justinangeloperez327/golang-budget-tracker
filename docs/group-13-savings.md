# Group 13 — Savings goals and ledger-backed progress

## Accounting model

Every savings goal earmarks **one existing savings account**, and the account can be allocated to only one goal (including archived goals). This avoids confusing overlapping claims on the same money. The live balance of that account is the goal's authoritative saved amount, **including its historical opening balance**. Creating a goal does not post money or create a second savings balance.

A goal has a workspace-owned name, AED/PHP currency inherited from its savings account, strictly positive target amount and optional target date. Target dates and monetary amounts are immutable after creation. A goal may be archived in one direction only; previously allocated savings accounts are not automatically released for reuse.

**Contribute:** debit the goal savings asset and credit the chosen bank/cash/savings/e-wallet asset.

**Withdraw:** debit the selected receiving asset and credit the goal savings asset.

Both directions record **one balanced, immutable, single-currency journal** and a workspace-owned `savings_goal_movements` event within one PostgreSQL transaction. There is no `finance_transaction`, no income, no expense, no fee, and no FX conversion. Currency mismatches, liabilities, archived accounts and the same account on both sides are rejected. The funding account for a goal movement cannot be the savings account allocated to a different goal, even when that other goal is archived. Use an unallocated bank account as an intermediary when reallocating between goals.

Account row locks acquired in stable ID order serialize concurrent goal transfers (and Group 9 transfers, which lock the same accounts). Transfers prevent the source's balance from going negative and the destination balance from exceeding the supported maximum. The DB independently verifies each movement journal's exact asset postings and workspace/currency identities; all movement history and journals are append-only. Group 13 does not attempt to prohibit separate authenticated transactions/standard transfers from modifying a goal's dedicated savings account, so the **true account balance**, rather than only the Group 13 event sum, is used for progress. External movements appear in the general ledger but not in goal-specific contribution history.

## Calculation

```
saved_minor = SUM(goal_account_debits - goal_account_credits)
remaining_minor = target_minor - saved_minor
progress_bps = clamp(floor(saved_minor * 10000 / target_minor),0,10000)
```

Calculations use exact integer minor units and arbitrary-precision intermediate arithmetic. The visual progress bar is capped at 100%, but actual savings may exceed the target and the remaining amount can be negative. The interface shows *in progress*, *achieved*, *past target* or *archived*. No mixed AED/PHP totals are calculated.

## Schema and rollback safety

Goose migration `00010_savings_goals.sql` introduces:

- `savings_goals`: immutable goal specifications, unique per workspace and dedicated savings account, optional target date, one-way archival, canonical SHA-256 request fingerprint and idempotency key.
- `savings_goal_movements`: immutable goal contribution/withdrawal metadata and linked journal, account/currency scoped composite foreign keys, exact minor amount, occurred date, unique idempotency key and journal.
- A PostgreSQL trigger validates both movement journal legs, active asset accounts, and blocks goal-to-goal counterparty reuse even for a direct SQL writer. Immutable triggers reject updates/deletes. The down migration refuses to discard existing goal or movement data.

## Browser workflows

`GET /savings` is an authenticated templ page showing each goal's target, currency, real account balance, percentage, target date and status, plus contribution/withdrawal forms and a movement history. `POST /savings` creates a goal, `POST /savings/movements` records a transfer, and `POST /savings/{id}/archive` archives a goal. Native forms and HTMX are both supported, with error fragments and redirect-after-save.

All writes require authenticated sessions, an exact matching Origin, form content-type validation, and session-bound CSRF. The workspace is never taken from browser POST fields. Form inputs, names and history are rendered with templ escaping. Idempotency keys are scoped to the workspace and locked in PostgreSQL transactions: identical retries return the original ID, different payloads using the same key fail without side effects.

## Testing and boundaries

Go unit tests cover goal validation, exact money normalization, fingerprinting, progress (0%, partly funded, complete, overfunded and negative account balances) and dated statuses. PostgreSQL 17 tests cover AED/PHP isolation, opening balance progress, concurrency, replay, withdrawal, insufficient funds, allocated-account uniqueness, cross-workspace denial, archival, append-only tables, actual ledger balances and authenticated browser CSRF/HTMX flows. CI also verifies Go 1.27.3 Windows/Linux plus race tests, sqlc 1.31.1, Node 24, Docker and Goose down/up on an empty database.

Not yet included: automatic deposits, bank APIs, cross-currency goal allocations, interest accrual, investment balances, and notifications. Existing Group 9 transfers can increase or decrease a dedicated savings account independently, so the goal's progress always reflects the authoritative account balance, not just its recorded Group 13 movements.
