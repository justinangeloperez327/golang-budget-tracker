# Architecture decisions

## Group 1 boundaries

A Go-first modular monolith is the fixed direction for the next 20 implementation groups. The Group 1 bootstrap intentionally has no database connection, financial persistence, Chi, templ, HTMX, or Tailwind runtime. Their directories and roadmapped integration points are prepared, rather than claiming features have already been delivered.

- Go 1.27.3 serves HTTP and business logic in one process.
- Chi middleware and the production HTTP architecture arrive in Group 2.
- PostgreSQL, pgx, sqlc, and Goose arrive in Group 3.
- Server-side auth and session enforcement arrive in Group 4.
- templ and HTMX render the UI; Tailwind handles styling from Group 5.
- Node 24 builds frontend assets and runs browser testing; it never acts as an application server.
- Docker Compose provides local PostgreSQL; Dockerfile builds the standalone Go binary.
- Financial records are workspace-scoped and must not be exposed to other users.

## Ledger design principles (implemented in subsequent groups)

All money uses integer minor units or explicit currency-appropriate decimal representations (e.g., exchange rates with exact rounding rules). Every finalized financial transaction must have balanced postings per currency. Opening balances use explicit equity postings. Transfers to owned accounts, including savings, are not expenses. Credit-card payments settle liabilities, not expenses twice. Remittances distinguish expenses from own-account transfers and record foreign exchange and fees separately. Finalized ledger events are corrected through reversal/adjustment rather than silent mutation.

No independent writable account totals or budget totals may become conflicting financial sources of truth. All linked postings must be persisted in one database transaction. Retries require idempotency and concurrent edits require revision checks.

## Release policy

No CI release, deploy, Vercel, image push, or package publication. Future production release must be manually initiated after explicit security, database restore, financial-invariant and CI quality gates.
