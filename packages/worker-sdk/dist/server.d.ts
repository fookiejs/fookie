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
import type { ExternalHandlerFn } from "./types.js";
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
export declare class ExternalServer {
    private handlers;
    private port;
    private fookieUrl;
    private adminKey?;
    private timeoutMs;
    private server;
    constructor(options?: ExternalServerOptions);
    /**
     * Register a handler for an external name. The name must match the FQL
     * `external <name> { url: "..." }` declaration exactly.
     */
    register(name: string, handler: ExternalHandlerFn): this;
    /** Start listening. Returns a promise that resolves once the server is bound. */
    listen(): Promise<void>;
    /** Gracefully stop the server. */
    close(): Promise<void>;
    private handleRequest;
    /**
     * Build a Store proxy that calls fookie's GraphQL API.
     * Uses raw GraphQL mutations/queries so the worker doesn't need generated code.
     */
    private buildStore;
}
