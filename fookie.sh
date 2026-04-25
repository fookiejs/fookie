#!/usr/bin/env bash
# Fookie dev CLI — runs in Git Bash (Windows) or any Linux/macOS shell.
# No `make` required.
#
# Usage:
#   ./fookie.sh docker-up
#   ./fookie.sh scale-up
#   SERVERS=5 WORKERS=10 ./fookie.sh scale-up
#   HELM_RELEASE=prod KUBE_NAMESPACE=fookie ./fookie.sh helm-install

set -euo pipefail

COMMAND="${1:-help}"

COMPOSE_DEMO="-f demo/docker-compose.yml -f demo/compose.demo.yml"
COMPOSE_SCALE="-f demo/docker-compose.yml -f deploy/compose/scale.yml"

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

case "$COMMAND" in

help)
    cat <<'EOF'
Fookie shell CLI  (Git Bash / Linux / macOS)
──────────────────────────────────────────────
Docker (local):
  ./fookie.sh docker-up          Build + start single server/worker
  ./fookie.sh docker-down        Stop containers
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

Helm (no local Helm — runs via Docker):
  ./fookie.sh helm-deps          Download chart dependencies
  ./fookie.sh helm-lint          Lint chart
  ./fookie.sh helm-template      Render YAML (no cluster needed)
  ./fookie.sh helm-install       Install to cluster
  ./fookie.sh helm-upgrade       Upgrade (or install) — idempotent
  ./fookie.sh helm-uninstall     Delete release
  ./fookie.sh helm-status        Show release status

Env overrides:
  SERVERS / WORKERS              Scale replica counts
  HELM_RELEASE                   Release name     (default: fookie)
  KUBE_NAMESPACE                 Namespace        (default: default)
  KUBECONFIG_DIR                 kubeconfig dir   (default: ~/.kube)
EOF
    ;;

# ── Docker ──────────────────────────────────────────────────────────────────

docker-up)
    echo "Building images..."
    docker compose $COMPOSE_DEMO build
    echo "Starting stack..."
    docker compose $COMPOSE_DEMO up -d
    sleep 3
    docker compose $COMPOSE_DEMO ps
    ;;

docker-down)
    docker compose $COMPOSE_DEMO down
    ;;

docker-clean)
    docker compose $COMPOSE_DEMO down -v
    ;;

docker-logs)
    docker compose $COMPOSE_DEMO logs -f
    ;;

docker-logs-server)
    docker compose $COMPOSE_DEMO logs -f fookie-server
    ;;

docker-logs-worker)
    docker compose $COMPOSE_DEMO logs -f fookie-worker
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
    docker compose $COMPOSE_DEMO build
    echo "Starting ${SERVERS} servers + ${WORKERS} workers..."
    docker compose $COMPOSE_SCALE up -d \
        --scale fookie-server="$SERVERS" \
        --scale fookie-worker="$WORKERS"
    echo ""
    docker compose $COMPOSE_SCALE ps
    echo ""
    echo "TIP: redis-cli monitor  →  izle LPUSH / BLPOP trafiği"
    echo "TIP: ./fookie.sh docker-logs-worker"
    ;;

scale-down)
    docker compose $COMPOSE_SCALE down
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
