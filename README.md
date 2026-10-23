# Golang Budget Tracker

Go-first personal finance application using Go 1.27.3, Chi, PostgreSQL, and planned templ + HTMX/Tailwind frontend. See the [20-group roadmap](docs/roadmap.md).

**Current implementation: Group 4 — Identity and authorization.** Registration, login/logout, Argon2id, database sessions, CSRF and workspace ownership are implemented as a JSON API. The financial ledger and templ/HTMX UI are future groups.

## Local setup

1. Copy `.env.example` to `.env` and start PostgreSQL: `docker compose up -d db`.
2. Export `DATABASE_URL` in the shell running Go (the server does **not** load `.env` automatically). Example Bash: `export DATABASE_URL='postgres://budget_tracker_dev:local_dev_only_change_me@127.0.0.1:5432/budget_tracker?sslmode=disable'`. PowerShell: `$env:DATABASE_URL='postgres://budget_tracker_dev:local_dev_only_change_me@127.0.0.1:5432/budget_tracker?sslmode=disable'`.
3. Migrate: `go run github.com/pressly/goose/v3/cmd/goose@v3.29.0 -dir db/migrations postgres "$DATABASE_URL" up` (PowerShell uses `"$env:DATABASE_URL"`).
4. Run `go run ./cmd/server`.

`GET /healthz` checks liveness; `GET /readyz` checks PostgreSQL and migration readiness.

## Identity API

```sh
curl -i -X POST http://localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -H 'Origin: http://localhost:8080' \
  -d '{"email":"person@example.com","password":"long-unique-password"}'
```

Routes: `POST /api/v1/auth/register`, `POST /api/v1/auth/login`, `GET /api/v1/auth/me`, `GET /api/v1/auth/csrf`, `POST /api/v1/auth/logout`, and `GET /api/v1/workspaces/{workspaceID}`. A successful registration/login sets an HttpOnly session cookie and returns a CSRF token. Browser clients must supply a matching `Origin` for all POSTs; logout requires `X-CSRF-Token`. See [Group 4 security notes](docs/group-04-auth.md).

For production, configure `PUBLIC_ORIGIN=https://your-real-hostname` and TLS at a trusted reverse proxy. The session cookie becomes Secure and `__Host-` prefixed. The app intentionally does not trust forwarding headers.

## Quality gates

```sh
go mod tidy -diff
go mod verify
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/server
npm ci --ignore-scripts
npm test
docker compose config --quiet
```

GitHub Actions also runs isolated PostgreSQL 17/Goose integration tests, sqlc verification, Node 24 and Docker builds. Migrations are explicit, CI cannot deploy, and no PR is required.
