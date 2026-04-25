/**
 * @fookie/types — Shared type definitions for Fookie client and worker packages
 */
export interface GraphQLRequest {
    query: string;
    variables?: Record<string, unknown>;
}
export interface GraphQLResponse<T = unknown> {
    data?: T;
    errors?: Array<{
        message: string;
        locations?: unknown;
        path?: unknown;
    }>;
}
export interface FookieClientOptions {
    url: string;
    wsUrl?: string;
    adminKey?: string;
    token?: string;
}
/** Input payload passed to an external handler from fookie. */
export type ExternalInput = Record<string, unknown>;
/** Result returned by an external handler. */
export type ExternalResult = Record<string, unknown>;
/** Context passed to external handlers (signals, abort, etc). */
export interface WorkerContext {
    signal?: AbortSignal;
}
/**
 * Store gives handlers access to fookie's CRUD operations via the GraphQL API.
 * Mirrors the Go Store interface so handlers have a familiar shape.
 */
export interface Store {
    read(model: string, filter?: Record<string, unknown>): Promise<ExternalResult[]>;
    create(model: string, body: Record<string, unknown>): Promise<ExternalResult>;
    update(model: string, id: string, body: Record<string, unknown>): Promise<ExternalResult>;
    delete(model: string, id: string): Promise<void>;
}
/** The function signature for an external handler. */
export type ExternalHandlerFn = (input: ExternalInput, store: Store) => Promise<ExternalResult>;
/**
 * Shape of the request body that fookie sends to POST /call/:name
 */
export interface ExternalCallRequest {
    id: string;
    model: string;
    operation: string;
    payload: ExternalInput;
    context: WorkerContext;
}
/**
 * Response shape that an external handler should return.
 */
export interface ExternalCallResponse {
    result?: ExternalResult;
    error?: string;
}
export declare class FookieError extends Error {
    code?: string | undefined;
    statusCode: number;
    constructor(message: string, code?: string | undefined, statusCode?: number);
}
export interface FookieServerConfig {
    schemaPath: string;
    dbUrl: string;
    port?: string;
    metricsListen?: string;
    serviceName?: string;
    otlpEndpoint?: string;
}
export interface FookieWorkerConfig {
    schemaPath: string;
    dbUrl: string;
    pollInterval?: number;
    metricsListen?: string;
    serviceName?: string;
    otlpEndpoint?: string;
    traceSampleRate?: number;
}
//# sourceMappingURL=index.d.ts.map