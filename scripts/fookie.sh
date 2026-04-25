#!/usr/bin/env bash
# Fookie dev CLI — runs in Git Bash (Windows) or any Linux/macOS shell.
# No `make` required. Run from the repo root.
#
# Usage:
#   ./scripts/fookie.sh docker-up
#   ./fookie.sh docker-up          (root wrapper forwards here)
#
#   ./scripts/fookie.sh scale-up
#   SERVERS=5 WORKERS=10 ./scripts/fookie.sh scale-up
#   HELM_RELEASE=prod KUBE_NAMESPACE=fookie ./scripts/fookie.sh helm-install

set -euo pipefail

RAW_COMMAND="${1:-help}"
PROFILE="${2:-${PROFILE:-full}}"

case "$RAW_COMMAND" in
    start) COMMAND="docker-up" ;;
    stop) COMMAND="docker-down" ;;
    status) COMMAND="docker-status" ;;
    logs) COMMAND="docker-logs" ;;
    *) COMMAND="$RAW_COMMAND" ;;
esac

COMPOSE_FULL=(-f demo/docker-compose.yml -f demo/compose.demo.yml)
COMPOSE_MIN=(-f demo/docker-compose.minimal.yml -f demo/compose.demo.yml)
COMPOSE_SCALE_FULL=(-f demo/docker-compose.yml -f deploy/compose/scale.yml)
COMPOSE_SCALE_MIN=(-f demo/docker-compose.minimal.yml -f deploy/compose/scale.yml)

SERVERS="${SERVERS:-3}"
WORKERS="${WORKERS:-5}"

HELM_IMAGE="${HELM_IMAGE:-alpine/helm:3}"
HELM_CHART="charts/fookie"
HELM_RELEASE="${HELM_RELEASE:-fookie}"
KUBE_NAMESPACE="${KUBE_NAMESPACE:-default}"
KUBECONFIG_DIR="${KUBECONFIG_DIR:-$HOME/.kube}"

# Convert Windows path (C:\foo) to Docker-compatible path (/c/foo)
to_docker_path() {
    echo "$1" | sed 's|\\|/|g' | sed 's|^\([A-Za-z]\):|/\L\1|'
}

helm_run() {
    local work_dir
    work_dir="$(to_docker_path "$(pwd)")"
    local kube_dir
    kube_dir="$(to_docker_path "$KUBECONFIG_DIR")"

    MSYS_NO_PATHCONV=1 docker run --rm \
        -v "${work_dir}:/workspace" \
        -w /workspace \
        -v "${kube_dir}:/root/.kube:ro" \
        -e HELM_CACHE_HOME=/tmp/helm-cache \
        "$HELM_IMAGE" "$@"
}

compose_args() {
    if [[ "$PROFILE" == "minimal" ]]; then
        printf '%s\n' "${COMPOSE_MIN[@]}"
    else
        printf '%s\n' "${COMPOSE_FULL[@]}"
    fi
}

scale_compose_args() {
    if [[ "$PROFILE" == "minimal" ]]; then
        printf '%s\n' "${COMPOSE_SCALE_MIN[@]}"
    else
        printf '%s\n' "${COMPOSE_SCALE_FULL[@]}"
    fi
}

run_compose() {
    mapfile -t files < <(compose_args)
    docker compose "${files[@]}" "$@"
}

run_scale_compose() {
    mapfile -t files < <(scale_compose_args)
    docker compose "${files[@]}" "$@"
}

case "$COMMAND" in

help)
    cat <<'EOF'
Fookie shell CLI  (Git Bash / Linux / macOS)
──────────────────────────────────────────────
Primary commands:
  ./fookie.sh start [full|minimal]
  ./fookie.sh stop [full|minimal]
  ./fookie.sh status [full|minimal]
  ./fookie.sh logs [full|minimal]

All-in-one image (postgres+redis+server+worker in one container):
  ./fookie.sh allinone-build     Build fookiejs/fookie image
  ./fookie.sh allinone-run       Run with demo/schema.fql on :8080
  ./fookie.sh allinone-run SCHEMA=/path/to/schema.fql

Docker (split server+worker, for dev/scale):
  ./fookie.sh docker-up          Build + start profile stack
  ./fookie.sh docker-down        Stop profile stack
  ./fookie.sh docker-status      Show profile stack status
  ./fookie.sh docker-clean       Stop + remove volumes
  ./fookie.sh docker-logs        Follow all logs
  ./fookie.sh docker-logs-server
  ./fookie.sh docker-logs-worker

Scale test:
  ./fookie.sh scale-up           3 servers + 5 workers (default)
  SERVERS=5 WORKERS=10 ./fookie.sh scale-up
  ./fookie.sh scale-down

Infra only:
  ./fookie.sh postgres-up
  ./fookie.sh redis-up
  ./fookie.sh infra-down

Grafana hub (Prometheus + Loki + Tempo + Grafana + exporters):
  ./fookie.sh observability-up    Start only the observability stack
  ./fookie.sh observability-down  Stop observability stack
  ./fookie.sh hub                 Open Grafana in browser (http://localhost:3000)
  ./fookie.sh grafana-bootstrap   Create read-only grafana_ro PG user

Helm (no local Helm — runs via Docker):
  ./fookie.sh helm-deps          Download chart dependencies
  ./fookie.sh helm-lint          Lint chart
  ./fookie.sh helm-template      Render YAML (no cluster needed)
  ./fookie.sh helm-install       Install to cluster
  ./fookie.sh helm-upgrade       Upgrade (or install) — idempotent
  ./fookie.sh helm-uninstall     Delete release
  ./fookie.sh helm-status        Show release status

Env overrides:
  PROFILE                        full|minimal
  SERVERS / WORKERS              Scale replica counts
  HELM_RELEASE                   Release name     (default: fookie)
  KUBE_NAMESPACE                 Namespace        (default: default)
  KUBECONFIG_DIR                 kubeconfig dir   (default: ~/.kube)
EOF
    ;;

# ── All-in-one image ─────────────────────────────────────────────────────────

allinone-build)
    docker build -f docker/Dockerfile.allinone -t fookiejs/fookie .
    echo "Image built: fookiejs/fookie"
    echo "Run: ./fookie.sh allinone-run"
    ;;

