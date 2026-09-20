#!/usr/bin/env bash
# fork-only dev workflow helper, exclude from upstream PR.
#
# Upgrade + idempotency check for the token model_mapping migration:
#   1. the upstream baseline binary creates the schema,
#   2. a legacy token row is inserted,
#   3. the head binary starts twice (once with Redis enabled),
#   4. tokens.model_mapping must be nullable text, the legacy row must stay NULL,
#      and both head startups must produce an identical schema.
#
# Usage: dev-ci-db-upgrade.sh <sqlite|mysql|postgres>
# Requires pre-built binaries at /tmp/new-api-baseline and /tmp/new-api-head.
set -euo pipefail

dialect="${1:-}"
case "$dialect" in
  sqlite | mysql | postgres) ;;
  *)
    echo "usage: $0 <sqlite|mysql|postgres>" >&2
    exit 2
    ;;
esac

baseline_bin=/tmp/new-api-baseline
head_bin=/tmp/new-api-head
app_port=3100
base_url="http://127.0.0.1:${app_port}"
redis_dsn=redis://127.0.0.1:6379

for bin in "$baseline_bin" "$head_bin"; do
  if [ ! -x "$bin" ]; then
    echo "::error::missing executable $bin"
    exit 1
  fi
done

# 48-character legacy token key, matching the pre-varchar(128) key format.
legacy_key=$(printf '%-48s' 'legacy-upgrade-token' | tr ' ' '0')
legacy_name='legacy-upgrade-token'

case "$dialect" in
  mysql)
    db_name=na_up_m80
    app_sql_dsn="root:123456@tcp(127.0.0.1:3306)/${db_name}?charset=utf8mb4&parseTime=True&loc=Local"
    key_column='`key`'
    ;;
  postgres)
    db_name=na_up_p15
    app_sql_dsn="postgres://root:123456@127.0.0.1:5432/${db_name}?sslmode=disable"
    key_column='"key"'
    ;;
  sqlite)
    sqlite_path=/tmp/na_up.db
    key_column='"key"'
    ;;
esac

run_sql() {
  case "$dialect" in
    mysql)
      mysql --host=127.0.0.1 --port=3306 --user=root --password=123456 \
        --batch --skip-column-names "$db_name" --execute "$1"
      ;;
    postgres)
      PGPASSWORD=123456 psql --host=127.0.0.1 --port=5432 --username=root \
        --dbname="$db_name" --no-align --tuples-only --quiet --command "$1"
      ;;
    sqlite)
      sqlite3 "$sqlite_path" "$1"
      ;;
  esac
}

reset_database() {
  case "$dialect" in
    mysql)
      mysql --host=127.0.0.1 --port=3306 --user=root --password=123456 \
        --execute "DROP DATABASE IF EXISTS ${db_name}; CREATE DATABASE ${db_name} DEFAULT CHARACTER SET utf8mb4"
      ;;
    postgres)
      PGPASSWORD=123456 psql --host=127.0.0.1 --port=5432 --username=root \
        --dbname=postgres --quiet --command "DROP DATABASE IF EXISTS ${db_name}"
      PGPASSWORD=123456 psql --host=127.0.0.1 --port=5432 --username=root \
        --dbname=postgres --quiet --command "CREATE DATABASE ${db_name}"
      ;;
    sqlite)
      rm -f "$sqlite_path" "$sqlite_path-wal" "$sqlite_path-shm"
      ;;
  esac
}

run_mysql_admin() {
  mysql --host=127.0.0.1 --port=3306 --user=root --password=123456 --execute "$1"
}

stop_app() {
  [ -n "${app_pid:-}" ] || return 0
  kill "$app_pid" 2>/dev/null || true
  for _ in $(seq 1 30); do
    if ! kill -0 "$app_pid" 2>/dev/null; then
      app_pid=""
      return 0
    fi
    sleep 1
  done
  kill -9 "$app_pid" 2>/dev/null || true
  wait "$app_pid" 2>/dev/null || true
  app_pid=""
}

trap 'stop_app' EXIT

start_app() { # start_app <binary> <redis:on|off> <log file>
  local binary="$1" redis="$2" log_file="$3"
  if curl -sf "$base_url/api/status" >/dev/null 2>&1; then
    echo "::error::port ${app_port} already answers /api/status before start"
    exit 1
  fi

  local -a app_env=(PORT="$app_port")
  case "$dialect" in
    mysql | postgres) app_env+=(SQL_DSN="$app_sql_dsn") ;;
    sqlite) app_env+=(SQLITE_PATH="$sqlite_path") ;;
  esac
  if [ "$redis" = "on" ]; then
    app_env+=(REDIS_CONN_STRING="$redis_dsn")
  fi

  (
    cd /tmp
    env -u REDIS_CONN_STRING -u SQL_DSN -u SQLITE_PATH "${app_env[@]}" "$binary"
  ) >"$log_file" 2>&1 &
  app_pid=$!

  local ready=0
  for _ in $(seq 1 60); do
    if curl -sf "$base_url/api/status" >/dev/null 2>&1; then
      ready=1
      break
    fi
    if ! kill -0 "$app_pid" 2>/dev/null; then
      break
    fi
    sleep 1
  done
  if [ "$ready" -ne 1 ]; then
    echo "::error::$(basename "$binary") (${dialect}, redis=${redis}) did not answer /api/status within 60s"
    sed -n '1,80p' "$log_file"
    stop_app
    exit 1
  fi
  echo "started $(basename "$binary") (dialect=${dialect} redis=${redis})"
}

stop_app
echo "==> reset ${dialect} database"
reset_database

