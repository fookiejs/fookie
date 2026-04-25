/**
 * ExternalServer — HTTP server that receives dispatch calls from fookie.
 *
 * Usage:
 *   import { ExternalServer } from "@fookie/worker";
 *
 *   const server = new ExternalServer({ port: 3001, fookieUrl: "http://localhost:8080" });
 *
 *   server.register("sendEmail", async (input, store) => {
 *     const user = await store.read("User", { id: input.userId });
 *     // ... send email ...
 *     return { sent: true };
 *   });
 *
 *   server.listen();
 *
 * The fookie FQL schema should declare:
 *   external sendEmail {
 *     url: "http://localhost:3001"
 *     ...
 *   }
 */

import http from 'node:http';
import type {
  Store,
  ExternalHandlerFn,
  ExternalInput,
  WorkerCallRequest,
  WorkerCallResponse,
} from './types.js';
import { FookieClient } from './client.js';

export type ExternalServerOptions = {
  /** TCP port to listen on. Default: 3001 */
  port?: number;
  /** Base URL of the fookie GraphQL server (used to build the Store). Default: http://localhost:8080 */
  fookieUrl?: string;
  /** Admin key forwarded to fookie for Store operations. */
  adminKey?: string;
  /** Request timeout in ms. Default: 30_000 */
  timeoutMs?: number;
};

export class ExternalServer {
  private handlers = new Map<string, ExternalHandlerFn>();
  private port: number;
  private fookieUrl: string;
  private adminKey?: string;
  private timeoutMs: number;
  private server: http.Server | null = null;

  constructor(options: ExternalServerOptions = {}) {
    this.port = options.port ?? 3001;
    this.fookieUrl = options.fookieUrl ?? 'http://localhost:8080';
    this.adminKey = options.adminKey;
    this.timeoutMs = options.timeoutMs ?? 30_000;
  }

  /**
   * Register a handler for an external name. The name must match the FQL
   * `external <name> { url: "..." }` declaration exactly.
   */
  register(name: string, handler: ExternalHandlerFn): this {
    this.handlers.set(name, handler);
    return this;
  }

  /** Start listening. Returns a promise that resolves once the server is bound. */
  listen(): Promise<void> {
    return new Promise((resolve, reject) => {
      this.server = http.createServer((req, res) => {
        this.handleRequest(req, res).catch((err) => {
          console.error('[fookie/worker] unhandled error:', err);
          if (!res.headersSent) {
            res.writeHead(500, { 'Content-Type': 'application/json' });
            res.end(JSON.stringify({ error: 'internal server error' }));
          }
        });
      });

      this.server.listen(this.port, () => {
        console.log(`[fookie/worker] listening on :${this.port}`);
        resolve();
      });

      this.server.once('error', reject);
    });
  }

  /** Gracefully stop the server. */
  close(): Promise<void> {
    return new Promise((resolve, reject) => {
      if (!this.server) return resolve();
      this.server.close((err) => (err ? reject(err) : resolve()));
    });
  }

  // ── Private ────────────────────────────────────────────────────────────────

