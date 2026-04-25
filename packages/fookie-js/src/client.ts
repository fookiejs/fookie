import { subscribe as wsSubscribe } from './subscribe.js'

export interface FookieOptions {
  url: string
  wsUrl?: string
  adminKey?: string
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

  async query<T = unknown>(
    gql: string,
    variables?: Record<string, unknown>,
  ): Promise<T> {
    return this._send<T>(gql, variables)
  }

  async mutate<T = unknown>(
    gql: string,
    variables?: Record<string, unknown>,
  ): Promise<T> {
    return this._send<T>(gql, variables)
  }

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
