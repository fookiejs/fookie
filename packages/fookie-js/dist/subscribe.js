import { createClient } from 'graphql-ws';
const clientCache = new Map();
function getClient(wsUrl, connectionParams) {
  const key = JSON.stringify({ wsUrl, connectionParams });
  if (!clientCache.has(key)) {
    clientCache.set(
      key,
      createClient({
        url: wsUrl,
        connectionParams,
        retryAttempts: Infinity,
        shouldRetry: () => true,
      }),
    );
  }
  return clientCache.get(key);
}
export function subscribe(opts) {
  const client = getClient(opts.wsUrl, opts.connectionParams);
  const unsubscribe = client.subscribe(
    {
      query: opts.query,
      variables: opts.variables,
    },
    {
      next: (data) => opts.onData(data),
      error: (err) => opts.onError?.(err),
      complete: () => opts.onComplete?.(),
    },
  );
  return unsubscribe;
}
//# sourceMappingURL=subscribe.js.map
