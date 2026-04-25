COMPOSE_DEMO     = -f demo/docker-compose.yml -f demo/compose.demo.yml
COMPOSE_SCALE    = -f demo/docker-compose.yml -f deploy/compose/scale.yml
COMPOSE_PLATFORM = -f deploy/compose/postgres.yml -f deploy/compose/observability.yml -f deploy/compose/apps.yml

# ---------------------------------------------------------------------------
# Helm via Docker — no local Helm install needed.
# Kubeconfig is mounted read-only from ~/.kube; override with KUBECONFIG_PATH.
# ---------------------------------------------------------------------------
KUBECONFIG_PATH ?= $(HOME)/.kube
HELM_IMAGE      ?= alpine/helm:3
# MSYS_NO_PATHCONV=1 prevents Git Bash from translating /workspace → C:/Program Files/Git/workspace.
# The //workspace double-slash is a secondary guard for the -w flag.
HELM = MSYS_NO_PATHCONV=1 docker run --rm \
	-v "$(CURDIR):/workspace" \
	-w //workspace \
	-v "$(KUBECONFIG_PATH):/root/.kube:ro" \
	$(HELM_IMAGE)

HELM_CHART       = charts/fookie
HELM_RELEASE    ?= fookie
KUBE_NAMESPACE  ?= default

.PHONY: help build build-fookie test test-unit test-integration run-server run-worker \
        postgres-up postgres-down redis-up redis-down \
        docker-up docker-down docker-clean \
        scale-up scale-down \
        helm-deps helm-lint helm-template helm-install helm-upgrade helm-uninstall helm-status \
        clean parser

help:
	@echo "Fookie Framework - Build Commands"
	@echo ""
	@echo "Development:"
	@echo "  make build             - Build all binaries"
	@echo "  make test              - Run all Go tests (pkg + tests/*)"
	@echo "  make test-unit         - Fast tests only (no Docker)"
	@echo "  make test-integration  - Integration tests (requires Docker for Testcontainers)"
	@echo "  make parser            - Build parser CLI tool"
	@echo ""
	@echo "Local infra (Docker):"
	@echo "  make postgres-up       - Start PostgreSQL (then: make run-server)"
	@echo "  make postgres-down     - Stop PostgreSQL container"
	@echo "  make redis-up          - Start Redis (then: REDIS_URL=redis://localhost:6379 make run-server)"
	@echo "  make redis-down        - Stop Redis container"
	@echo ""
	@echo "Running:"
	@echo "  make run-server        - Run server locally (requires DB; see postgres-up)"
	@echo "  make run-worker        - Run worker locally (requires DB)"
	@echo ""
	@echo "Docker (full stack):"
	@echo "  make docker-up         - Build + start full stack (single server + worker)"
	@echo "  make docker-down       - Stop all containers"
	@echo "  make docker-clean      - Remove containers and volumes"
	@echo ""
	@echo "Scale testing (N servers + M workers):"
	@echo "  make scale-up          - Build + start 3 servers + 5 workers (no port conflicts)"
	@echo "  make scale-down        - Stop scaled stack"
	@echo "  Override: SERVERS=5 WORKERS=10 make scale-up"
	@echo ""
	@echo "Helm (no local install — runs via Docker):"
	@echo "  make helm-deps         - Download chart dependencies (Bitnami Redis subchart)"
	@echo "  make helm-lint         - Lint the Helm chart"
	@echo "  make helm-template     - Render chart to stdout (dry-run / inspect)"
	@echo "  make helm-install      - Install to cluster (uses ~/.kube/config)"
	@echo "  make helm-upgrade      - Upgrade existing release"
	@echo "  make helm-uninstall    - Delete release from cluster"
	@echo "  make helm-status       - Show release status"
	@echo "  Override: HELM_RELEASE=prod KUBE_NAMESPACE=fookie make helm-install"
	@echo "  Override: KUBECONFIG_PATH=~/rancher.yaml make helm-install"
	@echo ""
	@echo "Utilities:"
	@echo "  make lint              - Run linter"
	@echo "  make fmt               - Format code"
	@echo "  make clean             - Clean build artifacts"

build: build-server build-parser build-worker build-fookie

build-fookie:
	@echo "Building fookie CLI..."
	go build -o bin/fookie ./cmd/fookie

build-server:
	@echo "Building server..."
	go build -o bin/server ./cmd/server

build-parser:
	@echo "Building parser..."
	go build -o bin/parser ./cmd/parser

build-worker:
	@echo "Building worker..."
	go build -o bin/worker ./cmd/worker

test:
	@echo "Running all tests..."
	go test -count=1 -v -cover ./pkg/... ./tests/...

test-unit:
	@echo "Running unit tests (no integration / no Docker required)..."
	go test -count=1 -v -cover ./pkg/... ./tests/unit/...

test-integration:
	@echo "Running integration tests (Docker required)..."
	go test -count=1 -v -timeout 30m ./tests/integration/...

