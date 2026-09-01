import type { Signal } from "./signal.ts";

export type OperationEvent = {
  model: string;
  operation: string;
  id: string;
  runId: string;
  signal: Signal;
  rooms: readonly string[];
};

export type OperationListener = (event: OperationEvent) => void;

export type OperationSubscription = {
  stop(): boolean;
};
