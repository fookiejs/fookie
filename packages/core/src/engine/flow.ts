import { z } from "zod";
import { SpanStatusCode } from "@opentelemetry/api";
import { nestedEntityId } from "./ids.ts";
import { compensateRun } from "./compensation.ts";
import { flushPendingExternalEvents, runExternal } from "./outbox.ts";
import {
  requireModel,
  resolveModel,
  scopedRuntime,
  storeForModel,
  withWriteTransaction,
} from "./runtime.ts";
import type { EmissionCursor, RoomBox, Runtime } from "./runtime.ts";
import {
  DatabaseError,
  ModelFieldError,
  NotFoundError,
  PgEncodeError,
  ValidationError,
} from "../errors.ts";
import type { ExternalDef, InferExternalInputFrom, InferExternalOutputFrom } from "../external.ts";
import { entityMatchesFilter } from "../filter/match.ts";
import { assignFilterOp, copyFilterState, createFilter, emptyListPage } from "../filter/ops.ts";
import type { FilterState, FilterView, ListPage } from "../filter/ops.ts";
import type { FilterInput } from "../filter/schema.ts";
import { applyNumericPatches } from "../patch.ts";
import {
  createdEntity,
  declaredFieldsFrom,
  isCreateBody,
  mergeUpdateBody,
  stampSoftDelete,
  stampUpdate,
} from "../model.ts";
import type {
  EntityFieldsOf,
  FieldValue,
  FieldsMap,
  ModelDef,
  ModelFieldsInput,
  ModelRef,
  NormalizeModelFields,
  WritableBody,
} from "../model.ts";
import { createObservability, obsScope, traceSpan } from "../observability.ts";
import type { FlowObs, FlowTelemetry } from "../observability.ts";
import { relationTargetOf } from "../pg/naming.ts";
import { getEntity, persistEntity, commitDelete } from "../store/entity-io.ts";
import { Done, Failed, Running } from "../signal.ts";
import type { Signal } from "../signal.ts";
import { appendItem, catchValidation, firstPresent } from "../slot.ts";
import type { PgParam, PgRow } from "../pg/encode.ts";
import type { ScalarSchema } from "../types/type.ts";
import { entityRecordFromPlain, entityValueAt, mergeEntityRecords } from "../values.ts";
import type { EntityRecord, JsonObject } from "../values.ts";

export type ExternalResultKinds<T> = {
  running: { signal: "running" };
  failed: { signal: "failed" };
  done: { signal: "done"; output: T };
};

export type ExternalResult<T> = ExternalResultKinds<T>[keyof ExternalResultKinds<T>];

export type NestedResultKinds = {
  running: { signal: "running" };
  failed: { signal: "failed" };
  done: { signal: "done" };
  doneEntity: { signal: "done"; id: string; entity: EntityRecord };
  doneList: { signal: "done"; results: EntityRecord[] };
  doneIds: { signal: "done"; ids: string[] };
};

export type NestedResult = NestedResultKinds[keyof NestedResultKinds];

export type SystemEntity = {
  id: string;
  createdAt: string;
  updatedAt: string;
  isDeleted: boolean;
};

export type CreateResultKinds<E extends EntityRecord> = {
  running: { signal: "running"; id: string; runId: string };
  failed: { signal: "failed"; id: string; runId: string };
  done: { signal: "done"; id: string; runId: string; entity: E };
};

export type CreateResult<E extends EntityRecord> = CreateResultKinds<E>[keyof CreateResultKinds<E>];

export type ListResult<E extends EntityRecord> = {
  signal: Signal;
  runId: string;
  results: readonly E[];
};

export type MutationResultKinds = {
  running: { signal: "running"; id: string; runId: string };
  failed: { signal: "failed"; id: string; runId: string };
  done: { signal: "done"; id: string; runId: string };
};

export type MutationResult = MutationResultKinds[keyof MutationResultKinds];

export type UpdateResultKinds = {
  running: { signal: "running"; ids: string[]; runId: string };
  failed: { signal: "failed"; ids: string[]; runId: string };
  done: { signal: "done"; ids: string[]; runId: string };
};

export type UpdateResult = UpdateResultKinds[keyof UpdateResultKinds];

export function mutationResult(signal: Signal, id: string, runId: string): MutationResult {
  if (signal === Running) {
    return { signal: Running, id, runId };
  }
  if (signal === Failed) {
    return { signal: Failed, id, runId };
  }
  return { signal: Done, id, runId };
}

