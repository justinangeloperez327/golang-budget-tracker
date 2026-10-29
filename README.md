# Golang Budget Tracker

A modular Go personal finance tracker: **Go 1.27.3**, Chi, templ + HTMX + Tailwind CSS 4, PostgreSQL 17 and Node.js 24 for assets. See the [finalized 20-group roadmap](docs/roadmap.md).

**Group 17 — Financial reports.** Generate monthly and annual AED/PHP reports with refund-aware categories, operating movements, journal-posted account histories, budget variance and secure CSV/PDF downloads. Server-rendered templ and HTMX share the same workspace-isolated reporting service.\n\n**Group 16 — Financial dashboard.** A unified read-only AED/PHP overview provides ledger-backed assets, liabilities and net worth, refund-aware income/spending, monthly budgets, savings goals, debt balances, actionable bills, PHP contributions and recent activity. Select a month with native GET or HTMX; never combine currencies or double-count transfer principal.\n\n**Group 15 — Philippine contributions.** Track user-entered PHP-only SSS, PhilHealth, regular Pag-IBIG and MP2 monthly obligations, optional due dates, payment/skipped history and overdue status. Link existing PHP expenses for regular contributions or PHP savings transfers for MP2 without posting duplicate ledger entries.

## Production and manual release

See [Group 20 production readiness, backup/restore and rollback runbook](docs/group-20-production.md). Production deployments require explicit operator approval. No GitHub Actions deployment or automatic migration is configured. Use the committed [production environment template](.env.production.example) without committing secrets.

## Run locally

1. Install Go 1.27.3, Node 24, Docker/Compose. Clone the repo and copy `.env.example` to `.env`.
2. Start PostgreSQL with `docker compose up -d db`.
3. Export `DATABASE_URL` in the process environment (the Go server does **not** parse `.env` automatically). Local example: `postgres://budget_tracker_dev:local_dev_only_change_me@127.0.0.1:5432/budget_tracker?sslmode=disable`.
4. Apply migrations explicitly before starting the server:
   `go run github.com/pressly/goose/v3/cmd/goose@v3.29.0 -dir db/migrations postgres "$DATABASE_URL" up`. Windows PowerShell should use `"$env:DATABASE_URL"`.
5. Build assets: `npm ci --ignore-scripts && npm run assets:build`.
6. Generate templ renderers: `go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate`.
7. Start: `go run ./cmd/server` and visit http://localhost:8080/.

Production: require HTTPS, correct `PUBLIC_ORIGIN`, separate migration/runtime DB roles, secured backups and monitoring. The server checks migrations and synchronizes any historic Group 6 openings before serving HTTP; it does not apply migrations automatically.

## Screens and ledger observations

- `/register`, `/login`: secure account access, native forms plus HTMX
- `/dashboard`: authenticated dashboard shell
- `/accounts`: account setup, archived accounts, opening snapshots and ledger-backed balances
- `/transactions`: income, expense, split and refund forms, category creation, searchable history
- `/transfers`: own-account transfers, AED/PHP FX settlements, transfer fees, card repayment and reconciliation
- `/budgets`: monthly AED/PHP category allocations, refund-aware actuals, rollovers and overspending
- `/recurring`: bill/subscription schedules, due and overdue occurrences, payment and skip history
- `/debts`: existing loan plans, interest-bearing installments, payments and ledger-backed outstanding debt
- `/savings`: goal targets and dates, account-backed contributions/withdrawals and progress
- `/remittances`: AED→PHP remittance orders, quoted versus effective FX, pending/settled/cancelled status
- `/contributions`: SSS, PhilHealth, Pag-IBIG and MP2 contribution periods and payment history
- `/dashboard`: live AED/PHP financial overview, budget and spending summary, obligations and recent activity
- `/reports`: monthly or annual financial reports, account movement history and CSV/PDF downloads
- `GET /api/v1/ledger/balances`: authenticated, workspace-scoped balances in integer minor units
- `GET /api/v1/ledger/journals`: authenticated, workspace-scoped immutable journal history
- `/healthz` and `/readyz`: liveness and Group 7 database readiness

Opening entries do **not** count as income or expense. Refunds reverse classified expenses; they do not count as new income. Credit-card and loan opening balances are liabilities, not negative cash deposits. Each currency remains separate. The internal `ledger.Store` supports balanced adjustments, exactly-once posting and reversals; Group 8 transactions post alongside category allocations in one atomic operation. Internal transfers and reconciliation are implemented in Group 9. No generic public mutation endpoint is exposed.

See [Group 17 financial report semantics](docs/group-17-reports.md), [Group 16 dashboard semantics](docs/group-16-dashboard.md), [Group 15 Philippine contribution semantics](docs/group-15-contributions.md), [Group 14 remittance settlement semantics](docs/group-14-remittances.md), [Group 13 savings goal semantics](docs/group-13-savings.md), [Group 12 debt and installment semantics](docs/group-12-debts.md), [Group 11 recurring semantics](docs/group-11-recurring.md), [Group 10 monthly budget semantics](docs/group-10-budgets.md), [Group 9 transfers/reconciliation semantics](docs/group-09-transfers.md), [Group 8 transaction semantics](docs/group-08-transactions.md), [Group 7 accounting guarantees](docs/group-07-ledger.md), [Group 6 account semantics](docs/group-06-accounts.md), and [20-group roadmap](docs/roadmap.md).

## Verification

```sh
npm ci --ignore-scripts
npm run assets:build
go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate
go mod tidy -diff
go mod verify
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/server
docker compose config --quiet
docker build -t golang-budget-tracker:local .
```

The PostgreSQL 17 GitHub Actions job runs the isolated integration suite, including PHP-only contribution periods, SSS/PhilHealth/Pag-IBIG payment classification, MP2 savings tracking without double counting, workspace isolation, concurrent retries, CSRF and immutable history. CI never creates a pull request, publishes a release or deploys automatically.
