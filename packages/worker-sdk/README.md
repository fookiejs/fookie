# `@fookie/worker`

Node.js external handler SDK for the Fookie framework.

Register TypeScript/JavaScript functions as fookie `external` handlers.
Fookie dispatches calls to your HTTP server — no polling needed.

## Install

```bash
npm install @fookie/worker
```

## Quick start

**1. Declare in FQL:**
```fql
external sendEmail {
  url: "http://localhost:3001"
  retry: 5
  retry_backoff: exponential

  body {
    userId   id
    subject  string
  }
  output {
    sent boolean
  }
}
```

**2. Implement in Node.js:**
```ts
import { ExternalServer } from "@fookie/worker";

const server = new ExternalServer({
  port: 3001,
  fookieUrl: "http://localhost:8080",
  adminKey: process.env.FOOKEE_ADMIN_KEY,
});

server.register("sendEmail", async (input, store) => {
  // input = { userId: "...", subject: "..." }
  const [user] = await store.read("User", { id: input.userId });
  await sendMail(user.email as string, input.subject as string);
  return { sent: true };
});

await server.listen();
// [fookie/worker] listening on :3001
```

## Protocol

Fookie calls `POST {url}/call/{name}` with:
```json
{ "input": { "key": "value" } }
```

Your server must respond with:
```json
{ "result": { ... } }
```
or on error:
```json
{ "error": "something went wrong" }
```

## Health check

`GET /health` returns:
```json
{ "status": "ok", "handlers": ["sendEmail", "..."] }
```

## Store API

The `store` argument in your handler lets you call back into fookie:

```ts
store.read("User", { status: "active" })        // → User[]
store.create("Order", { userId, amount })        // → Order
store.update("Order", id, { status: "paid" })    // → Order
store.delete("Order", id)                        // → void
```

## Low-level GraphQL client

```ts
import { FookieClient } from "@fookie/worker";

const client = new FookieClient({
  endpoint: "http://localhost:8080/graphql",
  adminKey: "...",
});

const res = await client.request({ query: "{ all_user { id name } }" });
```
