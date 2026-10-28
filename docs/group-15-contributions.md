# Group 15 — Philippine government contribution and MP2 tracking

## Scope and classification

The tracker supports four PHP-only schemes, with **user-entered**, not automatically determined, required amounts:

| Scheme | Financial classification of a linked payment |
|---|---|
| SSS | Existing PHP expense transaction, exact category split and amount |
| PhilHealth | Existing PHP expense transaction, exact category split and amount |
| Pag-IBIG regular | Existing PHP expense transaction, exact category split and amount |
| Pag-IBIG MP2 | Existing PHP same-currency asset transfer into a savings account, **not an expense** |

A contribution obligation is **planning metadata only**. Creating one does not create an expense, savings deposit, liability accrual, or ledger journal. A due date is optional and supplied by the user; the app does not calculate statutory contribution rates, mandatory due dates, eligibility, or penalties. Verify all amounts and payment deadlines with the relevant government portals.

Every obligation represents one scheme and a **YYYY-MM contribution period** in the workspace, with exactly one record per scheme per month. Values use PHP minor units (centavos). Records include an optional due date, a category for regular contributions or a savings destination for MP2, a note, and an idempotency fingerprint. Contribution periods and required amounts are immutable: create an accurate period before linking money. The application intentionally does **not** record SSS/PhilHealth/Pag-IBIG membership identifiers, which could be sensitive personal information.

## Status and accounting integration

For each obligation, the UI computes status from the due date and immutable resolution history:

- `pending` — no due date given and no resolution;
- `upcoming` — due date later than today;
- `due` — due today;
- `overdue` — due date is earlier than today;
- `paid` — linked to one existing eligible financial record;
- `skipped` — user explicitly skips the period, with no financial side effect.

All period comparisons use UTC dates, not local government filing calendars. Overdue here only means past the **user-entered due date**, not an official legal status.

Marking paid does **not** create a ledger transaction or initiate an external payment. It must reference a pre-existing Group 8 PHP expense for SSS, PhilHealth, or Pag-IBIG regular, or a pre-existing Group 9 PHP→PHP transfer to a savings account for MP2. Each linked record must match the scheme-specific category/savings destination and exact amount. Group 8's original expense is already counted once in the ledger and the monthly budget actuals. MP2 is a balance-sheet transfer and is not counted as spending. No Group 15 settlement code posts another journal or balance update.

A paid or skipped period is final; the resolution is append-only. Each referenced financial transaction/transfer can be used for only one contribution obligation. An expense used for a recurring bill cannot also be claimed by a government contribution (database-side guards in both directions). The amount belongs to the chosen contribution period even when the underlying payment was recorded on a different date.

## Schema

Goose migration `00012_contributions.sql` adds:

- `contribution_obligations`: workspace-owned, PHP-only, immutable records for scheme, first-of-month period, optional due date, exact amount, PHP expense category or PHP MP2 savings account, note, fingerprint and idempotency key. PostgreSQL CHECK constraints prevent assigning an expense category to MP2 or a savings destination to a regular contribution.
- `contribution_resolutions`: workspace-owned immutable paid/skipped history, exact links to finance transactions or Group 9 transfers, status, resolution date, note and fingerprint. Composite workspace/currency foreign keys ensure PHP-only money classification and workspace isolation.
- A database trigger validates active PHP expense categories or PHP savings destinations when creating an obligation. Another trigger verifies a paid regular contribution's exact PHP expense category split, or an MP2 transfer's PHP currency, same amount, zero fee and correct savings destination. A reciprocal guard prevents a linked expense being double-classified through the recurring bills tracker.
- Both tables have append-only triggers. Downgrades are intentionally refused once there are obligations or resolution records.

## Browser and security

Routes: `GET /contributions`, `POST /contributions`, `POST /contributions/resolve`. Forms are server-rendered with templ and usable without JavaScript, with HTMX error feedback and `HX-Redirect` on success. The authenticated session determines the workspace; form POSTs require a matching Origin, `application/x-www-form-urlencoded`, and session-bound CSRF.

Workspace-scoped PostgreSQL transactions and advisory locks serialize identical idempotency keys; resolving a period locks the obligation row, preventing simultaneous paid/skipped outcomes. Unique constraints prevent duplicate periods, linked expense reuse, transfer reuse, or more than one resolution. Identical retries return the existing record ID, while changed payloads with the same request key return a conflict.

The selection list exposes the most recent 25 PHP expenses and 50 internal transfers from existing Group 8/9 queries. Older matching records remain valid at the service/database layer but are not selectable in this initial UI. History and obligations are bounded to 100 most recent records each.

## Verification and limitations

Go tests cover exact money, category/destination input validation, month and date parsing, status derivation, request fingerprinting, and incorrect linked record types. PostgreSQL 17 integration tests cover expense linking, exactly-once concurrent payments, MP2 savings transfers without double counting, wrong category/destination/currency, cross-workspace denial, duplicate period, immutable records, skip history, reciprocal recurring-expense protections, Origin/CSRF, and native/HTMX flows.

CI checks Go 1.27.3 Linux/Windows, Linux race tests, PostgreSQL 17 Goose up/down/up on an *empty* database, sqlc 1.31.1, Node.js 24, and Docker/Compose.

**Not implemented:** statutory rate lookup, employer share, contribution bracket calculations, actual government remittance API, official receipt verification, arrears adjustments, refunds of paid contributions, multiple beneficiaries per workspace, MP2 investment earnings, and automatic reminders. This is a personal payment/status tracker, not government filing software.
