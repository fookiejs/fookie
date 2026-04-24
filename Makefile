COMPOSE_DEMO   = -f demo/docker-compose.yml -f demo/compose.demo.yml
COMPOSE_SCALE  = -f demo/docker-compose.yml -f deploy/compose/scale.yml
COMPOSE_PLATFORM = -f deploy/compose/postgres.yml -f deploy/compose/observability.yml -f deploy/compose/apps.yml

.PHONY: help build build-fookie test test-unit test-integration run-server run-worker \
        postgres-up postgres-down redis-up redis-down \
        docker-up docker-down docker-clean \
        scale-up scale-down \
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
