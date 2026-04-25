#!/bin/bash
# Fookie all-in-one container entrypoint.
# Starts PostgreSQL + Redis internally, then runs fookie-worker and fookie-server.
#
# Environment variables:
#   SCHEMA_PATH   Path to .fql file or directory of .fql files (default: /schema.fql)
#   PORT          Server listen port                            (default: :8080)
#   FOOKIE_DB_URL Override DB connection string (optional)
#   REDIS_URL     Override Redis URL              (optional)

set -euo pipefail

# ── Defaults ────────────────────────────────────────────────────────────────
SCHEMA_PATH="${SCHEMA_PATH:-/schema.fql}"
PORT="${PORT:-:8080}"
PGDATA="${PGDATA:-/var/lib/postgresql/data}"
PGUSER="fookie"
PGPASSWORD="fookie"
PGDB="fookie"
FOOKIE_DB_URL="${FOOKIE_DB_URL:-postgres://${PGUSER}:${PGPASSWORD}@localhost:5432/${PGDB}?sslmode=disable}"
REDIS_URL="${REDIS_URL:-redis://localhost:6379}"

export SCHEMA_PATH PORT FOOKIE_DB_URL REDIS_URL

# ── Validate schema ──────────────────────────────────────────────────────────
if [ ! -e "$SCHEMA_PATH" ]; then
  echo "ERROR: Schema not found at SCHEMA_PATH=$SCHEMA_PATH"
  echo "Mount your .fql file: docker run -v ./schema.fql:/schema.fql ..."
  echo "Or a directory:        docker run -v ./schemas:/schemas -e SCHEMA_PATH=/schemas ..."
  exit 1
fi

# ── PostgreSQL ───────────────────────────────────────────────────────────────
echo "[fookie] Initialising PostgreSQL..."

# Ensure data directory exists and is owned by postgres
mkdir -p "$PGDATA"
chown -R postgres:postgres "$PGDATA"

# initdb only on first run
if [ ! -f "$PGDATA/PG_VERSION" ]; then
  echo "[fookie] Running initdb..."
  su postgres -c "initdb -D '$PGDATA' --username=postgres --auth=trust --no-locale --encoding=UTF8" > /dev/null
fi

# Start PostgreSQL
echo "[fookie] Starting PostgreSQL..."
su postgres -c "pg_ctl start -D '$PGDATA' -l /var/log/postgresql.log -w -t 30"

# Create DB user and database (idempotent)
su postgres -c "psql -c \"CREATE USER ${PGUSER} WITH PASSWORD '${PGPASSWORD}';\"" 2>/dev/null || true
su postgres -c "psql -c \"CREATE DATABASE ${PGDB} OWNER ${PGUSER};\"" 2>/dev/null || true

echo "[fookie] PostgreSQL ready."

# ── Redis ────────────────────────────────────────────────────────────────────
echo "[fookie] Starting Redis..."
redis-server --daemonize yes --loglevel warning
echo "[fookie] Redis ready."

# ── Worker (background) ──────────────────────────────────────────────────────
echo "[fookie] Starting worker..."
DB_URL="$FOOKIE_DB_URL" \
  REDIS_URL="$REDIS_URL" \
  fookie-worker \
    -db "$FOOKIE_DB_URL" \
    -schema "$SCHEMA_PATH" \
  &
WORKER_PID=$!
echo "[fookie] Worker PID: $WORKER_PID"

# ── Server (foreground / PID 1) ──────────────────────────────────────────────
echo "[fookie] Starting server on $PORT..."
exec fookie-server \
  -db "$FOOKIE_DB_URL" \
  -schema "$SCHEMA_PATH" \
  -port "$PORT"
