import { FookieClient } from './client.js';
export async function runWorkerLoop(handler, options = {}) {
  const intervalMs = options.intervalMs ?? 1000;
  while (true) {
    try {
      await handler({});
    } catch (err) {
      options.onError?.(err);
    }
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }
}
export function createDefaultClient(adminKey) {
  return new FookieClient({ adminKey });
}
