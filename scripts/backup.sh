#!/bin/sh
set -eu

: "${DATABASE_URL:?DATABASE_URL harus diisi}"
server_major=$(($(psql "$DATABASE_URL" -Atqc 'SHOW server_version_num') / 10000))
client_major=$(pg_dump --version | awk '{print $3}' | cut -d. -f1)
if [ "$server_major" != "$client_major" ]; then
  echo "pg_dump versi $client_major harus sama dengan Postgres $server_major agar arsip bisa dipulihkan." >&2
  exit 1
fi
backup_dir=${BACKUP_DIR:-./backups}
umask 077
mkdir -p "$backup_dir"
file="$backup_dir/rukun-$(date -u +%Y%m%dT%H%M%SZ)-$$.dump"
tmp="$file.tmp"
trap 'rm -f "$tmp"' EXIT
trap 'exit 1' HUP INT TERM

pg_dump --format=custom --no-owner --no-acl --file="$tmp" "$DATABASE_URL"
pg_restore --list "$tmp" >/dev/null
mv "$tmp" "$file"
printf '%s\n' "$file"
