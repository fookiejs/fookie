.PHONY: help build test test-unit test-integration run-server run-worker postgres-up postgres-down docker-up docker-down clean parser

help:
	@echo "Fookie Framework - Build Commands"
	@echo ""
	@echo "Development:"
	@echo "  make build - Build all binaries"
	@echo "  make test              - Run all Go tests (pkg + tests/*)"
	@echo "  make test-unit         - Fast tests only (no Docker)"
	@echo "  make test-integration  - Integration tests (requires Docker for Testcontainers)"
	@echo "  make parser            - Build parser CLI tool"
	@echo ""
	@echo "Local database (Docker):"
	@echo "  make postgres-up       - Start only PostgreSQL (then: make run-server)"
	@echo "  make postgres-down     - Stop PostgreSQL container"
	@echo ""
	@echo "Running:"
	@echo "  make run-server        - Run server locally (requires DB; see postgres-up)"
	@echo "  make run-worker        - Run worker locally (requires DB)"
	@echo ""
	@echo "Docker (full stack):"
	@echo "  make docker-up         - Build images and start postgres + server + worker"
	@echo "  make docker-down       - Stop Docker containers"
	@echo "  make docker-clean      - Remove containers and volumes"
	@echo ""
	@echo "Utilities:"
	@echo "  make lint              - Run linter"
	@echo "  make fmt               - Format code"
	@echo "  make clean             - Clean build artifacts"

# Build targets
build: build-server build-parser build-worker

build-server:
	@echo "Building server..."
	go build -o bin/server ./cmd/server

build-parser:
	@echo "Building parser..."
	go build -o bin/parser ./cmd/parser

build-worker:
	@echo "Building worker..."
	go build -o bin/worker ./cmd/worker

# Test targets (integration tests use Testcontainers — Docker must be running)
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

# Run targets
run-server: build-server
	@echo "Starting server..."
	./bin/server -schema schemas/wallet_transfer.fql -db "postgres://fookie:fookie_dev@localhost:5432/fookie?sslmode=disable"

run-worker: build-worker
	@echo "Starting worker..."
	./bin/worker -db "postgres://fookie:fookie_dev@localhost:5432/fookie?sslmode=disable"

# Start only Postgres from docker-compose (matches default -db URL in cmd/server and cmd/worker)
postgres-up:
	docker-compose up -d postgres
	@echo "Postgres ready at postgres://fookie:fookie_dev@localhost:5432/fookie?sslmode=disable"

postgres-down:
	docker-compose stop postgres

run-parser: build-parser
	@echo "Running parser on wallet_transfer.fql..."
	./bin/parser -schema schemas/wallet_transfer.fql -sql

# Docker targets
docker-build:
	@echo "Building Docker images..."
	docker-compose build

docker-up: docker-build
	@echo "Starting Docker containers..."
	docker-compose up -d
	@echo "Waiting for services to start..."
	@sleep 5
	@echo "Services are up!"
	@docker-compose ps

docker-down:
	@echo "Stopping Docker containers..."
	docker-compose down

docker-logs-server:
	docker-compose logs -f fookie-server

docker-logs-worker:
	docker-compose logs -f fookie-worker

docker-logs-postgres:
	docker-compose logs -f postgres

docker-clean: docker-down
	@echo "Cleaning Docker volumes..."
	docker-compose down -v
	@echo "Docker cleanup complete"

docker-shell-postgres:
	docker-compose exec postgres psql -U fookie -d fookie

# Code quality targets
fmt:
	@echo "Formatting code..."
	go fmt ./...

lint:
	@echo "Running linter..."
	golangci-lint run ./...

# Utility targets
generate-migrations:
	@echo "Generating migrations from schema..."
	./bin/parser -schema schemas/wallet_transfer.fql -sql > migrations/001_initial.sql

deps:
	go mod download
	go mod tidy

clean:
	@echo "Cleaning build artifacts..."
	rm -rf bin/
	go clean

.PHONY: generate-migrations
