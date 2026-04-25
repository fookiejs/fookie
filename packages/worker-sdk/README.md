# `@fookie/worker-sdk`

Minimal TypeScript SDK for building Fookie workers and automation jobs.

## Install

```bash
npm install @fookie/worker-sdk
```

## Usage

```ts
import { createDefaultClient, runWorkerLoop } from "@fookie/worker-sdk";

const client = createDefaultClient("change_me_local_admin");

await runWorkerLoop(async () => {
  await client.request({
    query: "{ all_bank_user { id display_name } }"
  });
}, { intervalMs: 2000 });
```