allinone-run)
    SCHEMA="${SCHEMA:-$(pwd)/demo/schema.fql}"
    echo "Starting fookiejs/fookie with schema: $SCHEMA"
    docker run --rm \
        -v "${SCHEMA}:/schema.fql:ro" \
        -p 8080:8080 \
        fookiejs/fookie
    ;;

# ── Docker ──────────────────────────────────────────────────────────────────

docker-up)
    echo "Building images..."
    run_compose build
    echo "Starting stack..."
    run_compose up -d
    sleep 3
    run_compose ps
    ;;

docker-down)
    run_compose down
    ;;

docker-status)
    run_compose ps
    ;;

docker-clean)
    run_compose down -v
    ;;

docker-logs)
    run_compose logs -f
    ;;

docker-logs-server)
    run_compose logs -f fookie-server
    ;;

docker-logs-worker)
    run_compose logs -f fookie-worker
    ;;

# ── Infra ────────────────────────────────────────────────────────────────────

postgres-up)
    docker compose -f deploy/compose/postgres.yml up -d postgres
    echo "Postgres → postgres://fookie:fookie_dev@localhost:5432/fookie?sslmode=disable"
    ;;

redis-up)
    docker compose -f deploy/compose/postgres.yml up -d redis
    echo "Redis → redis://localhost:6379"
    ;;

infra-down)
    docker compose -f deploy/compose/postgres.yml down
    ;;

# ── Scale ────────────────────────────────────────────────────────────────────

scale-up)
    echo "Building images..."
    run_compose build
    echo "Starting ${SERVERS} servers + ${WORKERS} workers..."
    run_scale_compose up -d \
        --scale fookie-server="$SERVERS" \
        --scale fookie-worker="$WORKERS"
    echo ""
    run_scale_compose ps
    echo ""
    echo "TIP: redis-cli monitor  →  watch LPUSH / BLPOP traffic"
    echo "TIP: ./fookie.sh docker-logs-worker"
    ;;

scale-down)
    run_scale_compose down
    ;;

# ── Grafana Hub ─────────────────────────────────────────────────────────────

observability-up)
    echo "Starting observability stack (Prometheus + Loki + Tempo + Grafana + exporters)..."
    docker compose -f deploy/compose/postgres.yml -f deploy/compose/observability.yml up -d
    sleep 3
    echo ""
    echo "Grafana        → http://localhost:3000   (anonymous admin)"
    echo "Prometheus     → http://localhost:9090"
    echo "Loki           → http://localhost:3100"
    echo "Tempo          → http://localhost:3200"
    echo "cAdvisor       → http://localhost:8088"
    echo ""
    echo "Bootstrap grafana_ro PG user:"
    echo "  ./fookie.sh grafana-bootstrap"
    ;;

observability-down)
    docker compose -f deploy/compose/postgres.yml -f deploy/compose/observability.yml down
    ;;

hub)
    URL="http://localhost:3000"
    echo "Opening $URL ..."
    if command -v xdg-open >/dev/null 2>&1; then xdg-open "$URL"
    elif command -v open >/dev/null 2>&1; then open "$URL"
    elif command -v start >/dev/null 2>&1; then start "$URL"
    else echo "Browser açamadım — manuel: $URL"
    fi
    ;;

grafana-bootstrap)
    echo "Creating read-only grafana_ro PG user..."
    docker compose -f deploy/compose/postgres.yml exec -T postgres \
        psql -U fookie -d fookie < scripts/grafana-bootstrap.sql
    echo "Done. Grafana PostgreSQL datasource artık çalışır."
    ;;

# ── Helm ─────────────────────────────────────────────────────────────────────

helm-deps)
    echo "Downloading chart dependencies..."
    helm_run dependency update "$HELM_CHART"
    ;;

helm-lint)
    "$0" helm-deps
    helm_run lint "$HELM_CHART"
    ;;

helm-template)
    "$0" helm-deps
    helm_run template "$HELM_RELEASE" "$HELM_CHART" --namespace "$KUBE_NAMESPACE"
    ;;

helm-install)
    "$0" helm-deps
    echo "Installing '$HELM_RELEASE' → namespace '$KUBE_NAMESPACE'..."
    helm_run install "$HELM_RELEASE" "$HELM_CHART" \
        --namespace "$KUBE_NAMESPACE" \
        --create-namespace \
        --wait
    ;;

helm-upgrade)
    "$0" helm-deps
    echo "Upgrading (or installing) '$HELM_RELEASE'..."
    helm_run upgrade --install "$HELM_RELEASE" "$HELM_CHART" \
        --namespace "$KUBE_NAMESPACE" \
        --create-namespace \
        --wait
    ;;

helm-uninstall)
    helm_run uninstall "$HELM_RELEASE" --namespace "$KUBE_NAMESPACE"
    ;;

helm-status)
    helm_run status "$HELM_RELEASE" --namespace "$KUBE_NAMESPACE"
    ;;

*)
    echo "Bilinmeyen komut: $COMMAND"
    echo "Yardım için: ./fookie.sh help"
    exit 1
    ;;
esac