export function entityIdOf(entity: EntityRecord): string {
  const hits = entityValueAt(entity, "id");
  for (const idValue of hits) {
    const parsed = z.string().min(1).safeParse(idValue);
    if (parsed.success === true) {
      return parsed.data;
    }
  }
  throw ValidationError.create("entity id required");
}

export function entityIdsOf(entities: readonly EntityRecord[]): string[] {
  let ids: readonly string[] = [];
  for (const entity of entities) {
    ids = appendItem(ids, entityIdOf(entity));
  }
  return ids.slice();
}

export function updateResult(signal: Signal, ids: readonly string[], runId: string): UpdateResult {
  const copied = ids.slice();
  if (signal === Running) {
    return { signal: Running, ids: copied, runId };
  }
  if (signal === Failed) {
    return { signal: Failed, ids: copied, runId };
  }
  return { signal: Done, ids: copied, runId };
}

export type FlowModelOps = {
  create(target: ModelRef, body: EntityRecord): Promise<NestedResult>;
  list(target: ModelRef, filter: FilterInput): Promise<NestedResult>;
  update(target: ModelRef, filter: FilterInput, body: EntityRecord): Promise<NestedResult>;
  delete(target: ModelRef, input: { id: string; filter: FilterInput }): Promise<NestedResult>;
};

function nestedChild(rt: Runtime, target: ModelRef): readonly ModelDef<ModelFieldsInput>[] {
  const childHits = resolveModel(rt, target);
  if (childHits.length < 1) {
    return [];
  }
  const child = requireModel(childHits, "nested model missing");
  if (z.looseObject({}).safeParse(child).success === false) {
    return [];
  }
  return [child];
}

export type FlowPgOps = {
  pg: {
    query(sql: string, params: readonly PgParam[]): Promise<readonly PgRow[]>;
  };
};

export type FlowExternalOps = {
  external<I extends Record<string, ScalarSchema>, O extends Record<string, ScalarSchema>>(
    ext: ExternalDef<I, O>,
    input: InferExternalInputFrom<I>,
  ): Promise<ExternalResult<InferExternalOutputFrom<O>>>;
};

export type CreateFlow<F extends FieldsMap> = {
  id: string;
  body: WritableBody<F>;
} & FlowObs &
  FlowModelOps &
  FlowPgOps &
  FlowExternalOps;

export type ListFlow<F extends FieldsMap> = FlowObs &
  FlowModelOps &
  FlowPgOps &
  FlowExternalOps & {
    filter: FilterView<F>;
  };

export type UpdateFlow<F extends FieldsMap> = ListFlow<F> & {
  id: string;
  body: EntityRecord;
};

export type DeleteFlow<F extends FieldsMap> = ListFlow<F> & {
  id: string;
};

export function createFlowModelOps(
  rt: Runtime,
  parent: ModelDef<ModelFieldsInput>,
  parentEntityId: string,
  obs: FlowTelemetry,
): FlowObs & FlowModelOps {
  return {
    log: obs.log,
    metric: obs,
    trace: (name, run) => traceSpan(rt, name, run),
    create(target, body) {
      for (const child of nestedChild(rt, target)) {
        return runCreate(rt, child, body, parent, parentEntityId);
      }
      return Promise.resolve({ signal: Failed });
    },
    list(target, filter) {
      for (const child of nestedChild(rt, target)) {
        return runNestedList(rt, child, filter, parent, parentEntityId);
      }
      return Promise.resolve({ signal: Failed });
    },
    update(target, filter, body) {
      for (const child of nestedChild(rt, target)) {
        return runNestedUpdate(rt, child, filter, body, parent, parentEntityId);
      }
      return Promise.resolve({ signal: Failed });
    },
    delete(target, input) {
      for (const child of nestedChild(rt, target)) {
        return runNestedDelete(rt, child, input, parent, parentEntityId);
      }
      return Promise.resolve({ signal: Failed });
    },
  };
}

export function flowOpsOf(ops: FlowObs & FlowModelOps): FlowObs & FlowModelOps {
  if (z.looseObject({}).safeParse(ops).success === false) {
    throw ValidationError.create("flow ops required");
  }
  if (z.instanceof(Function).safeParse(ops.create).success === false) {
    throw ValidationError.create("flow create op required");
  }
  return {
    log: ops.log,
    metric: ops.metric,
    trace: ops.trace,
    create: ops.create,
    list: ops.list,
    update: ops.update,
    delete: ops.delete,
  };
}

