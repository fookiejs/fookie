export type GraphQLRequest = {
  query: string;
  variables?: Record<string, unknown>;
};
export type GraphQLResponse<T> = {
  data?: T;
  errors?: Array<{
    message: string;
  }>;
};
export type WorkerContext = {
  signal?: AbortSignal;
};
/** Input payload passed to an external handler from fookie. */
export type ExternalInput = Record<string, unknown>;
/** Result returned by an external handler. */
export type ExternalResult = Record<string, unknown>;
/**
 * Store gives handlers access to fookie's CRUD operations via the GraphQL API.
 * Mirrors the Go Store interface so handlers have a familiar shape.
 */
export type Store = {
  read(model: string, filter?: Record<string, unknown>): Promise<ExternalResult[]>;
  create(model: string, body: Record<string, unknown>): Promise<ExternalResult>;
  update(model: string, id: string, body: Record<string, unknown>): Promise<ExternalResult>;
  delete(model: string, id: string): Promise<void>;
};
/** The function signature for an external handler. */
export type ExternalHandlerFn = (input: ExternalInput, store: Store) => Promise<ExternalResult>;
/** Shape of the request body that fookie sends to POST /call/:name */
export type WorkerCallRequest = {
  input: ExternalInput;
};
/** Shape of the response the worker must return */
export type WorkerCallResponse =
  | {
      result: ExternalResult;
      error?: never;
    }
  | {
      error: string;
      result?: never;
    };
