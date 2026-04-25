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
    [Parameter(Position=1)]
    [ValidateSet("full","minimal")]
    [string]$Profile = "full",

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
$ComposeFull  = @("-f", "demo/docker-compose.yml", "-f", "demo/compose.demo.yml")
$ComposeMin   = @("-f", "demo/docker-compose.minimal.yml", "-f", "demo/compose.demo.yml")
$ComposeScaleFull = @("-f", "demo/docker-compose.yml", "-f", "deploy/compose/scale.yml")
$ComposeScaleMin  = @("-f", "demo/docker-compose.minimal.yml", "-f", "deploy/compose/scale.yml")
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

function Resolve-ComposeArgs {
    if ($Profile -eq "minimal") { return $ComposeMin }
    return $ComposeFull
}

function Resolve-ScaleComposeArgs {
    if ($Profile -eq "minimal") { return $ComposeScaleMin }
    return $ComposeScaleFull
}

switch ($Command) {

    "help" {
        Write-Host @"
Fookie PowerShell CLI
─────────────────────────────────────────────────
Primary commands:
  .\fookie.ps1 start [full|minimal]
  .\fookie.ps1 stop [full|minimal]
  .\fookie.ps1 status [full|minimal]
  .\fookie.ps1 logs [full|minimal]

All-in-one image (postgres+redis+server+worker):
  .\fookie.ps1 allinone-build     Build fookiejs/fookie image
  .\fookie.ps1 allinone-run       Run with demo/schema.fql on :8080
  .\fookie.ps1 allinone-run -Schema C:\path\to\schema.fql

Docker (split server+worker):
  .\fookie.ps1 docker-up          Build + start selected profile
  .\fookie.ps1 docker-down        Stop selected profile
  .\fookie.ps1 docker-status      Show selected profile status
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

Grafana hub (tüm cluster'ın bilgi merkezi):
  .\fookie.ps1 observability-up    Observability stack başlat
  .\fookie.ps1 observability-down  Stack'i durdur
  .\fookie.ps1 hub                 Grafana'yı tarayıcıda aç
  .\fookie.ps1 grafana-bootstrap   grafana_ro read-only PG user oluştur

Helm (no local Helm — runs via Docker):
  .\fookie.ps1 helm-deps          Download chart dependencies
  .\fookie.ps1 helm-lint          Lint the chart
  .\fookie.ps1 helm-template      Render chart YAML (no cluster needed)
  .\fookie.ps1 helm-install       Install to cluster
  .\fookie.ps1 helm-upgrade       Upgrade (or install) release
  .\fookie.ps1 helm-uninstall     Delete release
  .\fookie.ps1 helm-status        Show release status

Options:
  -Profile    full|minimal            (default: full)
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

    "start" { $Command = "docker-up" ; & $PSCommandPath $Command -Profile $Profile -Release $Release -Namespace $Namespace -KubeDir $KubeDir -Servers $Servers -Workers $Workers -Schema $Schema; return }
    "stop" { $Command = "docker-down" ; & $PSCommandPath $Command -Profile $Profile -Release $Release -Namespace $Namespace -KubeDir $KubeDir -Servers $Servers -Workers $Workers -Schema $Schema; return }
    "status" { $Command = "docker-status" ; & $PSCommandPath $Command -Profile $Profile -Release $Release -Namespace $Namespace -KubeDir $KubeDir -Servers $Servers -Workers $Workers -Schema $Schema; return }
    "logs" { $Command = "docker-logs" ; & $PSCommandPath $Command -Profile $Profile -Release $Release -Namespace $Namespace -KubeDir $KubeDir -Servers $Servers -Workers $Workers -Schema $Schema; return }

    "docker-up" {
        $compose = Resolve-ComposeArgs
        Write-Host "Building images..."
        & docker compose @compose build
        Write-Host "Starting stack..."
        & docker compose @compose up -d
        Start-Sleep 3
        & docker compose @compose ps
    }

    "docker-down" { $compose = Resolve-ComposeArgs; & docker compose @compose down }
    "docker-status" { $compose = Resolve-ComposeArgs; & docker compose @compose ps }
    "docker-clean" { $compose = Resolve-ComposeArgs; & docker compose @compose down -v }
    "docker-logs"  { $compose = Resolve-ComposeArgs; & docker compose @compose logs -f }
    "docker-logs-server" { $compose = Resolve-ComposeArgs; & docker compose @compose logs -f fookie-server }
    "docker-logs-worker"  { $compose = Resolve-ComposeArgs; & docker compose @compose logs -f fookie-worker }

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
        $compose = Resolve-ComposeArgs
        $composeScale = Resolve-ScaleComposeArgs
        Write-Host "Building images..."
        & docker compose @compose build
        Write-Host "Starting $Servers servers + $Workers workers..."
        & docker compose @composeScale up -d `
            --scale fookie-server=$Servers `
            --scale fookie-worker=$Workers
        Write-Host ""; & docker compose @composeScale ps
        Write-Host "TIP: .\fookie.ps1 docker-logs-worker"
    }

    "scale-down" { $composeScale = Resolve-ScaleComposeArgs; & docker compose @composeScale down }

    # ── Grafana Hub ──────────────────────────────────────────────────────────

    "observability-up" {
        Write-Host "Starting observability stack (Prometheus + Loki + Tempo + Grafana + exporters)..."
        & docker compose -f deploy/compose/postgres.yml -f deploy/compose/observability.yml up -d
        Start-Sleep 3
        Write-Host ""
        Write-Host "Grafana        -> http://localhost:3000   (anonymous admin)"
        Write-Host "Prometheus     -> http://localhost:9090"
        Write-Host "Loki           -> http://localhost:3100"
        Write-Host "Tempo          -> http://localhost:3200"
        Write-Host "cAdvisor       -> http://localhost:8088"
        Write-Host ""
        Write-Host "Bootstrap grafana_ro PG user:"
        Write-Host "  .\fookie.ps1 grafana-bootstrap"
    }

    "observability-down" {
        & docker compose -f deploy/compose/postgres.yml -f deploy/compose/observability.yml down
    }

    "hub" {
        Start-Process "http://localhost:3000"
    }

    "grafana-bootstrap" {
        Write-Host "Creating read-only grafana_ro PG user..."
        Get-Content scripts/grafana-bootstrap.sql | & docker compose -f deploy/compose/postgres.yml exec -T postgres psql -U fookie -d fookie
        Write-Host "Done. Grafana PostgreSQL datasource now works."
    }

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