export function flowPgOpsOf(rt: Runtime): FlowPgOps {
  return {
    pg: {
      query(sql, params) {
        if (z.string().min(1).safeParse(sql).success === false) {
          throw ValidationError.create("flow query sql required");
        }
        if (Array.isArray(params) === false) {
          throw ValidationError.create("flow query params required");
        }
        return rt.store.selectRows(sql, params);
      },
    },
  };
}

export function flowExternalOpsOf(rt: Runtime): FlowExternalOps {
  return {
    external(ext, input) {
      if (z.looseObject({}).safeParse(ext).success === false) {
        rt.obs.error(obsScope(rt), "external.definition_invalid", {});
        return Promise.resolve({ signal: Failed });
      }
      if (z.string().min(1).safeParse(ext.name).success === false) {
        rt.obs.error(obsScope(rt), "external.definition_invalid", {});
        return Promise.resolve({ signal: Failed });
      }
      return runExternal(rt, ext, input);
    },
  };
}

export function createFlowOf<F extends FieldsMap>(
  id: string,
  body: WritableBody<F>,
  ops: FlowObs & FlowModelOps,
  external: FlowExternalOps,
  pgOps: FlowPgOps,
): CreateFlow<F> {
  const flowOps = flowOpsOf(ops);
  return {
    id,
    body,
    pg: pgOps.pg,
    log: flowOps.log,
    metric: flowOps.metric,
    trace: flowOps.trace,
    create: flowOps.create,
    list: flowOps.list,
    update: flowOps.update,
    delete: flowOps.delete,
    external: external.external,
  };
}

export function listFlowOf<F extends FieldsMap>(
  filter: FilterView<F>,
  ops: FlowObs & FlowModelOps,
  external: FlowExternalOps,
  pgOps: FlowPgOps,
): ListFlow<F> {
  const flowOps = flowOpsOf(ops);
  return {
    pg: pgOps.pg,
    filter,
    log: flowOps.log,
    metric: flowOps.metric,
    trace: flowOps.trace,
    create: flowOps.create,
    list: flowOps.list,
    update: flowOps.update,
    delete: flowOps.delete,
    external: external.external,
  };
}

export function updateFlowOf<F extends FieldsMap>(
  id: string,
  body: EntityRecord,
  filter: FilterView<F>,
  ops: FlowObs & FlowModelOps,
  external: FlowExternalOps,
  pgOps: FlowPgOps,
): UpdateFlow<F> {
  const flowOps = flowOpsOf(ops);
  return {
    pg: pgOps.pg,
    id,
    body,
    filter,
    log: flowOps.log,
    metric: flowOps.metric,
    trace: flowOps.trace,
    create: flowOps.create,
    list: flowOps.list,
    update: flowOps.update,
    delete: flowOps.delete,
    external: external.external,
  };
}

export function deleteFlowOf<F extends FieldsMap>(
  id: string,
  filter: FilterView<F>,
  ops: FlowObs & FlowModelOps,
  external: FlowExternalOps,
  pgOps: FlowPgOps,
): DeleteFlow<F> {
  const flowOps = flowOpsOf(ops);
  return {
    pg: pgOps.pg,
    id,
    filter,
    log: flowOps.log,
    metric: flowOps.metric,
    trace: flowOps.trace,
    create: flowOps.create,
    list: flowOps.list,
    update: flowOps.update,
    delete: flowOps.delete,
    external: external.external,
  };
}

export type FlowOperationKinds = {
  create: "create";
  list: "list";
  update: "update";
  delete: "delete";
};

export type FlowOperation = FlowOperationKinds[keyof FlowOperationKinds];

export function isFlowOperation(operationText: string): operationText is FlowOperation {
  if (operationText === "create") {
    return true;
  }
  if (operationText === "list") {
    return true;
  }
  if (operationText === "update") {
    return true;
  }
  if (operationText === "delete") {
    return true;
  }
  return false;
}

export type FlowRun<D extends ModelFieldsInput = ModelFieldsInput> = {
  id: string;
  model: ModelDef<D>;
  operation: FlowOperation;
  entityId: string;
  body: readonly JsonObject[];
  filter: readonly FilterState[];
  entity: readonly EntityRecord[];
  created: readonly EntityRecord[];
  results: EntityRecord[];
  page: readonly ListPage[];
  signal: Signal;
  starts: number;
  emissions: EmissionCursor;
  rooms: RoomBox;
};

function relatesToParent(fieldValue: FieldValue, parentName: string): boolean {
  if (z.string().min(1).safeParse(parentName).success === false) {
    return false;
  }
  for (const target of relationTargetOf(fieldValue)) {
    if (target === parentName) {
      return true;
    }
  }
  return false;
}

