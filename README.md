# Golang Budget Tracker

A Go-first personal finance tracker planned around **Go 1.27.3 + templ + HTMX + Tailwind CSS + PostgreSQL**. The 20-group roadmap is described in [docs/roadmap.md](docs/roadmap.md).

**Current delivery: Group 1 repository bootstrap only.** The API, ledger, login, budget manager, database integration, and production UI are not yet implemented. The initial HTTP server uses the Go standard library; Chi will be integrated in Group 2, PostgreSQL/sqlc/Goose in Group 3, and templ/HTMX/Tailwind in Group 5.

## Prerequisites

- [Go 1.27.3](https://go.dev/dl/)
- Node.js 24 + npm (build/test tooling only)
- Docker with Compose (for local PostgreSQL)
- Optional: GNU Make

## Run locally

1. Clone the repository.
2. Copy `.env.example` to `.env` (PowerShell: `Copy-Item .env.example .env`).
3. Replace development-only PostgreSQL credentials before using any shared environment.
4. Start PostgreSQL with `docker compose up -d db`.
5. Start the HTTP skeleton with `go run ./cmd/server`.
6. Open `http://localhost:8080/` or `http://localhost:8080/healthz`.

The Group 1 Go server reads `APP_ENV` and `HTTP_ADDR` from process environment (not directly from the `.env` file). Their defaults are `development` and `:8080`. Docker Compose loads `.env` itself. The server does not connect to PostgreSQL until Group 3.

## Verify

```sh
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/server
npm ci --ignore-scripts
npm run check:node
npm test
npm run assets:build
docker compose config --quiet
docker build --tag golang-budget-tracker:local .
```

There are no external Go or npm dependencies in Group 1, so `go.sum` is not required. Node's lockfile is committed. The `assets:build` script copies an initial CSS placeholder; Tailwind tooling and templ code generation are introduced in Group 5.

## Structure

- `cmd/server`: Go entrypoint.
- `internal/app` and `internal/config`: bootstrap HTTP and environment validation.
- `internal/modules`: future financial domains.
- `internal/platform`: future persistence/security adapters.
- `internal/views`: future templ components.
- `db/migrations` and `db/queries`: future Goose/sqlc definitions.
- `web/styles`, `web/assets`, `scripts`: Node 24 asset tooling foundation.
- `tests`: future PostgreSQL integration and Playwright browser tests.

## Continuous integration

GitHub Actions validates Go on pinned Ubuntu and Windows runners, tests Node 24 scripts and validates the Docker build and Compose configuration. Official action versions use Node 24 internally. CI has read-only repository permissions and **does not publish or deploy automatically**. Manual production deployment is a future group.
