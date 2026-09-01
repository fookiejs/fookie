import { z } from "zod";
import { once } from "node:events";
import http from "node:http";
import { SpanStatusCode } from "@opentelemetry/api";
import { externalSummaryOf, modelSummaryOf } from "./catalog.ts";
import type { ExternalSummary, ModelSummary } from "./catalog.ts";
import { compensateRun, runCompensationClosed } from "./engine/compensation.ts";
import {
  executeRun,
  entityIdsOf,
  isFlowOperation,
  mutationResult,
  resolveModelByName,
  updateResult,
} from "./engine/flow.ts";
import type {
  CreateResult,
  FlowRun,
  ListResult,
  MutationResult,
  UpdateResult,
} from "./engine/flow.ts";
import { uuidV7 } from "./engine/ids.ts";
import {
  emitExternalHandler,
  outboxCompleted,
  outboxFailed,
  outboxPending,
  resolveExternalByName,
  outboxDeadLettered,
  outboxRescheduled,
} from "./engine/outbox.ts";
import type { OutboxEntry } from "./engine/outbox.ts";
import type { RunStateRow } from "./store/rows.ts";
import type {
  EmissionCursor,
  PendingEventQueue,
  PendingWriteQueue,
  RoomBox,
  Runtime,
} from "./engine/runtime.ts";
import {
  DatabaseError,
  ModelFieldError,
  NotFoundError,
  PgEncodeError,
  ValidationError,
} from "./errors.ts";
import { FailureClass, backoffDelayMs } from "./external.ts";
import type { ExternalDef, ExternalEventOf } from "./external.ts";
import { emptyListPage } from "./filter/ops.ts";
import type { ListPage } from "./filter/ops.ts";
import type { FilterInput } from "./filter/schema.ts";
import { httpErrorPayload, httpStatusForFookieError, listenPort, sendJson } from "./http.ts";
import { routeHttp } from "./http-router.ts";
import { isModelEntity } from "./model.ts";
import type {
  EntityFieldsOf,
  InferCreateBody,
  ModelDef,
  ModelEntity,
  ModelFieldsInput,
  UpdateBody,
} from "./model.ts";
import {
  Observability,
  dispatchIntervalMs,
  ddlLockTimeoutMs,
  pruneIntervalMs,
  retentionMs,
  runBufferLimit,
} from "./observability.ts";
import type {
  LogEntry,
  LogFieldValue,
  MetricEntry,
  ObsScope,
  ObservabilityPage,
  SpanEntry,
} from "./observability.ts";
import { dbErrorBoxText, dbErrorMessageForLog } from "./pg/encode.ts";
import type { DbErrorBox, PgParam, PgRow } from "./pg/encode.ts";
import type { OutboxQuery, RunQuery } from "./store/query.ts";
import type { ReadScope } from "./read-scope.ts";
import type { OperationEvent, OperationListener, OperationSubscription } from "./settled.ts";
import { Done, Failed, Phase, Running } from "./signal.ts";
import type { Signal } from "./signal.ts";
import { appendItem, catchValidation, firstPresent, mapLookup } from "./slot.ts";
import type { EntityStore } from "./store/entity-store.ts";
import type { Database } from "./store/database.ts";
import { modelDatabaseOf } from "./store/kind.ts";
import { StoreRegistry } from "./store/open.ts";
import { entityRecordFromPlain, jsonObjectFromRecord } from "./values.ts";
import type { EntityRecord, JsonObject, JsonValue } from "./values.ts";

function seqEdgeOf(
  buffer: readonly { seq: number }[],
): readonly { oldest: number; newest: number }[] {
  if (buffer.length < 1) {
    return [];
  }
  let oldest = 0;
  let newest = 0;
  let seen = false;
  for (const entry of buffer) {
    if (seen === false) {
      oldest = entry.seq;
      seen = true;
    }
    newest = entry.seq;
  }
  if (oldest < 1) {
    return [];
  }
  return [{ oldest, newest }];
}

export function models(items: readonly ModelDef<ModelFieldsInput>[]): ModelDef<ModelFieldsInput>[] {
  let registered: readonly ModelDef<ModelFieldsInput>[] = [];
  for (const modelDef of items) {
    if (z.string().min(1).safeParse(modelDef.name).success === false) {
      throw ModelFieldError.create("model name required");
    }
    registered = appendItem(registered, modelDef);
  }
  return registered.slice();
}

export type RegisteredModel = ModelDef<ModelFieldsInput>;

export type AppConfig<E extends readonly ExternalDef[] = readonly ExternalDef[]> = {
  listen: string;
  database: Database;
  models: readonly RegisteredModel[];
  externals: E;
  onExternalEvent: (event: ExternalEventOf<E[number]>) => Promise<void>;
};

const boundAddress = z.object({ port: z.number().int().positive() });

async function awaitBinding(server: http.Server): Promise<readonly number[]> {
  const heard = await Promise.race([
    once(server, "listening").then(() => true),
    once(server, "error").then(() => false),
  ]).catch(() => false);
  if (heard === false) {
    return [];
  }
  const parsed = boundAddress.safeParse(server.address());
  if (parsed.success === false) {
    return [];
  }
  if (parsed.data.port > 65535) {
    return [];
  }
  return [parsed.data.port];
}

export class App<E extends readonly ExternalDef[] = readonly ExternalDef[]> {
  private readonly listen: string;
  private readonly database: Database;
  private readonly registeredModels: readonly RegisteredModel[];
  private readonly externals: E;
  private readonly onExternalEvent: (event: ExternalEventOf<E[number]>) => Promise<void>;
  private readonly registry: StoreRegistry;
  private readonly store: EntityStore;
  private readonly runs = new Map<string, FlowRun<ModelFieldsInput>>();
  private readonly outbox = new Map<string, OutboxEntry>();
  private readonly entities = new Map<string, EntityRecord>();
  private readonly obs = new Observability();
  private listeners: readonly OperationListener[] = [];
  private readonly pendingExternalEvents: PendingEventQueue = { events: [] };
  private readonly pendingEntityWrites: PendingWriteQueue = { rows: [] };
  private dbReady = false;
  private dbSyncPending: Promise<boolean> | undefined;
  private dbErrorMessages: readonly string[] = [];
  private server: http.Server | undefined;
  private boundPort: Promise<readonly number[]> | undefined;
  private dispatcherTimer: NodeJS.Timeout | undefined;
  private dispatcherRunning = false;
  private dispatcherPrunedAtMs = 0;
  private readonly workerId = uuidV7();

