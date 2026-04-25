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
import http from "node:http";
import { FookieClient } from "./client.js";
export class ExternalServer {
    handlers = new Map();
    port;
    fookieUrl;
    adminKey;
    timeoutMs;
    server = null;
    constructor(options = {}) {
        this.port = options.port ?? 3001;
        this.fookieUrl = options.fookieUrl ?? "http://localhost:8080";
        this.adminKey = options.adminKey;
        this.timeoutMs = options.timeoutMs ?? 30_000;
    }
    /**
     * Register a handler for an external name. The name must match the FQL
     * `external <name> { url: "..." }` declaration exactly.
     */
    register(name, handler) {
        this.handlers.set(name, handler);
        return this;
    }
    /** Start listening. Returns a promise that resolves once the server is bound. */
    listen() {
        return new Promise((resolve, reject) => {
            this.server = http.createServer((req, res) => {
                this.handleRequest(req, res).catch((err) => {
                    console.error("[fookie/worker] unhandled error:", err);
                    if (!res.headersSent) {
                        res.writeHead(500, { "Content-Type": "application/json" });
                        res.end(JSON.stringify({ error: "internal server error" }));
                    }
                });
            });
            this.server.listen(this.port, () => {
                console.log(`[fookie/worker] listening on :${this.port}`);
                resolve();
            });
            this.server.once("error", reject);
        });
    }
    /** Gracefully stop the server. */
    close() {
        return new Promise((resolve, reject) => {
            if (!this.server)
                return resolve();
            this.server.close((err) => (err ? reject(err) : resolve()));
        });
    }
    // ── Private ────────────────────────────────────────────────────────────────
    async handleRequest(req, res) {
        // Health check
        if (req.method === "GET" && req.url === "/health") {
            res.writeHead(200, { "Content-Type": "application/json" });
            res.end(JSON.stringify({ status: "ok", handlers: [...this.handlers.keys()] }));
            return;
        }
        // POST /call/:name
        const match = req.url?.match(/^\/call\/([^/?]+)/);
        if (req.method !== "POST" || !match) {
            res.writeHead(404, { "Content-Type": "application/json" });
            res.end(JSON.stringify({ error: "not found" }));
            return;
        }
        const name = decodeURIComponent(match[1]);
        const handler = this.handlers.get(name);
        if (!handler) {
            res.writeHead(404, { "Content-Type": "application/json" });
            res.end(JSON.stringify({ error: `no handler registered for external "${name}"` }));
            return;
        }
        let body;
        try {
            body = await readJSON(req);
        }
        catch {
            res.writeHead(400, { "Content-Type": "application/json" });
            res.end(JSON.stringify({ error: "invalid JSON body" }));
            return;
        }
        const input = body.input ?? {};
        const store = this.buildStore();
        let response;
        try {
            const controller = new AbortController();
            const timer = setTimeout(() => controller.abort(), this.timeoutMs);
            try {
                const result = await handler(input, store);
                response = { result: result ?? {} };
            }
            finally {
                clearTimeout(timer);
            }
        }
        catch (err) {
            const msg = err instanceof Error ? err.message : String(err);
            console.error(`[fookie/worker] handler "${name}" error:`, msg);
            response = { error: msg };
        }
        const statusCode = "error" in response && response.error ? 500 : 200;
        res.writeHead(statusCode, { "Content-Type": "application/json" });
        res.end(JSON.stringify(response));
    }
    /**
     * Build a Store proxy that calls fookie's GraphQL API.
     * Uses raw GraphQL mutations/queries so the worker doesn't need generated code.
     */
    buildStore() {
        const client = new FookieClient({
            endpoint: `${this.fookieUrl}/graphql`,
            adminKey: this.adminKey,
        });
        return {
            async read(model, filter = {}) {
                const filterArgs = Object.keys(filter).length
                    ? `(filter: ${jsonToGQLArg(filter)})`
                    : "";
                const fieldName = `all_${toSnake(model)}`;
                const gql = `query { ${fieldName}${filterArgs} { id } }`;
                const res = await client.request({ query: gql });
                if (res.errors?.length)
                    throw new Error(res.errors[0].message);
                return (res.data?.[fieldName] ?? []);
            },
            async create(model, body) {
                const fieldName = `create_${toSnake(model)}`;
                const gql = `mutation { ${fieldName}(body: ${jsonToGQLArg(body)}) { id } }`;
                const res = await client.request({ query: gql });
                if (res.errors?.length)
                    throw new Error(res.errors[0].message);
                return (res.data?.[fieldName] ?? {});
            },
            async update(model, id, body) {
                const fieldName = `update_${toSnake(model)}`;
                const gql = `mutation { ${fieldName}(id: "${id}", body: ${jsonToGQLArg(body)}) { id } }`;
                const res = await client.request({ query: gql });
                if (res.errors?.length)
                    throw new Error(res.errors[0].message);
                return (res.data?.[fieldName] ?? {});
            },
            async delete(model, id) {
                const fieldName = `delete_${toSnake(model)}`;
                const gql = `mutation { ${fieldName}(id: "${id}") }`;
                const res = await client.request({ query: gql });
                if (res.errors?.length)
                    throw new Error(res.errors[0].message);
            },
        };
    }
}
// ── Helpers ──────────────────────────────────────────────────────────────────
function readJSON(req) {
    return new Promise((resolve, reject) => {
        let raw = "";
        req.setEncoding("utf8");
        req.on("data", (chunk) => (raw += chunk));
        req.on("end", () => {
            try {
                resolve(JSON.parse(raw));
            }
            catch (e) {
                reject(e);
            }
        });
        req.on("error", reject);
    });
}
/** Convert camelCase or PascalCase to snake_case (matches fookie's naming). */
function toSnake(s) {
    return s
        .replace(/([A-Z])/g, (_, c) => `_${c.toLowerCase()}`)
        .replace(/^_/, "");
}
/**
 * Convert a plain JS object to a GraphQL inline argument string.
 * Only handles simple scalar types (string, number, boolean, null).
 * For complex filtering, use the client.request() API directly.
 */
function jsonToGQLArg(obj) {
    if (obj === null || obj === undefined)
        return "null";
    if (typeof obj === "boolean")
        return String(obj);
    if (typeof obj === "number")
        return String(obj);
    if (typeof obj === "string")
        return JSON.stringify(obj);
    if (Array.isArray(obj))
        return `[${obj.map(jsonToGQLArg).join(", ")}]`;
    if (typeof obj === "object") {
        const entries = Object.entries(obj)
            .map(([k, v]) => `${k}: ${jsonToGQLArg(v)}`)
            .join(", ");
        return `{${entries}}`;
    }
    return JSON.stringify(obj);
}
