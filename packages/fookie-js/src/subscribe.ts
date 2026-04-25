import { createClient, type Client } from 'graphql-ws'
import type { GraphQLResponse } from './client.js'

// Single shared client per (wsUrl + connectionParams) combo.
// Keyed by JSON-serialised options so different credentials get separate connections.
const clientCache = new Map<string, Client>()

function getClient(
  wsUrl: string,
  connectionParams: Record<string, string>,
): Client {
  const key = JSON.stringify({ wsUrl, connectionParams })
  if (!clientCache.has(key)) {
    clientCache.set(
      key,
      createClient({
        url: wsUrl,
        connectionParams,
        // Auto-reconnect with exponential back-off
        retryAttempts: Infinity,
        shouldRetry: () => true,
      }),
    )
  }
  return clientCache.get(key)!
}

export interface SubscribeOptions {
  wsUrl: string
  connectionParams: Record<string, string>
  query: string
  variables?: Record<string, unknown>
  onData: (response: GraphQLResponse) => void
  onError?: (error: unknown) => void
  onComplete?: () => void
}

/**
 * Subscribe to a GraphQL subscription using the graphql-transport-ws protocol.
 * Returns an `unsubscribe` function.
 *
 * Reuses a single WebSocket connection per (wsUrl + credentials) combo.
 */
export function subscribe(opts: SubscribeOptions): () => void {
  const client = getClient(opts.wsUrl, opts.connectionParams)

  const unsubscribe = client.subscribe<GraphQLResponse>(
    {
      query: opts.query,
      variables: opts.variables,
    },
    {
      next: (data) => opts.onData(data as GraphQLResponse),
      error: (err) => opts.onError?.(err),
      complete: () => opts.onComplete?.(),
    },
  )

  return unsubscribe
}