  private constructor(config: AppConfig<E>) {
    this.listen = config.listen;
    this.database = config.database;
    this.registeredModels = config.models;
    this.externals = config.externals;
    this.onExternalEvent = config.onExternalEvent;
    this.registry = StoreRegistry.open(config.models, config.database, [
      (message) => {
        if (z.string().safeParse(message).success === false) {
          this.dbErrorMessages = [];
          return;
        }
        if (message.length < 1) {
          this.dbErrorMessages = [];
          return;
        }
        this.dbErrorMessages = [message];
      },
    ]);
    this.store = this.registry.defaultStore(config.database);
  }

  static create<const E extends readonly ExternalDef[]>(config: AppConfig<E>): App<E> {
    if (config.models.length < 1) {
      throw ValidationError.create("app models required");
    }
    return new App(config);
  }

  private reportAppError(
    operation: string,
    message: string,
    fields: Record<string, LogFieldValue>,
  ): void {
    const errorId = uuidV7();
    this.obs.error(
      {
        traceId: errorId,
        model: "app",
        entityId: errorId,
        operation,
        parent: [],
      },
      message,
      fields,
    );
  }

  async stop(): Promise<boolean> {
    let ok = true;
    this.stopDispatcher();
    if (this.server !== undefined) {
      const server = this.server;
      try {
        await new Promise<void>((resolve, reject) => {
          server.close((err) => {
            if (err instanceof Error) {
              reject(err);
              return;
            }
            if (z.instanceof(Error).safeParse(err).success === true) {
              reject(err);
              return;
            }
            resolve();
          });
        });
      } catch (err) {
        this.reportAppError("stop", "server stop failed", {
          reason: dbErrorMessageForLog(err, "database unavailable"),
        });
        ok = false;
      }
      this.server = undefined;
      this.boundPort = undefined;
    }
    for (const binding of this.registry.all()) {
      for (const closeStore of binding.close) {
        try {
          await closeStore();
        } catch (err) {
          this.reportAppError("stop", "store stop failed", {
            reason: dbErrorMessageForLog(err, "database unavailable"),
          });
          ok = false;
        }
      }
    }
    return ok;
  }

  private finalizeRun(runId: string, run: FlowRun<ModelFieldsInput>, signal: Signal): void {
    run.signal = signal;
    if (signal === Failed) {
      this.runs.delete(runId);
    }
    if (this.runs.size <= runBufferLimit) {
      return;
    }
    for (const [id, entry] of this.runs) {
      if (id !== runId && entry.signal !== Running) {
        this.runs.delete(id);
        if (this.runs.size <= runBufferLimit) {
          return;
        }
      }
    }
  }

  run(): boolean {
    if (this.server !== undefined) {
      return true;
    }
    const portHits = listenPort(this.listen);
    if (portHits.length < 1) {
      return false;
    }
    const port = firstPresent(portHits, "listen port required");
    const server = http.createServer((req, res) => {
      this.handleHttp(req, res).catch((err) => {
        const status = httpStatusForFookieError(err);
        if (
          status === 500 &&
          !(
            err instanceof DatabaseError ||
            err instanceof PgEncodeError ||
            err instanceof ValidationError ||
            err instanceof ModelFieldError ||
            err instanceof NotFoundError
          )
        ) {
          this.reportAppError("handleHttp", "internal error", {
            reason: dbErrorMessageForLog(err, "internal error"),
          });
        } else if (status === 500) {
          this.reportAppError("handleHttp", "internal error", {
            reason: dbErrorMessageForLog(err, "database unavailable"),
          });
        }
        if (res.headersSent === false) {
          sendJson(res, status, httpErrorPayload(err));
        }
      });
    });
    server.once("error", (err) => {
      const reason = dbErrorMessageForLog(err, "database unavailable");
      this.reportAppError("listen", "server listen failed", {
        reason,
      });
      if (this.server === server) {
        this.server = undefined;
        this.boundPort = undefined;
      }
    });
    this.boundPort = awaitBinding(server);
    server.listen(port);
    this.server = server;
    this.startDispatcher();
    this.ready().catch(() => false);
    return true;
  }

  async listening(): Promise<readonly number[]> {
    if (this.boundPort === undefined) {
      return [];
    }
    const bound = await this.boundPort;
    if (bound.length < 1) {
      return [];
    }
    return bound;
  }

  private startDispatcher(): boolean {
    if (this.dispatcherTimer !== undefined) {
      return true;
    }
    const timer = setInterval(() => this.runDispatchTick(), dispatchIntervalMs);
    timer.unref();
    this.dispatcherTimer = timer;
    return true;
  }

  private async runDispatchTick(): Promise<boolean> {
    if (this.dispatcherRunning === true) {
      return false;
    }
    this.dispatcherRunning = true;
    try {
      await this.tick();
      return true;
    } catch (err) {
      this.reportAppError("dispatch", "dispatcher tick failed", {
        reason: dbErrorMessageForLog(err, "dispatcher tick failed"),
      });
      return false;
    } finally {
      this.dispatcherRunning = false;
    }
  }

  private stopDispatcher(): boolean {
    if (this.dispatcherTimer === undefined) {
      return true;
    }
    clearInterval(this.dispatcherTimer);
    this.dispatcherTimer = undefined;
    this.dispatcherRunning = false;
    return true;
  }

  create<D extends ModelFieldsInput>(
    model: ModelDef<D>,
    body: InferCreateBody<D>,
  ): Promise<CreateResult<ModelEntity<D>>> {
    const runId = uuidV7();
    const entityId = uuidV7();
    const run: FlowRun<D> = {
      id: runId,
      model,
      operation: "create",
      entityId,
      body: [entityRecordFromPlain(body)],
      filter: [],
      entity: [],
      created: [],
      results: [],
      page: [],
      signal: Running,
      starts: 0,
      emissions: { seen: 0, published: 0 },
      rooms: { names: [] },
    };
    this.runs.set(runId, run);
    const createRt = this.runtimeFor(runId, model, entityId, "create");
    return executeRun(createRt, run).then(async (signal): Promise<CreateResult<ModelEntity<D>>> => {
      this.finalizeRun(runId, run, signal);
      await this.saveRunPhase(runId, run, signal);
      await this.publishSettled({
        model: model.name,
        operation: "create",
        id: entityId,
        runId,
        signal,
        rooms: createRt.rooms.names,
      });
      if (signal === Done) {
        for (const created of run.created) {
          if (isModelEntity(model, created) === false) {
            return { signal: Failed, id: entityId, runId };
          }
          return {
            signal: Done,
            id: entityId,
            runId,
            entity: created,
          };
        }
      }
      if (signal === Running) {
        return { signal: Running, id: entityId, runId };
      }
      return { signal: Failed, id: entityId, runId };
    });
  }

