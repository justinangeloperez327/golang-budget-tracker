# Golang Budget Tracker

A Go-first personal finance tracker built incrementally using **Go 1.27.3**, Chi, **templ + HTMX**, Tailwind and PostgreSQL. The [finalized 20-group roadmap](docs/roadmap.md) defines the feature sequence.

**Current scope: Group 3 — PostgreSQL infrastructure.** The app now uses a pgx connection pool, explicit Goose migrations, sqlc-generated queries, transaction boundaries and database-backed readiness. No user accounts, budgets, financial ledger or templ/HTMX pages exist yet.

## Requirements

- Go 1.27.3
- Docker and Compose (PostgreSQL 17)
- Node.js 24 (asset tooling only)
- GNU Make optional (PowerShell alternatives below)

## Local setup

1. Clone the repository and copy `.env.example` to `.env` (PowerShell: `Copy-Item .env.example .env`).
2. Start PostgreSQL: `docker compose up -d db`.
3. Export `DATABASE_URL` as a **process environment variable**, matching your Docker Compose password. On Linux/macOS: `export DATABASE_URL='postgres://budget_tracker_dev:local_dev_only_change_me@127.0.0.1:5432/budget_tracker?sslmode=disable'`. On PowerShell: `$env:DATABASE_URL='postgres://budget_tracker_dev:local_dev_only_change_me@127.0.0.1:5432/budget_tracker?sslmode=disable'`.
4. Apply migrations manually: `go run github.com/pressly/goose/v3/cmd/goose@v3.29.0 -dir db/migrations postgres "$DATABASE_URL" up`. PowerShell: use `$env:DATABASE_URL` rather than `$DATABASE_URL`.
5. Start the application: `go run ./cmd/server`.
6. Check `http://localhost:8080/healthz` and `http://localhost:8080/readyz`.

The Go process **does not load .env automatically**. Compose reads `.env`; application settings must be provided to the Go process separately. The server now requires `DATABASE_URL` and fails to start when PostgreSQL is inaccessible. `/healthz` checks the HTTP process, while `/readyz` checks connectivity and a migrated schema marker; it returns HTTP 503 before migration or during database failure.

The application does not auto-migrate or create application tables at startup. For production, use a protected migration workflow with a migration-capable account, and a separate least-privileged application database user.

## Tooling / verification

```sh
go mod verify
go mod tidy -diff
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/server
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate
npm ci --ignore-scripts
npm run check:node
npm test
npm run assets:build
docker compose config --quiet
docker build -t golang-budget-tracker:local .
```

The SQLC configuration is in `sqlc.yaml` and the generated package is in `internal/platform/database/sqlc/`. Database integration tests require an **isolated local** database named `budget_tracker_test` and `go test -tags=integration ./tests/integration`; CI provisions a dedicated PostgreSQL service and runs migrations before tests.

## Documentation

- [Architecture](docs/architecture.md)
- [Group 2 HTTP infrastructure](docs/group-02-http.md)
- [Group 3 database infrastructure](docs/group-03-database.md)
- [20-group roadmap](docs/roadmap.md)

GitHub Actions runs checks on Linux and Windows, Node 24 and PostgreSQL. No PR, package publication, automatic deployment, or production database changes are configured.
