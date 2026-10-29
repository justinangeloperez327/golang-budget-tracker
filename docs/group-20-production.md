# Group 20 — Production readiness and manual release

## Release model

**No automatic deploys or database migrations.** CI builds and tests; an authorized operator must approve each release, inspect environment configuration, apply migrations and start a pinned image. Use an immutable Git commit SHA or image digest for every release. Never deploy `main` with a mutable image tag as the rollback reference.

## Prerequisites and controls

- Go 1.27.3, Node.js 24 and pinned templ/sqlc versions are verified in CI on Linux/Windows. PostgreSQL 17 integration and Docker build must be green on the **exact release commit**.
- Provision PostgreSQL 17 privately with separate migration (DDL) and runtime roles. The runtime role needs only the table/sequence privileges required by the app. Grant no superuser, schema ownership, CREATE EXTENSION or broad public database access to the runtime user.
- Use a managed TLS endpoint (reverse proxy/load balancer) with valid HTTPS certificates. Set `APP_ENV=production`, `PUBLIC_ORIGIN=https://...` and production `DATABASE_URL` with `sslmode=verify-full`, valid CA trust, and hostname validation. The server enforces a secure session cookie in production.
- Bind the container's HTTP port to loopback or a private network behind the HTTPS proxy, **never expose port 8080 directly on the public internet**. Protect database traffic with a private network, firewall and access controls.
- Supply secrets via your platform secret mechanism; do not commit `.env` with credentials or print `DATABASE_URL` in CI logs. Set separate backup credentials and encrypted retention.
- Check disk quotas, CPU/memory limits, timeouts, structured logs, alerting, backups, uptime monitoring and recovery contacts.
- Configure appropriate retention and access controls for user financial data and exported reports.

## Manual build and preflight

1. Review the [GitHub Actions CI run](../.github/workflows/ci.yml) for the exact commit; require all jobs to pass. Review dependency, vulnerability and secret scans separately; CI success alone is **not** a security certification.
2. Build reproducibly from that commit: `docker build --pull -t budget-tracker:<commit-sha> .`. Check the resulting image, SBOM and dependency scan with your approved tools. Record the image digest.
3. From a secured environment with **migration-role** database access, run `go run github.com/pressly/goose/v3/cmd/goose@v3.29.0 -dir db/migrations postgres "$DATABASE_URL" status`. Take a verified backup first.
4. Run `goose ... up` manually during an approved maintenance window. Inspect schema version and any irreversible/down migration guards. The app does **not** run Goose on startup.
5. Switch to the separate **runtime-role** database URL, run the pinned container in a private network, and pass the production settings in [`.env.production.example`](../.env.production.example) through your secrets manager. The Dockerfile runs as non-root UID 10001.
6. Check `GET /healthz` and `GET /readyz` from inside the protected deployment path; readiness includes database/schema checks. Probe a real login and a read-only dashboard before admitting user traffic.
7. Perform authorized smoke checks for AED and PHP account separation, transaction idempotency, transfers, refunds, recurring bills, contributions, reports, CSV/PDF downloads, logout, wrong-workspace IDs, CSRF, and unauthorized requests. Confirm sensitive responses have no-store caching and proxy logs redact credentials.
8. Record release SHA, image digest, migration version, backup identifier, test run, operator approval and rollback decision in a release ledger.

## Backup and restore drill (operator initiated)

- `DATABASE_URL='[secret]' BACKUP_DIR=/private/backups bash scripts/backup-postgres.sh` creates a timestamped custom-format `pg_dump`, checks it with `pg_restore --list`, and writes a SHA-256 manifest. The backup directory must be private, access-controlled, encrypted at rest and excluded from source control.
- Copy the archive and checksum to encrypted off-host storage. Verify checksum after copying. Retain backups under a documented rotation policy; periodically test restore using the same PostgreSQL major version and compatible extensions.
- To rehearse **into a new empty, isolated database only**: `RESTORE_DATABASE_URL='[empty-test-db-secret]' BACKUP_FILE=/private/backups/file.dump CONFIRM_EMPTY_RESTORE=YES_RESTORE_TO_EMPTY_DATABASE bash scripts/restore-postgres.sh`. The script refuses to restore if the public schema already contains relations and uses a single transaction.
- Once restored, run read-only reconciliation: sample journal totals, balances, account counts, per-currency reports, and critical auth/workspace records. Do not expose restored real personal financial data outside a restricted test boundary. Record recovery time, RPO and RTO observations.
- Do not use the restore script to overwrite an active production database.

## Rollback and incident response

- If the container fails but the schema is compatible, stop admission of writes and redeploy the **previous pinned digest**; verify readiness and financial write paths before reopening traffic.
- If the release applied incompatible/irreversible schema changes, **do not blindly run goose down**. Assess the migration and backup; use a rehearsed, approved point-in-time database recovery plan with a maintenance window and validated ledger reconciliation. Inform affected operators of possible loss between backup and restore.
- If an idempotent financial POST times out, retry with the same request key and unchanged payload, after verifying history; never generate a new key for uncertain outcomes.
- For suspected credential compromise, revoke exposed credentials, rotate sessions/secrets, restrict access, preserve audit evidence and follow incident procedures.

## Boundaries

No deploy, cloud resource provisioning, real backup or restore was executed by this change. Build/test checks do not prove production TLS, firewall, backups, runtime database grants, provider uptime, accessibility conformance, or external penetration-test results. Browser end-to-end tests require an actual deployed test environment.