  list<D extends ModelFieldsInput>(
    model: ModelDef<D>,
    filter: FilterInput,
    page: ListPage = emptyListPage(),
  ): Promise<ListResult<EntityRecord>> {
    if (z.string().min(1).safeParse(model.name).success === false) {
      throw ValidationError.create("list model required");
    }
    if (Array.isArray(page.order) === false) {
      throw ValidationError.create("list page order required");
    }
    return this.listWith([], model, filter, page);
  }

  private listWith<D extends ModelFieldsInput>(
    pinned: readonly EntityStore[],
    model: ModelDef<D>,
    filter: FilterInput,
    page: ListPage,
  ): Promise<ListResult<EntityRecord>> {
    const runId = uuidV7();
    const run: FlowRun<D> = {
      id: runId,
      model,
      operation: "list",
      entityId: runId,
      body: [],
      filter: [filter],
      entity: [],
      created: [],
      results: [],
      page: [page],
      signal: Running,
      starts: 0,
      emissions: { seen: 0, published: 0 },
      rooms: { names: [] },
    };
    this.runs.set(runId, run);
    const rt = this.runtimeFor(runId, model, runId, "list", pinned);
    return executeRun(rt, run).then((signal) => {
      this.finalizeRun(runId, run, signal);
      return { signal, runId, results: run.results.slice() };
    });
  }

  private async settleMutation(
    runId: string,
    run: FlowRun<ModelFieldsInput>,
    signal: Signal,
    entityId: string,
  ): Promise<MutationResult> {
    this.finalizeRun(runId, run, signal);
    await this.saveRunPhase(runId, run, signal);
    await this.publishSettled({
      model: run.model.name,
      operation: run.operation,
      id: entityId,
      runId,
      signal,
      rooms: run.rooms.names,
    });
    return mutationResult(signal, entityId, runId);
  }

  private async settleUpdate(
    runId: string,
    run: FlowRun<ModelFieldsInput>,
    signal: Signal,
  ): Promise<UpdateResult> {
    this.finalizeRun(runId, run, signal);
    await this.saveRunPhase(runId, run, signal);
    const ids = entityIdsOf(run.entity);
    if (signal === Done) {
      for (const entityId of ids) {
        await this.publishSettled({
          model: run.model.name,
          operation: "update",
          id: entityId,
          runId,
          signal,
          rooms: run.rooms.names,
        });
      }
    }
    return updateResult(signal, ids, runId);
  }

  update<D extends ModelFieldsInput>(
    model: ModelDef<D>,
    filter: FilterInput,
    body: UpdateBody<EntityFieldsOf<D>>,
  ): Promise<UpdateResult> {
    const runId = uuidV7();
    const run: FlowRun<D> = {
      id: runId,
      model,
      operation: "update",
      entityId: runId,
      body: [jsonObjectFromRecord(body)],
      filter: [filter],
      entity: [],
      created: [],
      results: [],
      page: [],
      signal: Running,
      starts: 0,
      emissions: { seen: 0, published: 0 },
      rooms: { names: [] },
    };
    this.runs.set(runId, run);
    const mutationRt = this.runtimeFor(runId, model, runId, "update");
    return executeRun(mutationRt, run).then((signal) => this.settleUpdate(runId, run, signal));
  }

  delete<D extends ModelFieldsInput>(
    model: ModelDef<D>,
    input: { id: string; filter: FilterInput },
  ): Promise<MutationResult> {
    const runId = uuidV7();
    const run: FlowRun<D> = {
      id: runId,
      model,
      operation: "delete",
      entityId: input.id,
      body: [],
      filter: [input.filter],
      entity: [],
      created: [],
      results: [],
      page: [],
      signal: Running,
      starts: 0,
      emissions: { seen: 0, published: 0 },
      rooms: { names: [] },
    };
    this.runs.set(runId, run);
    const mutationRt = this.runtimeFor(runId, model, input.id, "delete");
    return executeRun(mutationRt, run).then((signal) =>
      this.settleMutation(runId, run, signal, input.id),
    );
  }

  async resume(runId: string): Promise<Signal> {
    const dbOk = await this.awaitDb();
    if (dbOk === false) {
      return Failed;
    }
    await this.ensureRunLoaded(runId);
    const runHits = mapLookup(this.runs, runId);
    if (runHits.length < 1) {
      return Failed;
    }
    const run = firstPresent(runHits, "run required");
    if (run.signal !== Running) {
      return run.signal;
    }
    return executeRun(this.runtimeFor(runId, run.model, run.entityId, run.operation), run).then(
      async (signal) => {
        if (z.string().min(1).safeParse(runId).success === false) {
          throw ValidationError.create("resume run id required");
        }
        if (run.signal !== Running && run.signal !== Done && run.signal !== Failed) {
          throw ValidationError.create("resume signal invalid");
        }
        const resumeRt = this.runtimeFor(runId, run.model, run.entityId, run.operation);
        this.finalizeRun(runId, run, signal);
        await this.saveRunPhase(runId, run, signal);
        this.publishSettled({
          model: run.model.name,
          operation: run.operation,
          id: run.entityId,
          runId,
          signal,
          rooms: resumeRt.rooms.names,
        });
        return signal;
      },
    );
  }

  private reportUnknownExternal(externalId: string, event: string): void {
    const scope = this.rootScope();
    this.obs.count(scope, event);
    this.obs.error(scope, event, {
      externalId,
      reason: "outbox entry not found on the control database",
      hint: "settlement looks up fookie_outbox by external_id on app.database",
    });
  }

  private async ensureOutboxLoaded(externalId: string): Promise<readonly OutboxEntry[]> {
    const cached = mapLookup(this.outbox, externalId);
    if (cached.length > 0) {
      return cached;
    }
    const loaded = await this.store.loadOutboxById(externalId);
    for (const row of loaded) {
      this.outbox.set(row.externalId, row);
    }
    return loaded;
  }

  private async ensureRunLoaded(runId: string): Promise<boolean> {
    if (this.runs.has(runId) === true) {
      return true;
    }
    const rows = await this.store.loadRunState(runId);
    for (const runState of rows) {
      const modelHits = resolveModelByName(this.registeredModels, runState.model);
      if (modelHits.length < 1) {
        return false;
      }
      const model = firstPresent(modelHits, "recovered model required");
      if (isFlowOperation(runState.operation) === false) {
        return false;
      }
      this.runs.set(runState.runId, {
        id: runState.runId,
        model,
        operation: runState.operation,
        entityId: runState.entityId,
        body: [runState.body],
        filter: this.restoredFilter(runState.filterJson, model),
        entity: [],
        created: [],
        results: [],
        page: [],
        signal: Running,
        starts: 1,
        emissions: { seen: 0, published: 0 },
        rooms: { names: [] },
      });
      return true;
    }
    return false;
  }

