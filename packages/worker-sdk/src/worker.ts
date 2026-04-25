import { FookieClient } from './client.js';
import type { WorkerContext } from './types.js';

export type WorkerHandler = (ctx: WorkerContext) => Promise<void>;

export type WorkerLoopOptions = {
  intervalMs?: number;
  onError?: (error: unknown) => void;
};

export async function runWorkerLoop(
  handler: WorkerHandler,
  options: WorkerLoopOptions = {},
): Promise<never> {
  const intervalMs = options.intervalMs ?? 1000;
  // eslint-disable-next-line no-constant-condition
  while (true) {
    try {
      await handler({});
    } catch (err) {
      options.onError?.(err);
    }
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }
}

export function createDefaultClient(adminKey?: string): FookieClient {
  return new FookieClient({ adminKey });
}
