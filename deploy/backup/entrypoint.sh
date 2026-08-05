#!/bin/sh
set -eu

schedule="${BACKUP_CRON:-0 3 * * *}"
printf '%s sh /scripts/run.sh >> /proc/1/fd/1 2>> /proc/1/fd/2\n' "$schedule" \
	> /etc/crontabs/root

echo "Postgres backup scheduled: ${schedule}"
exec crond -f -l 2
