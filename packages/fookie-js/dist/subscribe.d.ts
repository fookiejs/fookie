import type { GraphQLResponse } from './client.js';
export interface SubscribeOptions {
  wsUrl: string;
  connectionParams: Record<string, string>;
  query: string;
  variables?: Record<string, unknown>;
  onData: (response: GraphQLResponse) => void;
  onError?: (error: unknown) => void;
  onComplete?: () => void;
}
export declare function subscribe(opts: SubscribeOptions): () => void;
//# sourceMappingURL=subscribe.d.ts.map
