import type { GraphQLRequest, GraphQLResponse } from "./types.js";

export type FookieClientOptions = {
  endpoint?: string;
  adminKey?: string;
  headers?: Record<string, string>;
};

export class FookieClient {
  private endpoint: string;
  private adminKey?: string;
  private headers: Record<string, string>;

  constructor(options: FookieClientOptions = {}) {
    this.endpoint = options.endpoint ?? "http://localhost:8080/graphql";
    this.adminKey = options.adminKey;
    this.headers = options.headers ?? {};
  }

  async request<T>(payload: GraphQLRequest): Promise<GraphQLResponse<T>> {
    const headers: Record<string, string> = {
      "content-type": "application/json",
      ...this.headers
    };
    if (this.adminKey) {
      headers["admin_key"] = this.adminKey;
      headers["X-Fookie-Admin-Key"] = this.adminKey;
    }
    const res = await fetch(this.endpoint, {
      method: "POST",
      headers,
      body: JSON.stringify(payload)
    });
    return (await res.json()) as GraphQLResponse<T>;
  }
}
