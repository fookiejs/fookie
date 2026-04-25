export type GraphQLRequest = {
  query: string;
  variables?: Record<string, unknown>;
};

export type GraphQLResponse<T> = {
  data?: T;
  errors?: Array<{ message: string }>;
};

export type WorkerContext = {
  signal?: AbortSignal;
};