  async setExternalResult(externalResult: {
    externalId: string;
    output: JsonValue;
  }): Promise<boolean> {
    const hydrated = await this.awaitDb();
    if (hydrated === false) {
      return false;
    }
    const outboxHits = await this.ensureOutboxLoaded(externalResult.externalId);
    if (outboxHits.length < 1) {
      this.reportUnknownExternal(externalResult.externalId, "external.result_unknown");
      return false;
    }
    {
      const outboxRow = firstPresent(outboxHits, "outbox entry required");
      await this.ensureRunLoaded(outboxRow.runId);
      if (outboxRow.status === "completed") {
        return true;
      }
      if (outboxRow.status === "failed") {
        return false;
      }
      const runs = mapLookup(this.runs, outboxRow.runId);
      const resolvedModels = resolveModelByName(this.registeredModels, outboxRow.model);
      let scopeModel = outboxRow.model;
      for (const hit of resolvedModels) {
        scopeModel = hit.name;
      }
      if (resolvedModels.length < 1) {
        for (const run of runs) {
          scopeModel = run.model.name;
        }
      }
      let scopeOperation = "external";
      for (const run of runs) {
        scopeOperation = run.operation;
      }
      const scope: ObsScope = {
        traceId: outboxRow.runId,
        model: scopeModel,
        entityId: outboxRow.entityId,
        operation: scopeOperation,
        parent: [],
      };
      const extHits = resolveExternalByName(this.externals, outboxRow.name);
      if (extHits.length < 1) {
        this.obs.error(scope, "external.result_rejected", {
          reason: "unknown external",
          name: outboxRow.name,
          externalId: outboxRow.externalId,
        });
        this.obs.count(scope, "external.failed");
        this.obs.info(scope, "external.failed", {
          externalId: outboxRow.externalId,
          attempt: outboxRow.attempt,
        });
        const unknownFailed = await this.recordOutbox(outboxFailed(outboxRow));
        if (unknownFailed === false) {
          return false;
        }
        await this.resumeKnownRun(scope, outboxRow.runId, resolvedModels, runs);
        return false;
      }
      const ext = firstPresent(extHits, "external required");
      const spanAttributes = { externalName: outboxRow.name, externalId: outboxRow.externalId };
      return this.obs.runSpan(scope, "external.result", spanAttributes, async (span) => {
        const validatedHits = catchValidation(() => ext.validateOutput(externalResult.output));
        if (validatedHits.length < 1) {
          if (outboxRow.attempt < ext.attempts) {
            const nextAttempt = outboxRow.attempt + 1;
            this.obs.count(scope, "external.retry");
            this.obs.info(scope, "external.retry", {
              externalId: outboxRow.externalId,
              attempt: nextAttempt,
            });
            const dueAt = new Date(Date.now() + backoffDelayMs(ext.backoff, nextAttempt));
            const recorded = await this.recordOutbox(
              outboxRescheduled(outboxRow, nextAttempt, dueAt.toISOString()),
            );
            if (recorded === false) {
              span.setStatus({ code: SpanStatusCode.ERROR, message: "database unavailable" });
              return false;
            }
            const emitted = await emitExternalHandler(
              this.onExternalEvent,
              ext,
              outboxRow.externalId,
              outboxRow.input,
            );
            if (emitted !== "emitted") {
              this.obs.error(scope, "external.emit_skipped", {
                reason: emitted === "handler_error" ? "handler error" : "invalid input",
                name: outboxRow.name,
                externalId: outboxRow.externalId,
              });
              const skippedFailed = await this.recordOutbox(
                outboxFailed(outboxPending(outboxRow, nextAttempt)),
              );
              if (skippedFailed === false) {
                span.setStatus({ code: SpanStatusCode.ERROR, message: "database unavailable" });
                return false;
              }
              this.obs.count(scope, "external.failed");
              this.obs.info(scope, "external.failed", {
                externalId: outboxRow.externalId,
                attempt: nextAttempt,
              });
              await this.resumeKnownRun(scope, outboxRow.runId, resolvedModels, runs);
              return false;
            }
            return false;
          }
          this.obs.count(scope, "external.failed");
          this.obs.info(scope, "external.failed", {
            externalId: outboxRow.externalId,
            attempt: outboxRow.attempt,
          });
          span.setStatus({ code: SpanStatusCode.ERROR, message: "external output invalid" });
          const failedRecorded = await this.recordOutbox(outboxFailed(outboxRow));
          if (failedRecorded === false) {
            span.setStatus({ code: SpanStatusCode.ERROR, message: "database unavailable" });
            return false;
          }
          await this.resumeKnownRun(scope, outboxRow.runId, resolvedModels, runs);
          return false;
        }
        const validated = firstPresent(validatedHits, "validated body required");
        this.obs.count(scope, "external.completed");
        this.obs.info(scope, "external.completed", { externalId: outboxRow.externalId });
        const completedRecorded = await this.recordOutbox(outboxCompleted(outboxRow, validated));
        if (completedRecorded === false) {
          span.setStatus({ code: SpanStatusCode.ERROR, message: "database unavailable" });
          return false;
        }
        await this.resumeKnownRun(scope, outboxRow.runId, resolvedModels, runs);
        return true;
      });
    }
  }

  private async resumeKnownRun(
    scope: ObsScope,
    runId: string,
    resolvedModels: readonly ModelDef<ModelFieldsInput>[],
    runs: readonly FlowRun[],
  ): Promise<void> {
    let known: readonly ModelDef<ModelFieldsInput>[] = [];
    for (const resolvedModel of resolvedModels) {
      known = [resolvedModel];
    }
    for (const runningRun of runs) {
      known = [runningRun.model];
    }
    if (known.length < 1) {
      return;
    }
    this.obs.info(scope, "flow.resumed", { runId });
    const resumed = await this.resume(runId);
    if (resumed === Failed) {
      this.obs.error(scope, "flow.resume_failed", { runId });
    }
  }

  private runBodyOf(bodies: readonly JsonObject[]): JsonObject {
    const body = bodies[0];
    if (body === undefined) {
      return {};
    }
    return body;
  }

