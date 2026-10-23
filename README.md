# Golang Budget Tracker

Go-first personal finance application following a [20-group roadmap](docs/roadmap.md): **Go 1.27.3 + Chi + templ + HTMX + Tailwind CSS + PostgreSQL**.

**Current delivery: Group 2 — HTTP infrastructure.** Chi routing, structured logging, configuration validation, request IDs, response errors, limits and graceful shutdown are implemented and tested. The ledger, database, authentication and templ/HTMX UI are **not** yet implemented.

## Prerequisites

- Go **1.27.3**
- Node.js **24** + npm (asset/test tooling only)
- Docker + Compose (optional until database connectivity in Group 3)
- GNU Make (optional)

## Quick start

1. Clone the repository.
2. Optionally copy `.env.example` to `.env` for local Compose credentials; PowerShell: `Copy-Item .env.example .env`.
3. Run `go run ./cmd/server` (listens at `:8080` by default).
4. Open `http://localhost:8080/` or `http://localhost:8080/healthz`.

The Go program reads **process environment**, not the `.env` file. Example: `HTTP_ADDR=127.0.0.1:8081 go run ./cmd/server` on Linux; PowerShell: `$env:HTTP_ADDR='127.0.0.1:8081'; go run ./cmd/server`.

Endpoints:

| Path | Purpose |
| --- | --- |
| `GET /` | bootstrap HTML page |
| `GET /healthz` | liveness check |
| `GET /readyz` | HTTP process readiness (not database readiness yet) |
| `GET /api/v1/status` | versioned status payload |

Routes return `X-Request-ID` response headers. Errors use JSON with `error.code`, `error.message` and `error.request_id`. See [Group 2 HTTP notes](docs/group-02-http.md) for security/timeout limitations and environment settings.

## Local verification

```sh
go mod verify
go mod tidy -diff
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/server
npm ci --ignore-scripts
npm run check:node
npm test
npm run assets:build
docker compose config --quiet
docker build -t golang-budget-tracker:local .
```

Run `docker compose up -d db` after copying `.env.example` to `.env` if you want a local PostgreSQL container; the Go app does **not connect** to it yet. Group 3 introduces pgx, sqlc and Goose.

## Architecture

- `cmd/server/`: entrypoint, signal-aware HTTP server lifecycle
- `internal/app/`: Chi router, middleware and health routes
- `internal/config/`: process config and validation
- `internal/httpx/`: structured HTTP error envelopes
- `internal/modules/`: future financial domains
- `internal/platform/`: future infrastructure adapters
- `internal/views/`: templ UI coming in Group 5
- `db/`: future SQL migrations and generated queries
- `web/`: Node 24 asset tooling and upcoming frontend

CI checks Go on Ubuntu and Windows, Node 24, and Docker/Compose. **No PR, automatic release, image publishing or deployment** is configured.
