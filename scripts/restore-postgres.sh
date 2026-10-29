#!/usr/bin/env bash
set -euo pipefail
# Restore ONLY to a separately created, EMPTY database.
: "${RESTORE_DATABASE_URL:?Export RESTORE_DATABASE_URL for an empty target database}"
: "${BACKUP_FILE:?Export BACKUP_FILE pointing to a pg_dump custom-format archive}"
if [[ "${CONFIRM_EMPTY_RESTORE:-}" != "YES_RESTORE_TO_EMPTY_DATABASE" ]]; then
  echo "Explicitly set CONFIRM_EMPTY_RESTORE=YES_RESTORE_TO_EMPTY_DATABASE" >&2
  exit 1
fi
command -v psql >/dev/null || { echo "psql is required" >&2; exit 1; }
command -v pg_restore >/dev/null || { echo "pg_restore is required" >&2; exit 1; }
[[ -f "$BACKUP_FILE" && ! -L "$BACKUP_FILE" ]] || { echo "Missing or symlinked backup file" >&2; exit 1; }
pg_restore --list "$BACKUP_FILE" >/dev/null
# No existing user tables are allowed. This avoids silently merging live finance history.
existing="$(psql "$RESTORE_DATABASE_URL" -X -A -t -v ON_ERROR_STOP=1 -c "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('r','p','v','m','f')")"
if [[ "$existing" != "0" ]]; then
  echo "Target public schema is not empty: restore cancelled" >&2
  exit 1
fi
pg_restore --dbname="$RESTORE_DATABASE_URL" --no-owner --no-acl --exit-on-error --single-transaction "$BACKUP_FILE"
echo "Restore complete. Validate schema and application using a separate non-production endpoint."
