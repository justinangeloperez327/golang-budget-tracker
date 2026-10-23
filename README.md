# Golang Budget Tracker

Go-first personal finance tracker following the [20-group roadmap](docs/roadmap.md). Stack: **Go 1.27.3, Chi, templ, HTMX, Tailwind CSS 4, PostgreSQL 17**, Node.js 24 for asset tooling only.

**Group 5 delivers the responsive web foundation:** registration/login pages, authenticated dashboard shell, self-hosted assets, navigation, theme toggle, reusable templ components, HTMX form feedback and dashboard fragments. Financial balances are not implemented yet; the cards correctly display **Not set up**.

## Run locally

Install Go 1.27.3, Node.js 24, Docker/Compose, and optionally Make.

1. Clone the repository, copy `.env.example` to `.env`, and run `docker compose up -d db`.
2. Export `DATABASE_URL` in the shell that starts Go. (The Go app does not parse `.env` automatically.)
3. Run Goose migrations:
   `go run github.com/pressly/goose/v3/cmd/goose@v3.29.0 -dir db/migrations postgres "$DATABASE_URL" up`
   (PowerShell: use `"$env:DATABASE_URL"`).
4. Install/build frontend assets: `npm ci --ignore-scripts && npm run assets:build`.
5. Generate templ renderers: `go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate`.
6. Start the Go app: `go run ./cmd/server`, then open http://localhost:8080/.

Example dev URL: `postgres://budget_tracker_dev:local_dev_only_change_me@127.0.0.1:5432/budget_tracker?sslmode=disable`. Set it in your shell; do not use this local-only credential in production. Set `PUBLIC_ORIGIN` to exactly the browser-visible origin (default http://localhost:8080). TLS/HTTPS is required in production.

## Frontend architecture

- `internal/views/pages.templ`: page shell, login/register forms, dashboard, reusable cards, feedback and partials.
- `internal/app/ui.go`: native form and HTMX HTTP endpoints, auth and authorization.
- `internal/assets/`: generated styles/scripts embedded into the Go binary; no CDN.
- `web/styles/base.css`: Tailwind 4 theme and component styling.
- `web/scripts/app.js`: progressive dark/light mode enhancement.

The existing versioned JSON auth API remains available. Login/register forms are native HTML POSTs with HTMX enhancement, and logout requires a session-bound CSRF token. See [Group 5 documentation](docs/group-05-frontend.md).

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

CI additionally runs PostgreSQL integration tests with an isolated database and validates sqlc code generation. **No pull request, automatic production deployment or package publication** is configured.
