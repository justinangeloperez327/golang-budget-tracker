# Group 3 — PostgreSQL foundation

Versions: Go 1.27.3, PostgreSQL 17, pgx v5.11.0, Goose v3.29.0 and sqlc v1.31.1.

## Boundaries

- `internal/config/database.go`: URL, pool and timeout validation.
- `internal/platform/database/store.go`: pgxpool startup, ping, readiness, cleanup and transactional callback.
- `db/migrations/00001_app_metadata.sql`: Goose schema marker. No personal or financial data.
- `db/queries/app_metadata.sql` + `sqlc.yaml`: SQL-first generated access for app metadata, with pgx/v5.
- `internal/platform/database/sqlc/`: committed generated Go query package, which may be regenerated.
- `tests/integration/`: guarded test database, query verification, rollback and commit tests.

### Configuration

`DATABASE_URL` is mandatory and never logged. Production configuration refuses `sslmode=disable`, `allow`, or `prefer`. Use `sslmode=verify-full` whenever supported by the database provider. Pool defaults: maximum 8 connections, minimum 0, connect timeout 5s, readiness timeout 2s, max lifetime 30m, max idle 5m.

Use a separate migration role and runtime role for production. Credential rotation, RLS, grants, and per-workspace data isolation begin in the authentication and ledger groups.

### Readiness

`GET /healthz` indicates process liveness. `GET /readyz` returns 200 only if PostgreSQL responds and the `app_metadata` schema marker is found. A failed check returns a sanitized 503 without leaking database credentials. The server connects at startup but **does not run migrations**.

### Isolation and regression tests

CI creates ephemeral `budget_tracker_test` on PostgreSQL 17, runs Goose migrations, verifies sqlc queries, tests rollback and commit, and validates down/up migration cycles. The integration suite refuses non-loopback hosts and database names other than `budget_tracker_test`.

Run tests locally using a different database than your development or production data. Never run `goose down` against databases containing financial records.

### Non-goals

No application users, financial data, bank connectivity, live account balances, or UI persistence. Those are planned in Groups 4–17.