export function bindRelationFields(
  child: ModelDef<ModelFieldsInput>,
  parent: ModelDef<ModelFieldsInput>,
  parentEntityId: string,
  body: EntityRecord,
): EntityRecord {
  const next = entityRecordFromPlain(body);
  const fields = declaredFieldsFrom(child.fields);
  for (const [key, value] of Object.entries(fields)) {
    if (relatesToParent(value, parent.name)) {
      next[key] = parentEntityId;
    }
  }
  return next;
}

export function bindRelationFilter(
  child: ModelDef<ModelFieldsInput>,
  parent: ModelDef<ModelFieldsInput>,
  parentEntityId: string,
  filter: FilterState,
): FilterState {
  const next = copyFilterState(filter);
  const fields = declaredFieldsFrom(child.fields);
  for (const [key, value] of Object.entries(fields)) {
    if (relatesToParent(value, parent.name)) {
      assignFilterOp(next, key, "eq", parentEntityId);
    }
  }
  return next;
}

export function entityMatchesParentRelation(
  child: ModelDef<ModelFieldsInput>,
  parent: ModelDef<ModelFieldsInput>,
  parentEntityId: string,
  entity: EntityRecord,
): boolean {
  const fields = declaredFieldsFrom(child.fields);
  for (const [key, value] of Object.entries(fields)) {
    if (relatesToParent(value, parent.name)) {
      const relatedValues = entityValueAt(entity, key);
      if (relatedValues.length < 1) {
        return false;
      }
      for (const related of relatedValues) {
        if (related !== parentEntityId) {
          return false;
        }
      }
    }
  }
  return true;
}

export async function runCreate<D extends ModelFieldsInput>(
  rt: Runtime,
  model: ModelDef<D>,
  body: EntityRecord,
  parent: ModelDef<ModelFieldsInput>,
  parentEntityId: string,
): Promise<NestedResult> {
  const bound = bindRelationFields(model, parent, parentEntityId, body);
  const validatedHits = catchValidation(() => model.validateCreateBody(bound));
  if (validatedHits.length < 1) {
    return { signal: Failed };
  }
  const validated = firstPresent(validatedHits, "validated body required");
  if (isCreateBody(model, validated) === false) {
    return { signal: Failed };
  }

  const entityId = nestedEntityId(rt.nestedSteps, parentEntityId, model.name);
  const flowBody: WritableBody<NormalizeModelFields<D>> = validated;

  const localRt = scopedRuntime(rt, model, entityId, "create");
  const obs = createObservability(localRt);
  const ops = createFlowModelOps(localRt, model, entityId, obs);
  const flow = createFlowOf<NormalizeModelFields<D>>(
    entityId,
    flowBody,
    ops,
    flowExternalOpsOf(localRt),
    flowPgOpsOf(localRt),
  );

  const signal = await localRt.obs.runSpan(
    obsScope(localRt),
    `${model.name.toLowerCase()}.create`,
    {},
    () => model.flow.create(flow),
  );

  if (signal === Done) {
    const stored = createdEntity(
      entityId,
      mergeUpdateBody(model, entityRecordFromPlain(flow.body)),
    );
    const ok = await persistEntity(rt, model, entityId, stored, true);
    if (ok === false) {
      return { signal: Failed };
    }
    localRt.obs.count(obsScope(localRt), "nested.create");
    return { signal: Done, id: entityId, entity: stored };
  }
  return toNestedResult(signal);
}

export function toNestedResult(signal: Signal): NestedResult {
  if (signal === Done) {
    return { signal: Done };
  }
  if (signal === Running) {
    return { signal: Running };
  }
  return { signal: Failed };
}

export async function runNestedList(
  rt: Runtime,
  model: ModelDef<ModelFieldsInput>,
  filter: FilterInput,
  parent: ModelDef<ModelFieldsInput>,
  parentEntityId: string,
): Promise<NestedResult> {
  const validatedHits = catchValidation(() => model.validateListFilter(filter));
  if (validatedHits.length < 1) {
    return { signal: Failed };
  }
  const validated = firstPresent(validatedHits, "nested list filter required");

  const filterState = bindRelationFilter(model, parent, parentEntityId, copyFilterState(validated));
  const localRt = scopedRuntime(rt, model, parentEntityId, "list");
  const obs = createObservability(localRt);
  const ops = createFlowModelOps(localRt, parent, parentEntityId, obs);
  const flow = listFlowOf(
    createFilter(model.fields, filterState),
    ops,
    flowExternalOpsOf(localRt),
    flowPgOpsOf(localRt),
  );

  const signal = await localRt.obs.runSpan(
    obsScope(localRt),
    `${model.name.toLowerCase()}.list`,
    {},
    () => model.flow.list(flow),
  );
  if (signal === Done) {
    try {
      const rows = await storeForModel(rt, model).queryEntities(
        model,
        filterState,
        emptyListPage(),
      );
      localRt.obs.count(obsScope(localRt), "nested.list");
      return { signal: Done, results: rows };
    } catch (err) {
      if (err instanceof DatabaseError || err instanceof PgEncodeError) {
        logDatabaseFailure(rt);
      }
      return { signal: Failed };
    }
  }
  return toNestedResult(signal);
}