  private rootScope(): ObsScope {
    const dispatcherId = "dispatcher";
    if (z.string().min(1).safeParse(dispatcherId).success === false) {
      throw ValidationError.create("dispatcher scope required");
    }
    return {
      traceId: dispatcherId,
      model: dispatcherId,
      entityId: dispatcherId,
      operation: "dispatch",
      parent: [],
    };
  }

  private async pruneIfDue(nowMs: number): Promise<number> {
    if (nowMs - this.dispatcherPrunedAtMs < pruneIntervalMs) {
      return 0;
    }
    this.dispatcherPrunedAtMs = nowMs;
    const cutoff = new Date(nowMs - retentionMs).toISOString();
    const pruned = await this.store.pruneSettledRuns(cutoff);
    let removed: readonly string[] = [];
    for (const runId of pruned) {
      removed = appendItem(removed, runId);
    }
    for (const runId of removed) {
      for (const [externalId, outboxRow] of this.outbox) {
        if (outboxRow.runId === runId) {
          this.outbox.delete(externalId);
        }
      }
      this.runs.delete(runId);
    }
    if (removed.length > 0) {
      this.obs.count(this.rootScope(), "saga.pruned");
      this.obs.info(this.rootScope(), "saga.pruned", { runs: removed.length });
    }
    return removed.length;
  }

  private async deadLetter(outboxRow: OutboxEntry, reason: string): Promise<boolean> {
    const scope = this.rootScope();
    const recorded = await this.recordOutbox(outboxDeadLettered(outboxRow, reason));
    if (recorded === false) {
      return false;
    }
    this.obs.count(scope, "external.dead_letter");
    this.obs.error(scope, "external.dead_letter", {
      externalId: outboxRow.externalId,
      externalName: outboxRow.name,
      runId: outboxRow.runId,
      reason,
    });
    const undone = await this.compensateDeadLettered(outboxRow.runId);
    if (undone > 0) {
      await this.saveRunPhaseValue(outboxRow.runId, Phase.Compensating, reason);
      return true;
    }
    await this.markRunStuck(outboxRow.runId, reason);
    return true;
  }

  private async compensateDeadLettered(runId: string): Promise<number> {
    if (z.string().min(1).safeParse(runId).success === false) {
      return 0;
    }
    for (const run of mapLookup(this.runs, runId)) {
      const rt = this.runtimeFor(runId, run.model, run.entityId, run.operation);
      return await compensateRun(rt, runId);
    }
    return 0;
  }

  private async saveRunPhaseValue(runId: string, phase: Phase, reason: string): Promise<boolean> {
    if (z.string().min(1).safeParse(runId).success === false) {
      return false;
    }
    for (const run of mapLookup(this.runs, runId)) {
      return await this.store.saveRunState({
        runId,
        model: run.model.name,
        entityId: run.entityId,
        operation: run.operation,
        body: this.runBodyOf(run.body),
        filterJson: JSON.stringify(run.filter),
        phase,
        pivotExternalId: [],
        error: [reason],
      });
    }
    return false;
  }

  private phaseForSignal(runId: string, signal: Signal): Phase {
    if (signal === Done) {
      return Phase.Completed;
    }
    if (signal === Running) {
      return Phase.Forward;
    }
    if (runCompensationClosed(this.outbox.values(), this.externals, runId) === false) {
      return Phase.Stuck;
    }
    let undoing = false;
    for (const outboxRow of this.outbox.values()) {
      if (outboxRow.runId !== runId) {
        continue;
      }
      if (outboxRow.compensationOf.length > 0 && outboxRow.status === "pending") {
        undoing = true;
      }
    }
    if (undoing === true) {
      return Phase.Compensating;
    }
    return Phase.Compensated;
  }

  private async saveRunPhase(
    runId: string,
    run: FlowRun<ModelFieldsInput>,
    signal: Signal,
  ): Promise<boolean> {
    if (z.string().min(1).safeParse(runId).success === false) {
      return false;
    }
    if (run.operation === "list") {
      return false;
    }
    if (signal === Done) {
      let hasOutbox = false;
      for (const outboxRow of this.outbox.values()) {
        if (outboxRow.runId === runId) {
          hasOutbox = true;
          break;
        }
      }
      if (hasOutbox === false) {
        return false;
      }
    }
    return await this.store.saveRunState({
      runId,
      model: run.model.name,
      entityId: run.entityId,
      operation: run.operation,
      body: this.runBodyOf(run.body),
      filterJson: JSON.stringify(run.filter),
      phase: this.phaseForSignal(runId, signal),
      pivotExternalId: [],
      error: [],
    });
  }

  private async markRunStuck(runId: string, reason: string): Promise<boolean> {
    const scope = this.rootScope();
    if (z.string().min(1).safeParse(runId).success === false) {
      return false;
    }
    this.obs.count(scope, "saga.stuck");
    this.obs.error(scope, "saga.stuck", { runId, reason });
    for (const run of mapLookup(this.runs, runId)) {
      const saved = await this.store.saveRunState({
        runId,
        model: run.model.name,
        entityId: run.entityId,
        operation: run.operation,
        body: this.runBodyOf(run.body),
        filterJson: JSON.stringify(run.filter),
        phase: Phase.Stuck,
        pivotExternalId: [],
        error: [reason],
      });
      return saved;
    }
    return false;
  }

