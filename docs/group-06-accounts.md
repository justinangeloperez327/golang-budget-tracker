# Group 6 — Accounts and wallets

## Scope and purpose

This stage introduces **workspace-owned account setup**, not a transaction ledger. The supported currencies are AED and PHP, each represented in integer minor units (100 fils or centavos per major unit). No exchange-rate conversion is performed and AED/PHP values are never summed together.

Types:

| Type | Nature | What opening amount means |
| --- | --- | --- |
| cash | Asset | Cash held |
| bank | Asset | Bank balance held |
| savings | Asset | Savings held |
| ewallet | Asset | E-wallet funds held |
| credit | Liability | Credit-card amount owed |
| loan | Liability | Principal debt owed |

An opening amount is **always nonnegative**. It is a historical starting snapshot, not today's current balance, a deposit, income, or expense. The **ledger (Group 7)** must introduce balanced opening equity/liability journal entries from the immutable snapshots exactly once, and must never count openings again as income or spending. Credit and loan snapshots are outstanding liabilities, not negative cash balances.

## Data model and invariants

Goose migration `00003_accounts.sql` creates:

- `accounts`: generated 128-bit ID, owning workspace, name, kind, currency, unique workspace setup key, timestamp and optional archival timestamp. No independently writable `balance` column.
- `account_openings`: one initial snapshot per account, with workspace and currency bound by a composite foreign key; `BIGINT amount_minor` between 0 and 99,999,999,999,999. A database trigger rejects UPDATE and DELETE.
- Composite FK and workspace predicates prevent openings from silently crossing accounts, workspaces or currencies.
- Creation inserts the account and its opening record in **one PostgreSQL transaction**. A unique form setup key prevents duplicate account creation on resubmission.
- Archive is a workspace-scoped soft deletion. The opening snapshot remains intact and archived accounts remain visible.

No ledger postings, transfers, transactions, dynamic balances, reconciliation or account editing exists yet. Do **not** mutate opening records later; post reconciliations/adjustments through the Group 7 ledger.

## Input and display

The opening parser accepts plain ASCII base-10 amounts such as `0`, `23`, `12.3`, `1.25` and `999999999999.99`. It rejects negative values, currency symbols, thousands separators, exponents, more than two decimals, overflow and nonnumeric strings. Parsing uses `int64`, never float64.

Each list card explicitly labels the amount as **Opening amount held** or **Opening amount owed**. Amounts display separately with their original AED/PHP currency identifiers. No combined multicurrency total appears on the page.

## Browser endpoints

All account pages and mutations require an authenticated session.

- `GET /accounts`: accounts page and opening setup form
- `GET /accounts/rows`: scoped, server-rendered templ/HTMX list fragment
- `POST /accounts`: create account and opening atomically
- `POST /accounts/{id}/archive`: soft-archive, retaining opening snapshot

Form writes require a matching `Origin` and the session-bound hidden `csrf_token`. Invalid forms receive accessible inline feedback (HTML response or HTMX fragment); successful native posts redirect with 303 and successful HTMX posts use `HX-Redirect`. The client never supplies a workspace ID. Unsupported types or currencies fail validation.

## Tests and deployment

Isolated PostgreSQL integration tests check registration and session access, CSRF rejection, workspace isolation, duplicate setup keys, two distinct currencies, archive ownership, real SQL persistence, HTML escaping, HTMX fragments and immutability enforcement. Go unit tests cover money parsing and metadata validation.

After updating, run Goose `up` explicitly using a migration-capable user. Automatic schema migrations and deployments remain disabled. `/readyz` now requires migration marker `group6`.

## Known limitations

- Historical snapshots are not spendable balance calculations; the balance-of-record starts with Group 7.
- Once an account is created, name/type/currency/opening amount cannot be edited in Group 6. Archive and create a new account if setup was incorrect before transacting; migration into the ledger must preserve history.
- No multi-user workspace collaboration; Group 4's one-workspace-per-owner rule applies.
- Ingress-aware rate limits, account deletion policies, completed financial dashboards and end-to-end browser automation will be developed/reviewed in later groups.