async function persistMatchedUpdate(
  rt: Runtime,
  model: ModelDef<ModelFieldsInput>,
  existing: EntityRecord,
  bodyValid: JsonObject,
  flowBody: EntityRecord,
  filterState: FilterState,
  parent: readonly ModelDef<ModelFieldsInput>[],
  parentEntityId: readonly string[],
): Promise<readonly EntityRecord[]> {
  const entityId = entityIdOf(existing);
  const entityRt = scopedRuntime(rt, model, entityId, "update");
  let locked: EntityRecord;
  try {
    locked = await getEntity(entityRt, model, entityId, ["write"]);
  } catch (err) {
    if (err instanceof NotFoundError) {
      return [];
    }
    throw err;
  }
  if (entityIdOf(locked) !== entityId) {
    return [];
  }
  const patchedHits = catchValidation(() => applyNumericPatches(locked, bodyValid));
  if (patchedHits.length < 1) {
    throw ValidationError.create("numeric patch failed");
  }
  const patched = firstPresent(patchedHits, "patched entity required");
  let stored = stampUpdate(
    patched,
    mergeUpdateBody(
      model,
      entityRecordFromPlain(mergeEntityRecords(entityRecordFromPlain(bodyValid), flowBody)),
    ),
  );
  for (const parentModel of parent) {
    for (const boundId of parentEntityId) {
      stored = bindRelationFields(model, parentModel, boundId, stored);
    }
  }
  if (
    entityMatchesFilter(model, locked, filterState) === false &&
    entityMatchesFilter(model, stored, filterState) === false
  ) {
    return [];
  }
  const ok = await persistEntity(entityRt, model, entityId, stored);
  if (ok === false) {
    throw DatabaseError.create("persist failed");
  }
  return [stored];
}

async function updateOneEntity(
  rt: Runtime,
  model: ModelDef<ModelFieldsInput>,
  existing: EntityRecord,
  bodyValid: JsonObject,
  callerFilter: FilterState,
  parent: readonly ModelDef<ModelFieldsInput>[],
  parentEntityId: readonly string[],
): Promise<{ signal: Signal; stored: readonly EntityRecord[] }> {
  const entityId = entityIdOf(existing);
  const entityRt = scopedRuntime(rt, model, entityId, "update");
  const obs = createObservability(entityRt);
  const filterState = copyFilterState(callerFilter);
  const ops = createFlowModelOps(entityRt, model, entityId, obs);
  const updateBody = mergeUpdateBody(model, bodyValid);
  const flow = updateFlowOf(
    entityId,
    updateBody,
    createFilter(model.fields, filterState),
    ops,
    flowExternalOpsOf(entityRt),
    flowPgOpsOf(entityRt),
  );
  const signal = await model.flow.update(flow);
  if (signal !== Done) {
    return { signal, stored: [] };
  }
  try {
    const storedHits = await persistMatchedUpdate(
      rt,
      model,
      existing,
      bodyValid,
      entityRecordFromPlain(flow.body),
      filterState,
      parent,
      parentEntityId,
    );
    return { signal: Done, stored: storedHits };
  } catch {
    return { signal: Failed, stored: [] };
  }
}

function matchingEntities(
  model: ModelDef<ModelFieldsInput>,
  rows: readonly EntityRecord[],
  filterState: FilterState,
): EntityRecord[] {
  let matched: readonly EntityRecord[] = [];
  for (const existing of rows) {
    if (entityMatchesFilter(model, existing, filterState) === true) {
      matched = appendItem(matched, existing);
    }
  }
  return matched.slice();
}