  async tick(): Promise<number> {
    const scope = this.rootScope();
    const dbOk = await this.awaitDb();
    if (dbOk === false) {
      return 0;
    }
    const nowMs = Date.now();
    await this.pruneIfDue(nowMs);
    const nowIso = new Date(nowMs).toISOString();
    const dueRows = await this.store.claimDueOutbox(this.workerId, nowIso, 100);
    let dispatched = 0;
    for (const claimed of dueRows) {
      this.outbox.set(claimed.externalId, claimed);
      const outboxRow = claimed;
      if (outboxRow.status !== "pending") {
        continue;
      }
      let dueLater = false;
      for (const iso of outboxRow.nextAttemptAt) {
        const parsed = Date.parse(iso);
        if (Number.isFinite(parsed) === false) {
          continue;
        }
        if (parsed > nowMs) {
          dueLater = true;
        }
        break;
      }
      if (dueLater === true) {
        continue;
      }
      const extHits = resolveExternalByName(this.externals, outboxRow.name);
      if (extHits.length < 1) {
        await this.deadLetter(outboxRow, "unknown external");
        continue;
      }
      const ext = firstPresent(extHits, "external required");
      let expired = false;
      if (Number.isFinite(ext.timeoutMs) === true && ext.timeoutMs >= 1) {
        const dispatchedIso = outboxRow.dispatchedAt[0];
        if (dispatchedIso !== undefined) {
          const sentAt = Date.parse(dispatchedIso);
          if (Number.isFinite(sentAt) === true) {
            expired = nowMs - sentAt > ext.timeoutMs;
          }
        }
      }
      if (expired === true) {
        this.obs.count(scope, "external.timed_out");
        this.obs.error(scope, "external.timed_out", {
          externalId: outboxRow.externalId,
          externalName: outboxRow.name,
          timeoutMs: ext.timeoutMs,
        });
      }
      if (outboxRow.attempt >= ext.attempts) {
        const reason = expired === true ? "timed out" : "attempts exhausted";
        await this.deadLetter(outboxRow, reason);
        continue;
      }
      const nextAttempt = outboxRow.attempt + 1;
      const delay = backoffDelayMs(ext.backoff, nextAttempt);
      const rescheduled = outboxRescheduled(
        outboxRow,
        nextAttempt,
        new Date(nowMs + delay).toISOString(),
      );
      const recorded = await this.recordOutbox(rescheduled);
      if (recorded === false) {
        continue;
      }
      this.obs.count(scope, "external.retry");
      this.obs.info(scope, "external.retry", {
        externalId: outboxRow.externalId,
        externalName: outboxRow.name,
        attempt: nextAttempt,
      });
      await emitExternalHandler(this.onExternalEvent, ext, outboxRow.externalId, outboxRow.input);
      dispatched += 1;
    }
    return dispatched;
  }

  async setExternalFailure(failure: {
    externalId: string;
    reason: string;
    failure: FailureClass;
  }): Promise<boolean> {
    const scope = this.rootScope();
    if (z.string().min(1).safeParse(failure.externalId).success === false) {
      return false;
    }
    if (z.string().min(1).safeParse(failure.reason).success === false) {
      return false;
    }
    const dbOk = await this.awaitDb();
    if (dbOk === false) {
      return false;
    }
    if (this.outbox.has(failure.externalId) === false) {
      const loaded = await this.ensureOutboxLoaded(failure.externalId);
      if (loaded.length < 1) {
        this.reportUnknownExternal(failure.externalId, "external.failure_unknown");
        return false;
      }
    }
    for (const outboxRow of mapLookup(this.outbox, failure.externalId)) {
      if (outboxRow.status !== "pending") {
        return false;
      }
      const extHits = resolveExternalByName(this.externals, outboxRow.name);
      if (extHits.length < 1) {
        return await this.deadLetter(outboxRow, failure.reason);
      }
      const ext = firstPresent(extHits, "external required");
      const budgetLeft = outboxRow.attempt < ext.attempts;
      if (failure.failure === FailureClass.Transient && budgetLeft === true) {
        const nextAttempt = outboxRow.attempt + 1;
        const dueAt = new Date(Date.now() + backoffDelayMs(ext.backoff, nextAttempt));
        this.obs.count(scope, "external.transient_failure");
        this.obs.info(scope, "external.transient_failure", {
          externalId: outboxRow.externalId,
          attempt: nextAttempt,
          reason: failure.reason,
        });
        return await this.recordOutbox(
          outboxRescheduled(outboxRow, nextAttempt, dueAt.toISOString()),
        );
      }
      this.obs.count(scope, "external.permanent_failure");
      return await this.deadLetter(outboxRow, failure.reason);
    }
    return false;
  }

  async retryExternal(externalId: string): Promise<boolean> {
    if (z.string().min(1).safeParse(externalId).success === false) {
      return false;
    }
    const dbOk = await this.awaitDb();
    if (dbOk === false) {
      return false;
    }
    for (const outboxRow of await this.ensureOutboxLoaded(externalId)) {
      if (outboxRow.status !== "dead_letter") {
        return false;
      }
      this.obs.count(this.rootScope(), "external.retry_requested");
      return await this.recordOutbox(
        outboxRescheduled(outboxRow, 1, new Date(Date.now()).toISOString()),
      );
    }
    return false;
  }

  deadLetters(): OutboxEntry[] {
    let rows: OutboxEntry[] = [];
    for (const outboxRow of this.outbox.values()) {
      if (outboxRow.status === "dead_letter") {
        rows = rows.concat([outboxRow]);
      }
    }
    return rows;
  }

  async sagaRun(runId: string): Promise<readonly RunStateRow[]> {
    if (z.string().min(1).safeParse(runId).success === false) {
      return [];
    }
    const dbOk = await this.awaitDb();
    if (dbOk === false) {
      return [];
    }
    for (const binding of this.registry.all()) {
      const rows = await binding.store.loadRunState(runId);
      if (rows.length > 0) {
        return rows;
      }
    }
    return [];
  }

  catalog(): readonly ModelSummary[] {
    if (this.registeredModels.length < 1) {
      throw ValidationError.create("registered models required");
    }
    let summaries: readonly ModelSummary[] = [];
    for (const model of this.registeredModels) {
      summaries = appendItem(summaries, modelSummaryOf(model));
    }
    return summaries;
  }

  externalCatalog(): readonly ExternalSummary[] {
    if (Array.isArray(this.externals) === false) {
      throw ValidationError.create("registered externals required");
    }
    let summaries: readonly ExternalSummary[] = [];
    for (const external of this.externals) {
      summaries = appendItem(summaries, externalSummaryOf(external));
    }
    return summaries;
  }

  models(): readonly RegisteredModel[] {
    if (this.registeredModels.length < 1) {
      throw ValidationError.create("registered models required");
    }
    if (Array.isArray(this.registeredModels) === false) {
      throw ValidationError.create("registered models required");
    }
    return this.registeredModels.slice();
  }

  async sql(statement: string, params: readonly PgParam[]): Promise<readonly PgRow[]> {
    if (z.string().min(1).safeParse(statement).success === false) {
      throw ValidationError.create("sql statement required");
    }
    if (Array.isArray(params) === false) {
      throw ValidationError.create("sql params required");
    }
    await this.awaitDb();
    return await this.store.selectRows(statement, params);
  }

  async runList(query: RunQuery): Promise<readonly RunStateRow[]> {
    if (Array.isArray(query.phase) === false) {
      throw ValidationError.create("run query phase required");
    }
    if (Number.isInteger(query.limit) === false) {
      throw ValidationError.create("run query limit required");
    }
    await this.awaitDb();
    return await this.store.queryRuns(query);
  }

