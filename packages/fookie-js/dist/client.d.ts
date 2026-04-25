export interface FookieOptions {
  url: string;
  wsUrl?: string;
  adminKey?: string;
  token?: string;
}
export interface GraphQLResponse<T = unknown> {
  data?: T;
  errors?: Array<{
    message: string;
    locations?: unknown;
    path?: unknown;
  }>;
}
export declare class FookieClient {
  private readonly url;
  private readonly wsUrl;
  private readonly headers;
  private readonly connectionParams;
  constructor(opts: FookieOptions);
  query<T = unknown>(gql: string, variables?: Record<string, unknown>): Promise<T>;
  mutate<T = unknown>(gql: string, variables?: Record<string, unknown>): Promise<T>;
  subscribe(
    gql: string,
    onData: (response: GraphQLResponse) => void,
    variables?: Record<string, unknown>,
  ): () => void;
  private _send;
}
//# sourceMappingURL=client.d.ts.map
