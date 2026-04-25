-- grafana-bootstrap.sql
-- Creates a read-only `grafana_ro` role for the Grafana PostgreSQL data source.
-- Idempotent: safe to run multiple times.
--
-- Apply:
--   docker compose -f deploy/compose/postgres.yml exec -T postgres \
--     psql -U fookie -d fookie < scripts/grafana-bootstrap.sql

DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'grafana_ro') THEN
    CREATE ROLE grafana_ro LOGIN PASSWORD 'grafana_ro_dev';
  END IF;
END
$$;

GRANT CONNECT ON DATABASE fookie TO grafana_ro;
GRANT USAGE ON SCHEMA public TO grafana_ro;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO grafana_ro;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT SELECT ON TABLES TO grafana_ro;

-- Stats catalog (for database-health dashboard)
GRANT pg_read_all_stats TO grafana_ro;
