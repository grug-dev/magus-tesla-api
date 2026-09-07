#!/bin/sh
# Daily backup of the compose-local Postgres database.
#
# Runs pg_dump inside the running "db" container, gzips the output to a dated
# file under backups/, then deletes any backup older than 7 days. Invoked by
# `make backup-db` and by a daily cron line on the VPS — see
# docs/0-set-up/deployment.md §8 and docs/1-deploy/docker.md §8.
#
# set -eu: stop on the first failing command, and fail fast on any unset
# variable (POSTGRES_USER / POSTGRES_DB must come from .env).
set -eu

if [ -z "${POSTGRES_USER:-}" ] || [ -z "${POSTGRES_DB:-}" ]; then
	echo "ERROR: POSTGRES_USER and POSTGRES_DB must be set (source .env first)." >&2
	exit 1
fi

BACKUP_DIR="$(dirname "$0")/../backups"
mkdir -p "$BACKUP_DIR"

STAMP="$(date +%Y-%m-%d)"
OUT_FILE="$BACKUP_DIR/magus-$STAMP.sql.gz"

echo "Backing up database '$POSTGRES_DB' to $OUT_FILE ..."
docker compose exec -T db pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB" | gzip > "$OUT_FILE"
echo "Backup written: $OUT_FILE"

echo "Deleting backups older than 7 days ..."
find "$BACKUP_DIR" -name 'magus-*.sql.gz' -mtime +7 -delete

echo "Backup done."
