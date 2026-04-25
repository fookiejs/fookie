import type { GraphQLRequest, GraphQLResponse } from './types.js';
export type FookieClientOptions = {
  endpoint?: string;
  adminKey?: string;
  headers?: Record<string, string>;
};
export declare class FookieClient {
  private endpoint;
  private adminKey?;
  private headers;
  constructor(options?: FookieClientOptions);
  request<T>(payload: GraphQLRequest): Promise<GraphQLResponse<T>>;
}