echo "==> 1/3 baseline (upstream main) startup on ${dialect}"
start_app "$baseline_bin" off /tmp/dev-ci-baseline.log
stop_app
echo "baseline startup log tail:"
tail -n 5 /tmp/dev-ci-baseline.log

case "$dialect" in
  mysql)
    run_sql "INSERT INTO tokens (user_id, ${key_column}, status, name, created_time, accessed_time, expired_time, remain_quota, unlimited_quota, model_limits_enabled, model_limits, used_quota, cross_group_retry)
      VALUES (1, '${legacy_key}', 1, '${legacy_name}', 1, 1, -1, 100, 1, 0, '', 0, 0)"
    ;;
  postgres)
    run_sql "INSERT INTO tokens (user_id, ${key_column}, status, name, created_time, accessed_time, expired_time, remain_quota, unlimited_quota, model_limits_enabled, model_limits, used_quota, cross_group_retry)
      VALUES (1, '${legacy_key}', 1, '${legacy_name}', 1, 1, -1, 100, true, false, '', 0, false)"
    ;;
  sqlite)
    run_sql "INSERT INTO tokens (user_id, ${key_column}, status, name, created_time, accessed_time, expired_time, remain_quota, unlimited_quota, model_limits_enabled, model_limits, used_quota, cross_group_retry)
      VALUES (1, '${legacy_key}', 1, '${legacy_name}', 1, 1, -1, 100, 1, 0, '', 0, 0)"
    ;;
esac
echo "inserted legacy token row (key=${legacy_key}, ${#legacy_key} chars)"

columns_snapshot() {
  case "$dialect" in
    mysql)
      run_sql "SELECT column_name, column_type, is_nullable FROM information_schema.columns
        WHERE table_schema = DATABASE() AND table_name = 'tokens' ORDER BY column_name"
      ;;
    postgres)
      run_sql "SELECT column_name, data_type, is_nullable FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name = 'tokens' ORDER BY column_name"
      ;;
    sqlite)
      run_sql "SELECT name, type, CASE notnull WHEN 0 THEN 'YES' ELSE 'NO' END FROM pragma_table_info('tokens') ORDER BY name"
      ;;
  esac | tr '\t' '|' | tr -d '\r'
}

print_columns() {
  echo "tokens columns (${dialect}):"
  columns_snapshot | tr 'A-Z' 'a-z'
}

assert_column_present() {
  local columns
  columns=$(columns_snapshot | tr 'A-Z' 'a-z')
  print_columns
  if [ -z "$columns" ]; then
    echo "::error::tokens table missing on ${dialect}"
    exit 1
  fi
  if ! grep -qx 'model_mapping|text|yes' <<<"$columns"; then
    echo "::error::expected a nullable text column tokens.model_mapping on ${dialect}"
    exit 1
  fi
  echo "OK: tokens.model_mapping is text and nullable (${dialect})"
}

assert_column_absent() {
  local columns
  columns=$(columns_snapshot | tr 'A-Z' 'a-z')
  print_columns
  if [ -z "$columns" ]; then
    echo "::error::baseline binary did not create the tokens table on ${dialect}"
    exit 1
  fi
  if grep -q '^model_mapping|' <<<"$columns"; then
    echo "::error::baseline schema already contains tokens.model_mapping on ${dialect}"
    exit 1
  fi
  echo "OK: baseline schema has no tokens.model_mapping column (${dialect})"
}

schema_dump() {
  case "$dialect" in
    mysql)
      run_mysql_admin "SHOW CREATE TABLE ${db_name}.tokens"
      ;;
    postgres)
      PGPASSWORD=123456 psql --host=127.0.0.1 --port=5432 --username=root \
        --dbname="$db_name" --command '\d tokens'
      ;;
    sqlite)
      sqlite3 "$sqlite_path" ".schema tokens"
      ;;
  esac | tr -d '\r'
}

assert_column_absent

echo "==> 2/3 head startup #1 on ${dialect} (Redis enabled)"
start_app "$head_bin" on /tmp/dev-ci-head-1.log
stop_app
schema_dump >/tmp/dev-ci-schema-head-1.sql
echo "schema after head startup #1:"
cat /tmp/dev-ci-schema-head-1.sql

echo "==> 3/3 head startup #2 on ${dialect} (Redis disabled)"
start_app "$head_bin" off /tmp/dev-ci-head-2.log
stop_app
schema_dump >/tmp/dev-ci-schema-head-2.sql
echo "schema after head startup #2:"
cat /tmp/dev-ci-schema-head-2.sql

if ! diff -u /tmp/dev-ci-schema-head-1.sql /tmp/dev-ci-schema-head-2.sql; then
  echo "::error::tokens schema changed between two head startups on ${dialect}"
  exit 1
fi
echo "OK: schema diff between the two head startups is empty (${dialect})"

assert_column_present

legacy_state=$(run_sql "SELECT CASE WHEN model_mapping IS NULL THEN 'null' ELSE 'not-null' END FROM tokens WHERE ${key_column} = '${legacy_key}'" | tr -d '[:space:]')
legacy_key_state=$(run_sql "SELECT ${key_column} FROM tokens WHERE ${key_column} = '${legacy_key}'" | tr -d '[:space:]')
echo "legacy row: model_mapping=${legacy_state} key=${legacy_key_state}"
if [ "$legacy_state" != "null" ]; then
  echo "::error::legacy token row model_mapping is not NULL on ${dialect}"
  exit 1
fi
if [ "$legacy_key_state" != "$legacy_key" ]; then
  echo "::error::legacy token key was not preserved on ${dialect}"
  exit 1
fi

echo "PASS: ${dialect} upgrade + idempotency checks are green"
