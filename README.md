# Fookie

Fookie is the Go runtime and query engine behind the Fookie ecosystem.  
You define data models and behavior with FQL, and Fookie serves a GraphQL API, runs migrations, and executes background jobs.

This repository is currently in an early stage (alpha) and is evolving quickly. Core workflows (schema, migrate, serve, jobs, Docker stack) are usable, while APIs and ergonomics may still change.

## Quick Start (Docker-first)

Prerequisites:
- Docker + Docker Compose

From the repository root:

```bash
docker compose up -d
```

Then open:
- GraphQL (GraphiQL): `http://localhost:8080/graphql`
- Grafana: `http://localhost:3000`

## Quick Start (CLI)

Prerequisites:
- Go `>= 1.25`
- PostgreSQL

Install CLI tools:

```bash
go install github.com/fookiejs/fookie/cmd/fookie@latest
go install github.com/fookiejs/fookie/cmd/server@latest
```

Check environment:

```bash
fookie doctor
```

Plan and apply migrations:

```bash
fookie migrate plan --schema schema.fql --db "$DB_URL"
fookie migrate apply --schema schema.fql --db "$DB_URL"
```

Start the server:

```bash
fookie serve --schema schema.fql --db "$DB_URL" --port :8080
```

## FQL in 60 Seconds

```fql
model User {
  fields {
    name  string
    email string
    role  string
  }

  @@unique([email], where: "deleted_at IS NULL")

  create {}
  read   {}
  update {}
  delete {}
}
```

With a schema like this, Fookie generates GraphQL CRUD operations and migration-ready database structure from the model definition.

## Core Capabilities

- FQL-based schema/model definition
- GraphQL endpoint with generated operations
- Migration planning and apply workflow (`fookie migrate`)
- Dead-letter queue tooling (`fookie dlq list|retry|retry-all|purge`)
- Background/external job execution pattern (outbox-style)
- Built-in observability stack (Prometheus, Loki, Tempo, Grafana)
- Kubernetes deployment support via Helm chart

## Repository Map

- `pkg/` - core engine packages (parser, compiler, runtime, schema, telemetry, etc.)
- `cmd/` - executable entrypoints (`fookie`, `server`, `worker`, `parser`)
- `charts/fookie/` - Helm chart for Kubernetes deployment
- `deploy/compose/` - compose fragments and platform deployment configs
- `docs/` - project docs (quick start, roadmap, platform notes)
- `tools/fql-language/` - FQL language tooling/editor support
- `packages/` - JavaScript/TypeScript packages (`fookie-js`, worker SDK, types)
- `tests/` - unit and integration tests

## Documentation

- [Getting started](docs/getting-started.md)
- [Ecosystem roadmap](docs/ecosystem-roadmap.md)
- [Grafana dashboard setup](docs/grafana-hub.md)
- [Platform launcher notes](docs/platform-launcher.md)

## Roadmap Snapshot

Now:
- Stabilize core parser/compiler/runtime flow
- Improve onboarding and production-readiness docs

Next:
- Strengthen CRUD and relational workflows
- Expand test coverage for parser/compiler/runtime paths

Later:
- Broader ecosystem integrations and developer tooling maturity

## Contributing

Local checks commonly used in this repo:

```bash
make test
make test-unit
make test-integration
npm run lint
npm run test
```

If you want to contribute, open an issue describing the problem/proposal first, then submit a focused PR with tests where applicable.

## License

[MIT](LICENSE)
