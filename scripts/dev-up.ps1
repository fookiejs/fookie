# Local dev: PostgreSQL only (run server/worker on the host with Go).
# Requires Docker Desktop (or compatible engine).

$ErrorActionPreference = "Stop"
Set-Location (Split-Path -Parent $PSScriptRoot)

Write-Host "Starting PostgreSQL (docker-compose)..."
docker-compose up -d postgres

$dbUrl = "postgres://fookie:fookie_dev@localhost:5432/fookie?sslmode=disable"
Write-Host ""
Write-Host "Database: $dbUrl"
Write-Host ""
Write-Host "Next (examples):"
Write-Host "  go run ./cmd/server -schema schemas/wallet_transfer.fql -db `"$dbUrl`""
Write-Host "  go run ./cmd/worker -db `"$dbUrl`""
Write-Host "  # Then open http://localhost:8080/demo/"
Write-Host ""