  async outboxList(query: OutboxQuery): Promise<readonly OutboxEntry[]> {
    if (Array.isArray(query.status) === false) {
      throw ValidationError.create("outbox query status required");
    }
    if (Array.isArray(query.runId) === false) {
      throw ValidationError.create("outbox query run id required");
    }
    await this.awaitDb();
    return await this.store.queryOutbox(query);
  }

  onOperationSettled(listener: OperationListener): OperationSubscription {
    if (typeof listener !== "function") {
      throw ValidationError.create("operation listener required");
    }
    this.listeners = appendItem(this.listeners, listener);
    return {
      stop: () => {
        const before = this.listeners.length;
        this.listeners = this.listeners.filter((registered) => registered !== listener);
        return this.listeners.length < before;
      },
    };
  }

  private async publishSettled(event: OperationEvent): Promise<boolean> {
    if (z.string().min(1).safeParse(event.model).success === false) {
      throw ValidationError.create("settled event model required");
    }
    for (const listener of this.listeners) {
      try {
        listener(event);
      } catch (err) {
        this.reportAppError("settled", "operation listener failed", {
          reason: dbErrorMessageForLog(err, "listener failed"),
          model: event.model,
        });
      }
    }
    return await this.store.announceOperation(event);
  }

  observability(since: number): ObservabilityPage {
    if (Number.isInteger(since) === false || since < 0) {
      throw ValidationError.create("observability cursor must be a non-negative integer");
    }
    const logs = this.obs.buffers.logs.filter((logEntry) => logEntry.seq > since);
    const metrics = this.obs.buffers.metrics.filter((metricEntry) => metricEntry.seq > since);
    const spans = this.obs.buffers.spans.filter((spanEntry) => spanEntry.seq > since);
    let nextSeq = since;
    let oldestSeq = 0;
    for (const edge of this.bufferEdges()) {
      if (edge.newest > nextSeq) {
        nextSeq = edge.newest;
      }
      if (oldestSeq === 0 || edge.oldest < oldestSeq) {
        oldestSeq = edge.oldest;
      }
    }
    return { logs, metrics, spans, nextSeq, oldestSeq };
  }

  private bufferEdges(): readonly { oldest: number; newest: number }[] {
    let edges: readonly { oldest: number; newest: number }[] = [];
    for (const seqs of [
      seqEdgeOf(this.obs.buffers.logs),
      seqEdgeOf(this.obs.buffers.metrics),
      seqEdgeOf(this.obs.buffers.spans),
    ]) {
      for (const edge of seqs) {
        edges = appendItem(edges, edge);
      }
    }
    return edges;
  }

  logs(): LogEntry[] {
    if (Array.isArray(this.obs.buffers.logs) === false) {
      throw ValidationError.create("log buffer required");
    }
    const copied = this.obs.buffers.logs.slice();
    if (Array.isArray(copied) === false) {
      throw ValidationError.create("log copy required");
    }
    return copied;
  }

  metrics(): MetricEntry[] {
    if (Array.isArray(this.obs.buffers.metrics) === false) {
      throw ValidationError.create("metric buffer required");
    }
    const copied = this.obs.buffers.metrics.slice();
    if (Array.isArray(copied) === false) {
      throw ValidationError.create("metric copy required");
    }
    return copied;
  }

  spans(): SpanEntry[] {
    if (Array.isArray(this.obs.buffers.spans) === false) {
      throw ValidationError.create("span buffer required");
    }
    const copied = this.obs.buffers.spans.slice();
    if (Array.isArray(copied) === false) {
      throw ValidationError.create("span copy required");
    }
    return copied;
  }

  private async recordOutbox(outboxRow: OutboxEntry): Promise<boolean> {
    const previous = mapLookup(this.outbox, outboxRow.externalId);
    this.outbox.set(outboxRow.externalId, outboxRow);
    const ok = await this.store.saveOutboxEntry(outboxRow);
    if (ok === false) {
      if (previous.length < 1) {
        this.outbox.delete(outboxRow.externalId);
      } else {
        for (const prior of previous) {
          this.outbox.set(outboxRow.externalId, prior);
        }
      }
      return false;
    }
    return true;
  }

  async withReadSnapshot<T>(run: (scope: ReadScope) => Promise<T>): Promise<T> {
    await this.awaitDb();
    const session = await this.store.connectSession();
    let opened = false;
    try {
      await session.beginReadSnapshot();
      opened = true;
      const scope: ReadScope = {
        list: (model, filter, page = emptyListPage()) => {
          if (z.string().min(1).safeParse(model.name).success === false) {
            throw ValidationError.create("snapshot list model required");
          }
          const engine = modelDatabaseOf(model, this.database);
          if (engine.key === this.database.key) {
            return this.listWith([session.store], model, filter, page);
          }
          return this.listWith([], model, filter, page);
        },
        sql: (statement, params) => session.store.selectRows(statement, params),
      };
      return await run(scope);
    } finally {
      if (opened === true) {
        try {
          await session.commit();
        } catch (err) {
          this.reportAppError("snapshot", "snapshot commit failed", {
            reason: dbErrorMessageForLog(err, "database unavailable"),
          });
        }
      }
      session.release();
    }
  }

  private storeOf(pinned: readonly EntityStore[], model: ModelDef<ModelFieldsInput>): EntityStore {
    const pinnedStore = pinned[0];
    if (pinnedStore !== undefined) {
      return pinnedStore;
    }
    if (z.string().min(1).safeParse(model.name).success === false) {
      throw DatabaseError.create("model name required");
    }
    const engine = modelDatabaseOf(model, this.database);
    if (z.string().min(1).safeParse(engine.key).success === false) {
      throw DatabaseError.create("model database required");
    }
    return this.registry.require(engine.key).store;
  }

  private runtimeFor(
    traceId: string,
    model: ModelDef<ModelFieldsInput>,
    entityId: string,
    operation: string,
    pinned: readonly EntityStore[] = [],
  ): Runtime<E> {
    let emissions: EmissionCursor = { seen: 0, published: 0 };
    let rooms: RoomBox = { names: [] };
    for (const run of mapLookup(this.runs, traceId)) {
      emissions = run.emissions;
      rooms = run.rooms;
    }
    return {
      traceId,
      model,
      entityId,
      operation,
      parent: [],
      rooms,
      obs: this.obs,
      outbox: this.outbox,
      onExternalEvent: this.onExternalEvent,
      models: this.registeredModels,
      externals: this.externals,
      entities: this.entities,
      store: this.storeOf(pinned, model),
      appDatabase: this.database,
      lookupStore: (database) => this.registry.require(database).store,
      pendingExternalEvents: this.pendingExternalEvents,
      pendingEntityWrites: this.pendingEntityWrites,
      nestedSteps: { steps: 0 },
      emissions,
      reportDbError: (message: string) => {
        if (z.string().safeParse(message).success === false) {
          this.dbErrorMessages = [];
          return;
        }
        if (message.length < 1) {
          this.dbErrorMessages = [];
          return;
        }
        this.dbErrorMessages = [message];
      },
      clearDbError: () => {
        this.dbErrorMessages = [];
      },
      dbLastError: () => this.dbErrorMessages,
      awaitDb: () => this.awaitDb(),
      resume: (runId) => this.resume(runId),
    };
  }

