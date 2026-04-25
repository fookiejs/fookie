#Requires -Version 5.1
<#
.SYNOPSIS
    Fookie dev CLI — replaces `make` for PowerShell users.
    Run from the repo root.
.EXAMPLE
    .\scripts\fookie.ps1 docker-up
    .\fookie.ps1 docker-up           # root wrapper forwards here
    .\scripts\fookie.ps1 allinone-build
    .\scripts\fookie.ps1 scale-up -Servers 5 -Workers 10
    .\scripts\fookie.ps1 helm-install -Namespace fookie -Release prod
#>
param(
    [Parameter(Position=0)]
    [string]$Command = "help",

    [string]$Release   = "fookie",
    [string]$Namespace = "default",
    [string]$KubeDir   = "$HOME\.kube",
    [int]$Servers      = 3,
    [int]$Workers      = 5,
    [string]$Schema    = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$Root         = (Get-Location).Path
$ComposeDemo  = @("-f", "demo/docker-compose.yml", "-f", "demo/compose.demo.yml")
$ComposeScale = @("-f", "demo/docker-compose.yml", "-f", "deploy/compose/scale.yml")
$HelmImage    = "alpine/helm:3"
$HelmChart    = "charts/fookie"

# Normalize Windows path for Docker volume mounts (C:\foo -> /c/foo)
function To-DockerPath([string]$p) {
    $p = $p.Replace("\", "/")
    if ($p -match "^([A-Za-z]):(.*)") {
        return "/" + $Matches[1].ToLower() + $Matches[2]
    }
    return $p
}

function Invoke-Helm([string[]]$HelmArgs) {
    $workDir = To-DockerPath $Root
    $kubeDir = To-DockerPath $KubeDir
    $dockerArgs = @(
        "run", "--rm",
        "-v", "${workDir}:/workspace",
        "-w", "/workspace",
        "-v", "${kubeDir}:/root/.kube:ro",
        "-e", "HELM_CACHE_HOME=/tmp/helm-cache",
        $HelmImage
    ) + $HelmArgs
    & docker @dockerArgs
    if ($LASTEXITCODE -ne 0) { throw "Helm command failed (exit $LASTEXITCODE)" }
}

switch ($Command) {

    "help" {
        Write-Host @"
Fookie PowerShell CLI
─────────────────────────────────────────────────
All-in-one image (postgres+redis+server+worker):
  .\fookie.ps1 allinone-build     Build fookiejs/fookie image
  .\fookie.ps1 allinone-run       Run with demo/schema.fql on :8080
  .\fookie.ps1 allinone-run -Schema C:\path\to\schema.fql

Docker (split server+worker):
  .\fookie.ps1 docker-up          Build + start single server/worker
  .\fookie.ps1 docker-down        Stop containers
  .\fookie.ps1 docker-clean       Stop + remove volumes
  .\fookie.ps1 docker-logs        Follow all logs

Scale test (no port conflicts):
  .\fookie.ps1 scale-up           3 servers + 5 workers (default)
  .\fookie.ps1 scale-up -Servers 5 -Workers 10
  .\fookie.ps1 scale-down

Infra only:
  .\fookie.ps1 postgres-up        Start PostgreSQL
  .\fookie.ps1 redis-up           Start Redis
  .\fookie.ps1 infra-down         Stop postgres + redis

Helm (no local Helm — runs via Docker):
  .\fookie.ps1 helm-deps          Download chart dependencies
  .\fookie.ps1 helm-lint          Lint the chart
  .\fookie.ps1 helm-template      Render chart YAML (no cluster needed)
  .\fookie.ps1 helm-install       Install to cluster
  .\fookie.ps1 helm-upgrade       Upgrade (or install) release
  .\fookie.ps1 helm-uninstall     Delete release
  .\fookie.ps1 helm-status        Show release status

Options:
  -Release    Helm release name       (default: fookie)
  -Namespace  Kubernetes namespace    (default: default)
  -KubeDir    kubeconfig directory    (default: ~\.kube)
  -Servers    Server replica count    (default: 3)
  -Workers    Worker replica count    (default: 5)
  -Schema     Schema path for allinone-run
"@
    }

    # ── All-in-one ───────────────────────────────────────────────────────────

    "allinone-build" {
        Write-Host "Building fookiejs/fookie all-in-one image..."
        & docker build -f docker/Dockerfile.allinone -t fookiejs/fookie .
        Write-Host "Done. Run: .\fookie.ps1 allinone-run"
    }

    "allinone-run" {
        $schemaPath = if ($Schema) { $Schema } else { "$Root\demo\schema.fql" }
        $dockerSchema = To-DockerPath $schemaPath
        Write-Host "Starting fookiejs/fookie with schema: $schemaPath"
        & docker run --rm `
            -v "${dockerSchema}:/schema.fql:ro" `
            -p 8080:8080 `
            fookiejs/fookie
    }

    # ── Docker ──────────────────────────────────────────────────────────────

    "docker-up" {
        Write-Host "Building images..."
        & docker compose @ComposeDemo build
        Write-Host "Starting stack..."
        & docker compose @ComposeDemo up -d
        Start-Sleep 3
        & docker compose @ComposeDemo ps
    }

    "docker-down" { & docker compose @ComposeDemo down }
    "docker-clean" { & docker compose @ComposeDemo down -v }
    "docker-logs"  { & docker compose @ComposeDemo logs -f }
    "docker-logs-server" { & docker compose @ComposeDemo logs -f fookie-server }
    "docker-logs-worker"  { & docker compose @ComposeDemo logs -f fookie-worker }

    # ── Infra ────────────────────────────────────────────────────────────────

    "postgres-up" {
        & docker compose -f deploy/compose/postgres.yml up -d postgres
        Write-Host "Postgres → postgres://fookie:fookie_dev@localhost:5432/fookie?sslmode=disable"
    }
    "redis-up" {
        & docker compose -f deploy/compose/postgres.yml up -d redis
        Write-Host "Redis → redis://localhost:6379"
    }
    "infra-down" { & docker compose -f deploy/compose/postgres.yml down }

    # ── Scale ────────────────────────────────────────────────────────────────

    "scale-up" {
        Write-Host "Building images..."
        & docker compose @ComposeDemo build
        Write-Host "Starting $Servers servers + $Workers workers..."
        & docker compose @ComposeScale up -d `
            --scale fookie-server=$Servers `
            --scale fookie-worker=$Workers
        Write-Host ""; & docker compose @ComposeScale ps
        Write-Host "TIP: .\fookie.ps1 docker-logs-worker"
    }

    "scale-down" { & docker compose @ComposeScale down }

    # ── Helm ─────────────────────────────────────────────────────────────────

    "helm-deps" {
        Write-Host "Downloading chart dependencies..."
        Invoke-Helm @("dependency", "update", $HelmChart)
    }
    "helm-lint" {
        & $PSCommandPath helm-deps
        Invoke-Helm @("lint", $HelmChart)
    }
    "helm-template" {
        & $PSCommandPath helm-deps
        Invoke-Helm @("template", $Release, $HelmChart, "--namespace", $Namespace)
    }
    "helm-install" {
        & $PSCommandPath helm-deps
        Write-Host "Installing '$Release' → namespace '$Namespace'..."
        Invoke-Helm @("install", $Release, $HelmChart, "--namespace", $Namespace, "--create-namespace", "--wait")
    }
    "helm-upgrade" {
        & $PSCommandPath helm-deps
        Write-Host "Upgrading '$Release'..."
        Invoke-Helm @("upgrade", "--install", $Release, $HelmChart, "--namespace", $Namespace, "--create-namespace", "--wait")
    }
    "helm-uninstall" { Invoke-Helm @("uninstall", $Release, "--namespace", $Namespace) }
    "helm-status"    { Invoke-Helm @("status",    $Release, "--namespace", $Namespace) }

    default {
        Write-Warning "Unknown command: $Command"
        Write-Host "Run '.\fookie.ps1 help' for available commands."
        exit 1
    }
}
