# Golang Budget Tracker

Go-first personal finance tracker following the [20-group roadmap](docs/roadmap.md). Stack: **Go 1.27.3, Chi, templ, HTMX, Tailwind CSS 4, PostgreSQL 17**, Node.js 24 for asset tooling only.

**Current release candidate: Group 6 — Accounts and wallets foundation.** After registration, you can create AED/PHP cash, bank, savings, e-wallet, credit-card and loan accounts, record an immutable opening amount, view and archive accounts. Opening amounts are **not live ledger balances**. The ledger, transfers and financial reporting are delivered in later groups.

## Run locally

Requirements: Go 1.27.3, Node.js 24, Docker/Compose, optionally Make.

1. Clone the repository; copy `.env.example` to `.env` and run `docker compose up -d db`.
2. Export `DATABASE_URL` in the shell running Go. The server **does not** automatically parse the `.env` file.
3. Run Goose migrations explicitly:
   `go run github.com/pressly/goose/v3/cmd/goose@v3.29.0 -dir db/migrations postgres "$DATABASE_URL" up` (PowerShell: `"$env:DATABASE_URL"`).
4. Run `npm ci --ignore-scripts && npm run assets:build`.
5. Run `go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate`.
6. Run `go run ./cmd/server`; open http://localhost:8080/.

Local `DATABASE_URL` example:
`postgres://budget_tracker_dev:local_dev_only_change_me@127.0.0.1:5432/budget_tracker?sslmode=disable`

Never use development credentials in production. Set `PUBLIC_ORIGIN` to the exact browser-visible origin; production requires HTTPS and Secure cookies.

### Pages

- `/login` and `/register` — native HTML forms enhanced by HTMX
- `/dashboard` — secure dashboard shell and navigation (not yet a live financial dashboard)
- `/accounts` — account setup and wallet listing, with currency-specific opening amounts
- `/accounts/rows` — authenticated HTMX account list fragment
- `/healthz` and `/readyz` — process liveness and Group 6 database migration readiness

Native account form submission works without JavaScript; HTMX enhances feedback and redirects. Account data is scoped to the authenticated workspace. CSRF and same-origin validation apply to every state-changing browser form.

**Important:** Opening amounts use integer minor units with exactly up to two decimals. Positive balances for cash/banks/savings/e-wallets mean assets held; positive credit/loan opening amounts mean debt owed. The application does not currently execute ledger entries, track transactions, convert AED to PHP or compute a live combined balance.

## Verification

```sh
npm ci --ignore-scripts
npm run check:node
npm test
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

Integration CI uses PostgreSQL 17 with `go test -tags=integration ./tests/integration`. Do not run integration migrations against live financial data.

See [Group 6 account semantics](docs/group-06-accounts.md), [Group 5 UI foundation](docs/group-05-frontend.md), [authentication security](docs/group-04-auth.md) and the [full roadmap](docs/roadmap.md). CI commits never publish, deploy, or open pull requests automatically.
