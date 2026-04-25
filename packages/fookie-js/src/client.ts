import { subscribe as wsSubscribe } from './subscribe.js'

export interface FookieOptions {
  /** HTTP endpoint, e.g. "http://localhost:8080/graphql" */
  url: string
  /**
   * WebSocket endpoint, e.g. "ws://localhost:8080/graphql/ws"
   * Auto-derived from `url` if omitted (http→ws, https→wss, appends /ws).
   */
  wsUrl?: string
  /** Admin key for privileged operations (X-Fookie-Admin-Key header) */
  adminKey?: string
  /** Bearer token for authenticated operations */
  token?: string
}

export interface GraphQLResponse<T = unknown> {
  data?: T
  errors?: Array<{ message: string; locations?: unknown; path?: unknown }>
}

export class FookieClient {
  private readonly url: string
  private readonly wsUrl: string
  private readonly headers: Record<string, string>
  private readonly connectionParams: Record<string, string>

  constructor(opts: FookieOptions) {
    this.url = opts.url

    if (opts.wsUrl) {
      this.wsUrl = opts.wsUrl
    } else {
      // Derive ws(s):// from http(s):// and ensure path ends with /ws
      const httpUrl = opts.url.replace(/^http/, 'ws')
      this.wsUrl = httpUrl.endsWith('/ws') ? httpUrl : httpUrl + '/ws'
    }

    this.headers = { 'Content-Type': 'application/json' }
    this.connectionParams = {}

    if (opts.adminKey) {
      this.headers['X-Fookie-Admin-Key'] = opts.adminKey
      this.connectionParams['adminKey'] = opts.adminKey
    }
    if (opts.token) {
      this.headers['Authorization'] = `Bearer ${opts.token}`
      this.connectionParams['token'] = opts.token
    }
  }

  /**
   * Execute a GraphQL query.
   * @example
   *   const data = await fookie.query<{ allBankWallet: Wallet[] }>(
   *     `query { allBankWallet { id balance } }`
   *   )
   */
  async query<T = unknown>(
    gql: string,
    variables?: Record<string, unknown>,
  ): Promise<T> {
    return this._send<T>(gql, variables)
  }

  /**
   * Execute a GraphQL mutation.
   * @example
   *   const data = await fookie.mutate<{ createBankWallet: Wallet }>(
   *     `mutation CreateWallet($body: BankWalletInput!) {
   *        createBankWallet(body: $body) { id }
   *      }`,
   *     { body: { address: "0x1", balance: 1000 } }
   *   )
   */
  async mutate<T = unknown>(
    gql: string,
    variables?: Record<string, unknown>,
  ): Promise<T> {
    return this._send<T>(gql, variables)
  }

  /**
   * Subscribe to a GraphQL subscription over WebSocket (graphql-transport-ws).
   * Returns an `unsubscribe` function.
   *
   * @example
   *   const unsub = fookie.subscribe(
   *     `subscription { entity_events(model: "WalletTransfer") { op payload_json } }`,
   *     (event) => console.log(event.data),
   *   )
   *   // later:
   *   unsub()
   */
  subscribe(
    gql: string,
    onData: (response: GraphQLResponse) => void,
    variables?: Record<string, unknown>,
  ): () => void {
    return wsSubscribe({
      wsUrl: this.wsUrl,
      connectionParams: this.connectionParams,
      query: gql,
      variables,
      onData,
    })
  }

  private async _send<T>(
    query: string,
    variables?: Record<string, unknown>,
  ): Promise<T> {
    const res = await fetch(this.url, {
      method: 'POST',
      headers: this.headers,
      body: JSON.stringify({ query, variables }),
    })

    if (!res.ok) {
      throw new Error(`HTTP ${res.status}: ${res.statusText}`)
    }

    const json: GraphQLResponse<T> = await res.json()
    if (json.errors && json.errors.length > 0) {
      throw new Error(json.errors.map((e) => e.message).join('; '))
    }
    return json.data as T
  }
}
