#!/bin/sh
set -eu

: "${RESTORE_DATABASE_URL:?RESTORE_DATABASE_URL harus menunjuk database uji yang kosong}"
: "${1:?berikan lokasi berkas .dump}"
server_major=$(($(psql "$RESTORE_DATABASE_URL" -Atqc 'SHOW server_version_num') / 10000))
client_major=$(pg_restore --version | awk '{print $3}' | cut -d. -f1)
if [ "$server_major" != "$client_major" ]; then
  echo "pg_restore versi $client_major harus sama dengan Postgres $server_major." >&2
  exit 1
fi

# Jangan izinkan data hidup menjadi target pemeriksaan pemulihan.
if [ "${DATABASE_URL:-}" = "$RESTORE_DATABASE_URL" ]; then
  echo 'Target pemulihan sama dengan DATABASE_URL.' >&2
  exit 1
fi
db_name=$(psql "$RESTORE_DATABASE_URL" -Atqc 'SELECT current_database()')
case "$db_name" in
  rukun_restore_*) ;;
  *) echo 'Nama database uji harus diawali rukun_restore_.' >&2; exit 1 ;;
esac
tables=$(psql "$RESTORE_DATABASE_URL" -Atqc "SELECT count(*) FROM pg_tables WHERE schemaname = 'public'")
if [ "$tables" != 0 ]; then
  echo 'Target pemulihan harus database kosong.' >&2
  exit 1
fi

pg_restore --exit-on-error --single-transaction --no-owner --no-acl \
  --dbname="$RESTORE_DATABASE_URL" "$1"
psql "$RESTORE_DATABASE_URL" -Atqc \
  "SELECT 'keluarga=' || count(*) FROM families UNION ALL SELECT 'transaksi=' || count(*) FROM transactions UNION ALL SELECT 'migrasi=' || count(*) FROM schema_migrations"
