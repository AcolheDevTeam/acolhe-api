#!/bin/sh
set -eu

backup_dir="${BACKUP_DIR:-/backups}"
retention_days="${BACKUP_RETENTION_DAYS:-7}"
stamp="$(date -u +%F)"
destination="${backup_dir}/acolhe-${stamp}.sql.gz"
temporary="${destination}.tmp"
lock_dir="${backup_dir}/.backup.lock"

mkdir -p "$backup_dir"
if ! mkdir "$lock_dir" 2>/dev/null; then
	echo "Postgres backup already running; skipping"
	exit 0
fi
trap 'rm -f "$temporary"; rmdir "$lock_dir"' EXIT

pg_dump --no-owner --no-privileges | gzip -9 > "$temporary"
mv "$temporary" "$destination"
find "$backup_dir" -type f -name 'acolhe-*.sql.gz' -mtime "+${retention_days}" -delete

echo "Postgres backup completed: ${destination}"