export async function runNestedUpdate<D extends ModelFieldsInput>(
  rt: Runtime,
  model: ModelDef<D>,
  filter: FilterInput,
  body: EntityRecord,
  parent: ModelDef<ModelFieldsInput>,
  parentEntityId: string,
): Promise<NestedResult> {
  const filterValidHits = catchValidation(() => model.validateUpdateFilter(filter));
  const bodyValidHits = catchValidation(() => model.validateUpdateBody(body));
  if (filterValidHits.length < 1 || bodyValidHits.length < 1) {
    return { signal: Failed };
  }
  const filterValid = firstPresent(filterValidHits, "nested update filter required");
  const bodyValid = firstPresent(bodyValidHits, "nested update body required");
  const callerFilter = bindRelationFilter(
    model,
    parent,
    parentEntityId,
    copyFilterState(filterValid),
  );
  const localRt = scopedRuntime(rt, model, parentEntityId, "update");
  let rows: EntityRecord[];
  try {
    rows = await storeForModel(rt, model).queryEntities(model, callerFilter, emptyListPage());
  } catch (err) {
    if (err instanceof DatabaseError || err instanceof PgEncodeError) {
      logDatabaseFailure(rt);
    }
    return { signal: Failed };
  }
  let storedAll: readonly EntityRecord[] = [];
  for (const existing of matchingEntities(model, rows, callerFilter)) {
    const one = await updateOneEntity(
      rt,
      model,
      existing,
      bodyValid,
      callerFilter,
      [parent],
      [parentEntityId],
    );
    if (one.signal !== Done) {
      return toNestedResult(one.signal);
    }
    for (const stored of one.stored) {
      storedAll = appendItem(storedAll, stored);
    }
  }
  localRt.obs.count(obsScope(localRt), "nested.update");
  return { signal: Done, ids: entityIdsOf(storedAll) };
}

export async function runNestedDelete(
  rt: Runtime,
  model: ModelDef<ModelFieldsInput>,
  input: { id: string; filter: FilterInput },
  parent: ModelDef<ModelFieldsInput>,
  parentEntityId: string,
): Promise<NestedResult> {
  const filterValidHits = catchValidation(() => model.validateDeleteFilter(input.filter));
  if (filterValidHits.length < 1) {
    return { signal: Failed };
  }
  const filterValid = firstPresent(filterValidHits, "filter required");

  let existing: EntityRecord;
  try {
    existing = await getEntity(rt, model, input.id);
  } catch {
    return { signal: Failed };
  }
  if (existing.id !== input.id) {
    return { signal: Failed };
  }
  if (entityMatchesParentRelation(model, parent, parentEntityId, existing) === false) {
    return { signal: Failed };
  }
  if (entityMatchesFilter(model, existing, filterValid) === false) {
    return { signal: Failed };
  }
  const localRt = scopedRuntime(rt, model, input.id, "delete");
  const obs = createObservability(localRt);
  const filterState = copyFilterState(filterValid);
  const ops = createFlowModelOps(localRt, model, input.id, obs);
  const flow = deleteFlowOf(
    input.id,
    createFilter(model.fields, filterState),
    ops,
    flowExternalOpsOf(localRt),
    flowPgOpsOf(localRt),
  );

  const signal = await localRt.obs.runSpan(
    obsScope(localRt),
    `${model.name.toLowerCase()}.delete`,
    {},
    () => model.flow.delete(flow),
  );
  if (signal === Done) {
    if (entityMatchesFilter(model, existing, filterState) === false) {
      return { signal: Failed };
    }
    const ok = await commitDelete(rt, model, input.id, existing);
    if (ok === false) {
      return { signal: Failed };
    }
    localRt.obs.count(obsScope(localRt), "nested.delete");
  }
  return toNestedResult(signal);
}