  private async handleRequest(req: http.IncomingMessage, res: http.ServerResponse): Promise<void> {
    // Health check
    if (req.method === 'GET' && req.url === '/health') {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ status: 'ok', handlers: [...this.handlers.keys()] }));
      return;
    }

    // POST /call/:name
    const match = req.url?.match(/^\/call\/([^/?]+)/);
    if (req.method !== 'POST' || !match) {
      res.writeHead(404, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: 'not found' }));
      return;
    }

    const name = decodeURIComponent(match[1]);
    const handler = this.handlers.get(name);
    if (!handler) {
      res.writeHead(404, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: `no handler registered for external "${name}"` }));
      return;
    }

    let body: WorkerCallRequest;
    try {
      body = await readJSON<WorkerCallRequest>(req);
    } catch {
      res.writeHead(400, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: 'invalid JSON body' }));
      return;
    }

    const input: ExternalInput = body.input ?? {};
    const store = this.buildStore();

    let response: WorkerCallResponse;
    try {
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), this.timeoutMs);
      try {
        const result = await handler(input, store);
        response = { result: result ?? {} };
      } finally {
        clearTimeout(timer);
      }
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      console.error(`[fookie/worker] handler "${name}" error:`, msg);
      response = { error: msg };
    }

    const statusCode = 'error' in response && response.error ? 500 : 200;
    res.writeHead(statusCode, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify(response));
  }

  /**
   * Build a Store proxy that calls fookie's GraphQL API.
   * Uses raw GraphQL mutations/queries so the worker doesn't need generated code.
   */
  private buildStore(): Store {
    const client = new FookieClient({
      endpoint: `${this.fookieUrl}/graphql`,
      adminKey: this.adminKey,
    });

    return {
      async read(model, filter = {}) {
        const filterArgs = Object.keys(filter).length ? `(filter: ${jsonToGQLArg(filter)})` : '';
        const fieldName = `all_${toSnake(model)}`;
        const gql = `query { ${fieldName}${filterArgs} { id } }`;
        const res = await client.request<Record<string, unknown[]>>({ query: gql });
        if (res.errors?.length) throw new Error(res.errors[0].message);
        return (res.data?.[fieldName] ?? []) as Record<string, unknown>[];
      },

      async create(model, body) {
        const fieldName = `create_${toSnake(model)}`;
        const gql = `mutation { ${fieldName}(body: ${jsonToGQLArg(body)}) { id } }`;
        const res = await client.request<Record<string, unknown>>({ query: gql });
        if (res.errors?.length) throw new Error(res.errors[0].message);
        return (res.data?.[fieldName] ?? {}) as Record<string, unknown>;
      },

      async update(model, id, body) {
        const fieldName = `update_${toSnake(model)}`;
        const gql = `mutation { ${fieldName}(id: "${id}", body: ${jsonToGQLArg(body)}) { id } }`;
        const res = await client.request<Record<string, unknown>>({ query: gql });
        if (res.errors?.length) throw new Error(res.errors[0].message);
        return (res.data?.[fieldName] ?? {}) as Record<string, unknown>;
      },

      async delete(model, id) {
        const fieldName = `delete_${toSnake(model)}`;
        const gql = `mutation { ${fieldName}(id: "${id}") }`;
        const res = await client.request({ query: gql });
        if (res.errors?.length) throw new Error(res.errors[0].message);
      },
    };
  }
}

// ── Helpers ──────────────────────────────────────────────────────────────────

function readJSON<T>(req: http.IncomingMessage): Promise<T> {
  return new Promise((resolve, reject) => {
    let raw = '';
    req.setEncoding('utf8');
    req.on('data', (chunk) => (raw += chunk));
    req.on('end', () => {
      try {
        resolve(JSON.parse(raw) as T);
      } catch (e) {
        reject(e);
      }
    });
    req.on('error', reject);
  });
}

/** Convert camelCase or PascalCase to snake_case (matches fookie's naming). */
function toSnake(s: string): string {
  return s.replace(/([A-Z])/g, (_, c: string) => `_${c.toLowerCase()}`).replace(/^_/, '');
}

/**
 * Convert a plain JS object to a GraphQL inline argument string.
 * Only handles simple scalar types (string, number, boolean, null).
 * For complex filtering, use the client.request() API directly.
 */
function jsonToGQLArg(obj: unknown): string {
  if (obj === null || obj === undefined) return 'null';
  if (typeof obj === 'boolean') return String(obj);
  if (typeof obj === 'number') return String(obj);
  if (typeof obj === 'string') return JSON.stringify(obj);
  if (Array.isArray(obj)) return `[${obj.map(jsonToGQLArg).join(', ')}]`;
  if (typeof obj === 'object') {
    const entries = Object.entries(obj as Record<string, unknown>)
      .map(([k, v]) => `${k}: ${jsonToGQLArg(v)}`)
      .join(', ');
    return `{${entries}}`;
  }
  return JSON.stringify(obj);
}
