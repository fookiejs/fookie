#!/bin/sh
set -eu

COMMAND="${1:-help}"
PROFILE="${PROFILE:-full}"
SERVERS="${SERVERS:-3}"
WORKERS="${WORKERS:-5}"

compose_files_full="-f demo/docker-compose.yml -f demo/compose.demo.yml"
compose_files_min="-f demo/docker-compose.minimal.yml -f demo/compose.demo.yml"
compose_scale_full="-f demo/docker-compose.yml -f deploy/compose/scale.yml"
compose_scale_min="-f demo/docker-compose.minimal.yml -f deploy/compose/scale.yml"

if [ "$PROFILE" = "minimal" ]; then
  COMPOSE_FILES="$compose_files_min"
  COMPOSE_SCALE="$compose_scale_min"
else
  COMPOSE_FILES="$compose_files_full"
  COMPOSE_SCALE="$compose_scale_full"
fi

if [ ! -S /var/run/docker.sock ]; then
  echo "missing /var/run/docker.sock mount"
  echo "run with: -v /var/run/docker.sock:/var/run/docker.sock"
  exit 1
fi

run_compose() {
  # shellcheck disable=SC2086
  docker compose $COMPOSE_FILES "$@"
}

run_scale_compose() {
  # shellcheck disable=SC2086
  docker compose $COMPOSE_SCALE "$@"
}

case "$COMMAND" in
  start)
    run_compose up -d
    run_compose ps
    ;;
  stop)
    run_compose down
    ;;
  status)
    run_compose ps
    ;;
  logs)
    run_compose logs -f
    ;;
  scale)
    run_compose build
    run_scale_compose up -d --scale fookie-server="$SERVERS" --scale fookie-worker="$WORKERS"
    run_scale_compose ps
    ;;
  down-scale)
    run_scale_compose down
    ;;
  help|*)
    cat <<'EOF'
fookie/platform launcher

Usage:
  docker run --rm -it -v /var/run/docker.sock:/var/run/docker.sock fookie/platform start
  docker run --rm -it -v /var/run/docker.sock:/var/run/docker.sock -e PROFILE=minimal fookie/platform status
  docker run --rm -it -v /var/run/docker.sock:/var/run/docker.sock -e SERVERS=5 -e WORKERS=20 fookie/platform scale

Commands:
  start | stop | status | logs | scale | down-scale
EOF
    ;;
esac