  private modelsOn(database: string): readonly RegisteredModel[] {
    let onStore: readonly RegisteredModel[] = [];
    for (const model of this.registeredModels) {
      if (modelDatabaseOf(model, this.database).key === database) {
        onStore = appendItem(onStore, model);
      }
    }
    return onStore;
  }

  private async syncSchema(): Promise<boolean> {
    const errorBox: DbErrorBox = { message: "database unavailable" };
    for (const binding of this.registry.all()) {
      let session;
      try {
        session = await binding.store.connectSession();
      } catch (err) {
        this.dbErrorMessages = [dbErrorMessageForLog(err, "database unavailable")];
        return false;
      }
      try {
        const pinned = session.store;
        await pinned.applyDdlLockTimeout(ddlLockTimeoutMs);
        const control = binding.database === this.database.key;
        const tablesOk = await pinned.ensureAllTables(
          this.modelsOn(binding.database),
          this.registeredModels,
          this.database,
          errorBox,
          { control },
        );
        if (tablesOk === false) {
          this.dbErrorMessages = [dbErrorBoxText(errorBox)];
          return false;
        }
        if (control === false) {
          continue;
        }
        const outboxOk = await pinned.loadOutbox(this.outbox, errorBox);
        if (outboxOk === false) {
          this.dbErrorMessages = [dbErrorBoxText(errorBox)];
          return false;
        }
      } finally {
        session.release();
      }
    }
    this.dbReady = true;
    await this.recoverRuns();
    return true;
  }

  ready(): Promise<boolean> {
    if (this.dbReady === true) {
      return Promise.resolve(true);
    }
    if (this.dbSyncPending !== undefined) {
      return this.dbSyncPending;
    }
    const started = this.syncSchema().finally(() => {
      this.dbSyncPending = undefined;
    });
    this.dbSyncPending = started;
    return started;
  }

  private async awaitDb(): Promise<boolean> {
    if (this.dbReady === true) {
      return true;
    }
    const synced = await this.ready();
    if (synced === false) {
      return false;
    }
    if (this.dbReady === false) {
      throw DatabaseError.create("schema sync reported ready without finishing");
    }
    return true;
  }

  private restoredFilter(
    filterJson: string,
    model: ModelDef<ModelFieldsInput>,
  ): readonly FilterInput[] {
    const parsedHits = catchValidation(() => {
      const raw: JsonValue = JSON.parse(filterJson);
      if (Array.isArray(raw) === false) {
        throw ValidationError.create("run filter invalid");
      }
      let restored: readonly FilterInput[] = [];
      for (const filterEntry of raw) {
        restored = appendItem(restored, model.validateListFilter(filterEntry));
      }
      return restored;
    });
    const restored = parsedHits[0];
    if (restored === undefined) {
      return [];
    }
    return restored;
  }

  private async recoverRuns(): Promise<number> {
    const scope = this.rootScope();
    const rows = await this.store.loadResumableRuns(runBufferLimit);
    let restored = 0;
    for (const runState of rows) {
      if (this.runs.has(runState.runId) === true) {
        continue;
      }
      const modelHits = resolveModelByName(this.registeredModels, runState.model);
      if (modelHits.length < 1) {
        this.obs.count(scope, "saga.recovery_model_missing");
        this.obs.error(scope, "saga.recovery_model_missing", {
          runId: runState.runId,
          model: runState.model,
        });
        await this.store.saveRunState({
          runId: runState.runId,
          model: runState.model,
          entityId: runState.entityId,
          operation: runState.operation,
          body: runState.body,
          filterJson: runState.filterJson,
          phase: Phase.Stuck,
          pivotExternalId: [],
          error: ["model no longer registered"],
        });
        continue;
      }
      const model = firstPresent(modelHits, "recovered model required");
      if (isFlowOperation(runState.operation) === false) {
        this.obs.error(scope, "saga.recovery_operation_invalid", {
          runId: runState.runId,
          operation: runState.operation,
        });
        continue;
      }
      this.runs.set(runState.runId, {
        id: runState.runId,
        model,
        operation: runState.operation,
        entityId: runState.entityId,
        body: [runState.body],
        filter: this.restoredFilter(runState.filterJson, model),
        entity: [],
        created: [],
        results: [],
        page: [],
        signal: Running,
        starts: 1,
        emissions: { seen: 0, published: 0 },
        rooms: { names: [] },
      });
      restored += 1;
    }
    if (restored > 0) {
      this.obs.count(scope, "saga.recovered");
      this.obs.info(scope, "saga.recovered", { runs: restored });
    }
    return restored;
  }

  private async settleHttpRun(
    runId: string,
    run: FlowRun<ModelFieldsInput>,
    signal: Signal,
    entityId: string,
    rooms: readonly string[],
  ): Promise<void> {
    this.finalizeRun(runId, run, signal);
    await this.saveRunPhase(runId, run, signal);
    this.publishSettled({
      model: run.model.name,
      operation: run.operation,
      id: entityId,
      runId,
      signal,
      rooms,
    });
  }

  private async handleHttp(req: http.IncomingMessage, res: http.ServerResponse): Promise<void> {
    return await routeHttp(
      {
        registeredModels: this.registeredModels,
        runs: this.runs,
        runtimeFor: (traceId, model, entityId, operation) =>
          this.runtimeFor(traceId, model, entityId, operation),
        observability: (since) => this.observability(since),
        settleHttpRun: (runId, run, signal, entityId, rooms) =>
          this.settleHttpRun(runId, run, signal, entityId, rooms),
      },
      req,
      res,
    );
  }
}

export type AppInstance = App;

export function app<const E extends readonly ExternalDef[]>(config: AppConfig<E>): App<E> {
  if (z.looseObject({}).safeParse(config).success === false) {
    throw ValidationError.create("app config required");
  }
  if (Array.isArray(config.models) === false) {
    throw ValidationError.create("app models required");
  }
  if (config.models.length < 1) {
    throw ValidationError.create("app models required");
  }
  return App.create(config);
}