export async function executeRunMutation<D extends ModelFieldsInput>(
  rt: Runtime,
  run: FlowRun<D>,
): Promise<Signal> {
  if (run.operation === "create") {
    if (run.body.length < 1) {
      return Failed;
    }
    const createBodyInput = firstPresent(run.body, "create body required");
    const validatedHits = catchValidation(() => run.model.validateCreateBody(createBodyInput));
    if (validatedHits.length < 1) {
      return Failed;
    }
    const validated = firstPresent(validatedHits, "validated body required");
    if (isCreateBody(run.model, validated) === false) {
      return Failed;
    }

    const createBody: WritableBody<NormalizeModelFields<D>> = validated;
    const localRt = scopedRuntime(rt, run.model, run.entityId, run.operation);
    const obs = createObservability(localRt);
    const ops = createFlowModelOps(localRt, run.model, run.entityId, obs);
    const flow = createFlowOf<NormalizeModelFields<D>>(
      run.entityId,
      createBody,
      ops,
      flowExternalOpsOf(localRt),
      flowPgOpsOf(localRt),
    );

    const signal = await run.model.flow.create(flow);
    if (signal === Done) {
      const stored = createdEntity(
        run.entityId,
        mergeUpdateBody(run.model, entityRecordFromPlain(flow.body)),
      );
      const ok = await persistEntity(rt, run.model, run.entityId, stored, true);
      if (ok === false) {
        return Failed;
      }
      run.entity = [stored];
      run.created = [stored];
    }
    return signal;
  }

  if (run.operation === "update") {
    if (run.body.length < 1 || run.filter.length < 1) {
      return Failed;
    }
    const updateFilterInput = firstPresent(run.filter, "update filter required");
    const updateBodyInput = firstPresent(run.body, "update body required");
    const filterValidHits = catchValidation(() =>
      run.model.validateUpdateFilter(updateFilterInput),
    );
    const bodyValidHits = catchValidation(() => run.model.validateUpdateBody(updateBodyInput));
    if (filterValidHits.length < 1 || bodyValidHits.length < 1) {
      return Failed;
    }
    const filterValid = firstPresent(filterValidHits, "update filter required");
    const bodyValid = firstPresent(bodyValidHits, "update body required");
    const callerFilter = copyFilterState(filterValid);
    let rows: EntityRecord[];
    try {
      rows = await storeForModel(rt, run.model).queryEntities(
        run.model,
        callerFilter,
        emptyListPage(),
      );
    } catch (err) {
      if (err instanceof DatabaseError || err instanceof PgEncodeError) {
        logDatabaseFailure(rt);
      }
      return Failed;
    }
    let storedAll: readonly EntityRecord[] = [];
    for (const existing of matchingEntities(run.model, rows, callerFilter)) {
      const one = await updateOneEntity(rt, run.model, existing, bodyValid, callerFilter, [], []);
      if (one.signal !== Done) {
        return one.signal;
      }
      for (const stored of one.stored) {
        storedAll = appendItem(storedAll, stored);
      }
    }
    run.entity = storedAll.slice();
    return Done;
  }

  if (run.filter.length < 1) {
    return Failed;
  }
  const deleteFilterInput = firstPresent(run.filter, "delete filter required");
  const filterValidHits = catchValidation(() => run.model.validateDeleteFilter(deleteFilterInput));
  if (filterValidHits.length < 1) {
    return Failed;
  }
  const filterValid = firstPresent(filterValidHits, "filter required");
  const localRt = scopedRuntime(rt, run.model, run.entityId, run.operation);
  const obs = createObservability(localRt);
  const filterState = copyFilterState(filterValid);
  const ops = createFlowModelOps(localRt, run.model, run.entityId, obs);
  const flow = deleteFlowOf<EntityFieldsOf<D>>(
    run.entityId,
    createFilter(run.model.fields, filterState),
    ops,
    flowExternalOpsOf(localRt),
    flowPgOpsOf(localRt),
  );
  let existing: EntityRecord;
  try {
    existing = await getEntity(rt, run.model, run.entityId, ["write"]);
  } catch {
    return Failed;
  }
  if (existing.id !== run.entityId) {
    return Failed;
  }
  if (entityMatchesFilter(run.model, existing, filterValid) === false) {
    return Failed;
  }
  run.entity = [existing];
  const signal = await run.model.flow.delete(flow);
  if (signal === Done) {
    if (entityMatchesFilter(run.model, existing, filterState) === false) {
      return Failed;
    }
    const stored = stampSoftDelete(existing);
    const ok = await commitDelete(rt, run.model, run.entityId, existing);
    if (ok === false) {
      return Failed;
    }
    run.entity = [stored];
  }
  return signal;
}

export function logDatabaseFailure(rt: Runtime): void {
  const reason = firstPresent(rt.dbLastError(), "database failed without a driver message");
  rt.obs.error(obsScope(rt), "database unavailable", { reason });
}

