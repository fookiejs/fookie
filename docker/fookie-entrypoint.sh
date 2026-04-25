#!/bin/sh
# fookie-entrypoint.sh — process mode dispatcher.
# No postgres/redis setup here — all connections come from env.
set -e

# Validate required env vars
if [ -z "$DB_URL" ]; then
  echo "[fookie] ERROR: DB_URL is not set. Exiting." >&2
  exit 1
fi

if [ ! -f "$SCHEMA_PATH" ] && [ ! -d "$SCHEMA_PATH" ]; then
  echo "[fookie] ERROR: SCHEMA_PATH='$SCHEMA_PATH' not found. Mount your .fql file." >&2
  exit 1
fi

echo "[fookie] mode=$FOOKIE_MODE schema=$SCHEMA_PATH port=$PORT"

run_server() {
  exec /app/fookie-server \
    -db    "$DB_URL" \
    -schema "$SCHEMA_PATH" \
    -port  "$PORT"
}

run_worker() {
  exec /app/fookie-worker \
    -db    "$DB_URL" \
    -schema "$SCHEMA_PATH"
}

case "$FOOKIE_MODE" in
  server)
    run_server
    ;;
  worker)
    run_worker
    ;;
  both)
    # Start worker in background, server as PID 1.
    /app/fookie-worker \
      -db    "$DB_URL" \
      -schema "$SCHEMA_PATH" &
    WORKER_PID=$!
    echo "[fookie] worker started (pid=$WORKER_PID)"
    # Trap SIGTERM to gracefully stop worker when server exits
    trap 'kill "$WORKER_PID" 2>/dev/null' TERM INT
    run_server
    ;;
  *)
    echo "[fookie] ERROR: Unknown FOOKIE_MODE='$FOOKIE_MODE'. Use: server | worker | both" >&2
    exit 1
    ;;
esac
