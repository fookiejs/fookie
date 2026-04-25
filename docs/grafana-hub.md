# Grafana — Fookie'nin Merkezi Bilgi Hub'ı

Grafana, Fookie cluster'ının **tek gözlem/veri merkezi**dir. Metric, log, trace yanında PostgreSQL verisine ve GraphQL API'sine doğrudan buradan erişirsin — ayrı bir DB client veya GraphQL Playground gerekmez.

## 2 Dakikada Deployment

```bash
git clone <repo> && cd fookie
./fookie.sh docker-up              # postgres + redis + fookie-server + worker + observability
./fookie.sh grafana-bootstrap      # Grafana için read-only PG user (tek kere)
./fookie.sh hub                    # Tarayıcıda http://localhost:3000
```

PowerShell:
```powershell
.\fookie.ps1 docker-up
.\fookie.ps1 grafana-bootstrap
.\fookie.ps1 hub
```

## Dashboard'lar

Hepsi provision edilmiş, manuel import gerekmez:

| Dashboard | URL | Amaç |
|-----------|-----|------|
| **Genel Bakış** | `/d/fookie-home` | Ops/s, error rate, p95, aktif model — hızlı nabız |
| **Cluster Operations** | `/d/fookie-cluster-ops` | Servis up/down, replica sayısı, container CPU/mem |
| **Data Hub** | `/d/fookie-data-hub` | DB tablo boyutları, row count, outbox backlog, canlı `SELECT` |
| **GraphQL Explorer** | `/d/fookie-graphql` | Grafana içinden GraphQL sorgu — introspection, custom query |
| **Database Health** | `/d/fookie-db-health` | PG connection pool, transactions/s, cache hit, deadlocks |
| **Redis Health** | `/d/fookie-redis-health` | Memory, ops/s, keyspace hit, pub/sub, evictions |
| **Models** | `/d/fookie-models` | Model bazlı ops/s & latency (mevcut) |
| **Traces** | `/d/fookie-traces` | TraceQL + trace→log sıçrama (mevcut) |

## Data Source'lar

| Name | Tip | Kullanım |
|------|-----|----------|
| `Prometheus` | prometheus | Metric'ler |
| `Loki` | loki | Log'lar (default) |
| `Tempo` | tempo | Trace'ler (→ Loki bridge) |
| `FookieDB` | postgres | Business veri (read-only `grafana_ro` user) |
| `FookieGraphQL` | infinity | GraphQL query'leri — POST to `/graphql` |

## Exporter'lar

Prometheus bu kaynakları scrape eder:

- `fookie-server:8080/metrics` — GraphQL + executor metric'leri
- `fookie-worker:9091/metrics` — outbox/worker metric'leri
- `otel-collector:8889` — OTel span metrics
- `postgres-exporter:9187` — PG stat
- `redis-exporter:9121` — Redis info
- `cadvisor:8080` — container CPU/mem

## Alert'ler

`deploy/compose/config/prometheus-alerts.yml`:

- `FookieServerDown` (critical, 1m)
- `FookieWorkerDown` (warning, 2m)
- `HighErrorRate` (> 5% 5m)
- `DBConnectionPoolExhausted` (> 90% 3m)
- `OutboxBacklog` (> 10k 5m)
- `RedisMemoryHigh` (> 85% 5m)

Slack/webhook bağlamak için Grafana UI → Alerting → Contact points.

## Kubernetes (Helm)

```bash
./fookie.sh helm-install   # observability.enabled=true default
```

`charts/fookie/values.yaml`'da:
- `observability.enabled: true` → kube-prometheus-stack subchart (Prometheus + Grafana + kube-state-metrics)
- `observability.logs.enabled: false` → loki-stack opsiyonel
- ServiceMonitor / PodMonitor'lar otomatik devreye girer

Dashboard ConfigMap'leri `grafana_dashboard: "1"` label'ı ile Grafana sidecar tarafından otomatik yüklenir. Chart'ta `charts/fookie/files/dashboards/*.json` olarak paketlenir.

## Retention

| Stack | Retention | Config |
|-------|-----------|--------|
| Prometheus | 30d | `--storage.tsdb.retention.time=30d` |
| Loki | default (7d) | `loki-config.yaml` |
| Tempo | default | `tempo.yaml` |

## Production Notları

- `GF_AUTH_ANONYMOUS_ENABLED: false` yap, admin password env'den
- `grafana_ro` password'ünü rotate et (bootstrap SQL düzenle)
- Persistent volume'lar zaten set (Docker named volume, Helm PVC subchart'tan)
- `observability.enabled=false` ile central bir Grafana cluster'a ServiceMonitor yönlendirilebilir