test-parser:
	@echo "Testing parser..."
	go test -v -run TestParser ./tests/unit/...

test-compiler:
	@echo "Testing compiler..."
	go test -v -run TestSQLGenerator ./tests/unit/...

run-server: build-server
	@echo "Starting server..."
	./bin/server -db "postgres://fookie:fookie_dev@localhost:5432/fookie?sslmode=disable"

run-worker: build-worker
	@echo "Starting worker..."
	./bin/worker -db "postgres://fookie:fookie_dev@localhost:5432/fookie?sslmode=disable"

postgres-up:
	docker compose -f deploy/compose/postgres.yml up -d postgres
	@echo "Postgres ready at postgres://fookie:fookie_dev@localhost:5432/fookie?sslmode=disable"

postgres-down:
	docker compose -f deploy/compose/postgres.yml stop postgres

redis-up:
	docker compose -f deploy/compose/postgres.yml up -d redis
	@echo "Redis ready at redis://localhost:6379"

redis-down:
	docker compose -f deploy/compose/postgres.yml stop redis

run-parser: build-parser
	@echo "Running parser on demo/schema.fql..."
	./bin/parser -schema demo/schema.fql -sql

docker-build:
	@echo "Building Docker images..."
	docker compose $(COMPOSE_DEMO) build

docker-up: docker-build
	@echo "Starting Docker containers..."
	docker compose $(COMPOSE_DEMO) up -d
	@echo "Waiting for services to start..."
	@sleep 5
	@echo "Services are up!"
	@docker compose $(COMPOSE_DEMO) ps

docker-down:
	@echo "Stopping Docker containers..."
	docker compose $(COMPOSE_DEMO) down

docker-logs-server:
	docker compose $(COMPOSE_DEMO) logs -f fookie-server

docker-logs-worker:
	docker compose $(COMPOSE_DEMO) logs -f fookie-worker

docker-logs-postgres:
	docker compose $(COMPOSE_DEMO) logs -f postgres

docker-clean: docker-down
	@echo "Cleaning Docker volumes..."
	docker compose $(COMPOSE_DEMO) down -v
	@echo "Docker cleanup complete"

# --- Scale testing -------------------------------------------------------
SERVERS ?= 3
WORKERS ?= 5

scale-up: docker-build
	@echo "Starting scaled stack: $(SERVERS) servers, $(WORKERS) workers..."
	docker compose $(COMPOSE_SCALE) up -d \
		--scale fookie-server=$(SERVERS) \
		--scale fookie-worker=$(WORKERS)
	@echo ""
	@echo "Scaled stack running:"
	@docker compose $(COMPOSE_SCALE) ps
	@echo ""
	@echo "TIP: redis-cli monitor  →  watch LPUSH / BLPOP traffic"
	@echo "TIP: docker compose $(COMPOSE_SCALE) logs -f fookie-worker"

scale-down:
	docker compose $(COMPOSE_SCALE) down

# --- Helm (runs inside Docker, no local Helm required) --------------------

# 1. Download subchart dependencies (Bitnami Redis).
#    Run once after cloning, or after changing Chart.yaml.
helm-deps:
	$(HELM) dependency update $(HELM_CHART)

# 2. Validate chart structure and values.
helm-lint: helm-deps
	$(HELM) lint $(HELM_CHART)

# 3. Render templates to stdout — useful for reviewing generated YAML.
#    Does NOT require a running cluster.
helm-template: helm-deps
	$(HELM) template $(HELM_RELEASE) $(HELM_CHART) \
		--namespace $(KUBE_NAMESPACE)

# 4. First-time install to cluster.
helm-install: helm-deps
	$(HELM) install $(HELM_RELEASE) $(HELM_CHART) \
		--namespace $(KUBE_NAMESPACE) \
		--create-namespace \
		--wait

# 5. Upgrade an existing release (idempotent — safe to re-run).
helm-upgrade: helm-deps
	$(HELM) upgrade --install $(HELM_RELEASE) $(HELM_CHART) \
		--namespace $(KUBE_NAMESPACE) \
		--create-namespace \
		--wait

# 6. Delete release (keeps PVCs by default).
helm-uninstall:
	$(HELM) uninstall $(HELM_RELEASE) \
		--namespace $(KUBE_NAMESPACE)

# 7. Show current release status.
helm-status:
	$(HELM) status $(HELM_RELEASE) \
		--namespace $(KUBE_NAMESPACE)

docker-shell-postgres:
	docker compose $(COMPOSE_DEMO) exec postgres psql -U fookie -d fookie

fmt:
	@echo "Formatting code..."
	go fmt ./...

lint:
	@echo "Running linter..."
	golangci-lint run ./...

generate-migrations:
	@echo "Generating migrations from schema..."
	./bin/parser -schema demo/schema.fql -sql > migrations/001_initial.sql

deps:
	go mod download
	go mod tidy

clean:
	@echo "Cleaning build artifacts..."
	rm -rf bin/
	go clean

.PHONY: generate-migrations
