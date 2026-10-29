#!/usr/bin/env bash
set -euo pipefail
# Offline operator-run database backup. Never print database credentials.
: "${DATABASE_URL:?Export DATABASE_URL before backup}"
: "${BACKUP_DIR:?Export BACKUP_DIR (private directory) before backup}"
umask 077
command -v pg_dump >/dev/null || { echo "pg_dump is required" >&2; exit 1; }
command -v pg_restore >/dev/null || { echo "pg_restore is required" >&2; exit 1; }
mkdir -p -- "$BACKUP_DIR"
if [[ -L "$BACKUP_DIR" ]]; then echo "Refusing symlink backup directory" >&2; exit 1; fi
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
destination="$BACKUP_DIR/budget-tracker-$stamp-$PPID.dump"
if [[ -e "$destination" ]]; then echo "Backup exists; refusing overwrite" >&2; exit 1; fi
tmp="$(mktemp "$BACKUP_DIR/.backup.XXXXXXXX")"
trap 'rm -f -- "$tmp"' EXIT
chmod 600 "$tmp"
pg_dump --dbname="$DATABASE_URL" --format=custom --no-owner --no-acl --file="$tmp"
pg_restore --list "$tmp" >/dev/null
mv -- "$tmp" "$destination"
sha256sum "$destination" > "$destination.sha256"
chmod 600 "$destination.sha256"
trap - EXIT
echo "Backup created: $destination"
echo "Verify: (cd \"$BACKUP_DIR\" && sha256sum -c \"$(basename "$destination").sha256\")"
