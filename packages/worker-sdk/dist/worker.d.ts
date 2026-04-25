import { FookieClient } from "./client.js";
import type { WorkerContext } from "./types.js";
export type WorkerHandler = (ctx: WorkerContext) => Promise<void>;
export type WorkerLoopOptions = {
    intervalMs?: number;
    onError?: (error: unknown) => void;
};
export declare function runWorkerLoop(handler: WorkerHandler, options?: WorkerLoopOptions): Promise<never>;
export declare function createDefaultClient(adminKey?: string): FookieClient;