export async function executeRun<D extends ModelFieldsInput>(
  rt: Runtime,
  run: FlowRun<D>,
): Promise<Signal> {
  const metricRt = scopedRuntime(rt, run.model, run.entityId, run.operation);
  const scope = obsScope(metricRt);
  const spanName = `${run.model.name.toLowerCase()}.${run.operation}`;
  return await rt.obs.runSpan(scope, spanName, {}, async (span) => {
    const dbOk = await rt.awaitDb();
    if (dbOk === false) {
      logDatabaseFailure(metricRt);
      metricRt.obs.count(obsScope(metricRt), "operation.failed");
      span.setAttribute("signal", Failed);
      span.setStatus({ code: SpanStatusCode.ERROR, message: "database unavailable" });
      return Failed;
    }
    rt.clearDbError();
    const startedAt = Date.now();
    run.emissions.seen = 0;
    if (run.starts < 1) {
      rt.obs.count(scope, "operation.started");
    } else {
      rt.obs.count(scope, "operation.resumed");
    }
    run.starts = run.starts + 1;
    let signal: Signal = Failed;

    if (run.operation === "list") {
      if (run.filter.length < 1) {
        rt.obs.measure(scope, "operation.duration", Date.now() - startedAt);
        rt.obs.count(scope, "operation.failed");
        span.setAttribute("signal", Failed);
        span.setStatus({ code: SpanStatusCode.ERROR, message: "invalid filter" });
        return Failed;
      }
      const listFilterInput = firstPresent(run.filter, "list filter required");
      const validatedHits = catchValidation(() => run.model.validateListFilter(listFilterInput));
      if (validatedHits.length < 1) {
        rt.obs.measure(scope, "operation.duration", Date.now() - startedAt);
        rt.obs.count(scope, "operation.failed");
        span.setAttribute("signal", Failed);
        span.setStatus({ code: SpanStatusCode.ERROR, message: "invalid filter" });
        return Failed;
      }
      const validated = firstPresent(validatedHits, "list filter required");
      const localRt = scopedRuntime(rt, run.model, run.entityId, run.operation);
      const obs = createObservability(localRt);
      const filterState = copyFilterState(validated);
      const ops = createFlowModelOps(localRt, run.model, run.entityId, obs);
      const flow = listFlowOf<EntityFieldsOf<D>>(
        createFilter(run.model.fields, filterState),
        ops,
        flowExternalOpsOf(localRt),
        flowPgOpsOf(localRt),
      );
      signal = await run.model.flow.list(flow);
      if (signal === Failed) {
        rt.obs.measure(scope, "operation.duration", Date.now() - startedAt);
        rt.obs.count(scope, "operation.failed");
        span.setAttribute("signal", Failed);
        span.setStatus({ code: SpanStatusCode.ERROR, message: "flow failed" });
        return Failed;
      }
      if (signal === Done) {
        try {
          const page = run.page[0];
          if (page === undefined) {
            run.results = await rt.store.queryEntities(run.model, filterState, emptyListPage());
          } else {
            if (Array.isArray(page.order) === false) {
              throw ValidationError.create("list page order required");
            }
            run.results = await rt.store.queryEntities(run.model, filterState, page);
          }
        } catch (err) {
          rt.obs.measure(scope, "operation.duration", Date.now() - startedAt);
          span.setAttribute("signal", Failed);
          if (err instanceof DatabaseError || err instanceof PgEncodeError) {
            logDatabaseFailure(metricRt);
            metricRt.obs.count(obsScope(metricRt), "operation.failed");
            span.setStatus({ code: SpanStatusCode.ERROR, message: "database unavailable" });
          } else if (err instanceof ModelFieldError) {
            rt.obs.count(scope, "operation.failed");
            span.setStatus({ code: SpanStatusCode.ERROR, message: "invalid filter" });
          } else {
            rt.obs.count(scope, "operation.failed");
            span.setStatus({ code: SpanStatusCode.ERROR, message: "invalid filter" });
          }
          return Failed;
        }
      }
    } else {
      signal = await withWriteTransaction(rt, (txRt) => executeRunMutation(txRt, run));
    }

    rt.obs.measure(scope, "operation.duration", Date.now() - startedAt);
    span.setAttribute("signal", signal);
    if (signal === Done) {
      rt.obs.count(scope, "operation.completed");
    } else if (signal === Failed) {
      rt.obs.count(scope, "operation.failed");
      span.setStatus({ code: SpanStatusCode.ERROR, message: "flow failed" });
      await compensateRun(metricRt, run.id);
      await flushPendingExternalEvents(metricRt);
    } else {
      rt.obs.count(scope, "operation.suspended");
      rt.obs.info(scope, "flow.suspended", { runId: run.id });
    }
    return signal;
  });
}

export function resolveModelByName(
  models: ReadonlyArray<ModelDef<ModelFieldsInput>>,
  name: string,
): ModelDef<ModelFieldsInput>[] {
  const lowered = name.toLowerCase();
  for (const model of models) {
    if (model.name.toLowerCase() === lowered) {
      return [model];
    }
  }
  return [];
}
