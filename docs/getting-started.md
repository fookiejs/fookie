# Getting Started with Fookie

> **5-minute quickstart**: write a schema → migrate → serve → query.

---

## Prerequisites

| Tool | Version |
|------|---------|
| Go | ≥ 1.22 |
| PostgreSQL | ≥ 14 |
| Redis (optional) | ≥ 7 |
| Node.js (optional, for workers) | ≥ 18 |

---

## 1. Install the CLI

```bash
go install github.com/fookiejs/fookie/cmd/fookie@latest
go install github.com/fookiejs/fookie/cmd/server@latest
```

Check everything is wired up:

```bash
fookie doctor
# [✓] go          go version go1.22.x ...
# [✓] docker      v26.x
# [✓] fookie-server  found in PATH
```

---

## 2. Create a new project

```bash
fookie init myapp
cd myapp
```

This creates:
```
myapp/
  schema.fql   # your data model
  .env         # DB_URL, REDIS_URL, etc.
```

---

## 3. Write your schema (`schema.fql`)

```fql
model User {
  fields {
    name       string
    email      string --unique
    role       string
    created_at timestamp --index desc
  }

  create {}
  read   {}
  update {}
  delete {}   // soft-delete (deleted_at is set, row is never removed)
}

model Post {
  fields {
    title      string
    body       string
    author     id     --index
    created_at timestamp
  }

  @@index([author, created_at DESC])

  create {}
  read   {}
  update {}
  delete {}
}

// External job with retry policy
external sendWelcomeEmail {
  retry: 5
  retry_backoff: exponential
  retry_max_delay: 60

  body {
    userId id
  }
  output {
    sent boolean
  }
}
```

---

## 4. Migrate the database

```bash
# Preview what SQL will be run:
fookie migrate plan --schema schema.fql --db "$DB_URL"

# Apply the changes:
fookie migrate apply --schema schema.fql --db "$DB_URL"
# ✓ Applied 12 statement(s) (label: manual-20240425-103000)
```

Migration history is stored in the `schema_migrations` table.

---

## 5. Start the server

```bash
fookie serve --schema schema.fql --db "$DB_URL"
# or use the server binary directly:
fookie-server --schema schema.fql --db "$DB_URL" --port :8080
```

Visit **http://localhost:8080/graphql** for GraphiQL.

---

## 6. Query via GraphQL

```graphql
# Create a user
mutation {
  create_user(body: { name: "Alice", email: "alice@example.com", role: "admin" }) {
    id
    name
    email
  }
}

# List with cursor pagination
query {
  list_user(connection: { first: 10 }) {
    edges {
      node { id name email }
      cursor
    }
    pageInfo {
      hasNextPage
      endCursor
      totalCount
    }
  }
}

# Fetch next page
query {
  list_user(connection: { first: 10, after: "<endCursor from above>" }) {
    edges { node { id name } cursor }
    pageInfo { hasNextPage endCursor }
  }
}

# Soft-delete a user (sets deleted_at, row stays in DB)
mutation {
  delete_user(id: "<id>")
}

# Restore a soft-deleted user
mutation {
  restore_user(id: "<id>")
}
```

---

## 7. External jobs (background tasks)

Fookie uses an **outbox pattern** — jobs are written to the `outbox` table atomically with your mutation and processed asynchronously.

### Go handler

```go
executor.ExternalManager().Register("sendWelcomeEmail", func(ctx context.Context, input map[string]interface{}, store runtime.Store) (map[string]interface{}, error) {
    userID := input["userId"].(string)
    // ... send email ...
    return map[string]interface{}{"sent": true}, nil
})
```

### Node.js handler (via `@fookie/worker`)

```bash
npm install @fookie/worker
```

```ts
import { ExternalServer } from "@fookie/worker";

const server = new ExternalServer({
  port: 3001,
  fookieUrl: "http://localhost:8080",
});

server.register("sendWelcomeEmail", async (input, store) => {
  const [user] = await store.read("User", { id: input.userId });
  await sendEmail(user.email, "Welcome!");
  return { sent: true };
});

await server.listen();
```

Declare the URL in FQL:
```fql
external sendWelcomeEmail {
  url: "http://localhost:3001"
  ...
}
```

---

## 8. Dead-letter queue

View and retry failed jobs:

```bash
# List failed jobs
fookie dlq list --db "$DB_URL"

# Retry a specific job
fookie dlq retry <id> --db "$DB_URL"

# Retry all failed jobs
fookie dlq retry-all --db "$DB_URL"

# Purge old failures (default: 30 days)
fookie dlq purge --db "$DB_URL" --before 2024-01-01
```

---

## 9. Observability (optional)

Start the full observability stack (Grafana, Prometheus, Loki, Tempo):

```bash
docker compose --profile observability up -d
```

Open **http://localhost:3000** for Grafana dashboards:
- **Fookie Home** — cluster overview
- **Background Jobs** — outbox queue, DLQ, retry stats, saga status
- **Database Health** — connections, cache hit ratio, table sizes
- **Redis Health** — memory, ops/s, evictions
- **Cluster Operations** — service health, CPU/mem

---

## 10. Deploying to Kubernetes

```bash
helm repo add fookie https://fookiejs.github.io/fookie/charts
helm install my-app fookie/fookie \
  --set schema.content="$(cat schema.fql)" \
  --set postgresql.externalUrl="postgres://..." \
  --set redis.externalUrl="redis://..."
```

---

## CLI reference

```
fookie init <dir>                    scaffold schema.fql + .env
fookie doctor                        check required tools
fookie serve [flags]                 start the fookie-server binary
fookie migrate plan   [flags]        show pending DDL
fookie migrate apply  [flags]        apply DDL + record in schema_migrations
fookie migrate history [flags]       show applied migrations
fookie dlq list       [flags]        list failed outbox items
fookie dlq retry <id> [flags]        re-queue one failed item
fookie dlq retry-all  [flags]        re-queue all failed items
fookie dlq purge      [flags]        delete old failed items

Common flags:
  --schema <path>   path to .fql file or directory (env: SCHEMA_PATH)
  --db     <url>    PostgreSQL connection string   (env: DB_URL)
```
