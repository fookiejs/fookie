import { z } from "zod";
import http from "node:http";
import {
  entityIdsOf,
  executeRun,
  mutationResult,
  resolveModelByName,
  updateResult,
} from "./engine/flow.ts";
import type { FlowRun } from "./engine/flow.ts";
import { uuidV7 } from "./engine/ids.ts";
import type { Runtime } from "./engine/runtime.ts";
import {
  filterFromPayload,
  pathPartAt,
  pathPartsFrom,
  readJsonBody,
  recordFromPayload,
  sendJson,
} from "./http.ts";
import type { ModelDef, ModelFieldsInput } from "./model.ts";
import { catchValidation, firstPresent } from "./slot.ts";
import { uuidSchema } from "./types/pg-literals.ts";
import { Done, Failed, Running } from "./signal.ts";
import type { Signal } from "./signal.ts";
import type { ObservabilityPage } from "./observability.ts";

export type RegisteredModel = ModelDef<ModelFieldsInput>;

export type RouterPorts = {
  registeredModels: readonly RegisteredModel[];
  runs: Map<string, FlowRun<ModelFieldsInput>>;
  runtimeFor(
    traceId: string,
    model: ModelDef<ModelFieldsInput>,
    entityId: string,
    operation: string,
  ): Runtime;
  observability: (since: number) => ObservabilityPage;
  settleHttpRun: (
    runId: string,
    run: FlowRun<ModelFieldsInput>,
    signal: Signal,
    entityId: string,
    rooms: readonly string[],
  ) => Promise<void>;
};

export async function routeHttp(
  ports: RouterPorts,
  req: http.IncomingMessage,
  res: http.ServerResponse,
): Promise<void> {
  if (req.method !== "POST") {
    sendJson(res, 405, { error: "method not allowed" });
    return;
  }
  const payloadHits = await readJsonBody(req);
  if (payloadHits.length < 1) {
    sendJson(res, 400, { error: "invalid body" });
    return;
  }
  const payload = firstPresent(payloadHits, "invalid body");
  const requestUrlParsed = z.string().safeParse(req.url);
  if (requestUrlParsed.success === false) {
    sendJson(res, 404, { error: "not found" });
    return;
  }
  const url = new URL(requestUrlParsed.data, "http://local");
  const parts = pathPartsFrom(url.pathname);
  const routeHeadHits = pathPartAt(parts, 0);
  const routeNextHits = pathPartAt(parts, 1);
  if (routeHeadHits[0] === "realtime" && routeNextHits[0] === "observability") {
    if (parts.length !== 2) {
      sendJson(res, 404, { error: "not found" });
      return;
    }
    const sinceParsed = z.number().int().nonnegative().safeParse(payload.since);
    if (sinceParsed.success === false) {
      sendJson(res, 400, { error: "invalid since cursor" });
      return;
    }
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify(ports.observability(sinceParsed.data)));
    return;
  }
  if (parts.length < 2) {
    sendJson(res, 404, { error: "not found" });
    return;
  }
  const modelNameHits = pathPartAt(parts, 0);
  const actionHits = pathPartAt(parts, 1);
  if (modelNameHits.length < 1 || actionHits.length < 1) {
    sendJson(res, 404, { error: "not found" });
    return;
  }
  const modelName = firstPresent(modelNameHits, "model name required");
  const action = firstPresent(actionHits, "action required");
  const modelHits = resolveModelByName(ports.registeredModels, modelName);
  if (modelHits.length < 1) {
    sendJson(res, 404, { error: "model not found" });
    return;
  }
  const model = firstPresent(modelHits, "model required");
  if (action === "create") {
    if (parts.length !== 2) {
      sendJson(res, 404, { error: "not found" });
      return;
    }
    const bodyHits = recordFromPayload(payload, "body");
    if (bodyHits.length < 1) {
      sendJson(res, 400, { error: "invalid body" });
      return;
    }
    const body = firstPresent(bodyHits, "http body required");
    const validatedHits = catchValidation(() => model.validateCreateBody(body));
    if (validatedHits.length < 1) {
      sendJson(res, 400, { error: "invalid body" });
      return;
    }
    const validated = firstPresent(validatedHits, "validated body required");
    const runId = uuidV7();
    const entityId = uuidV7();
    const run: FlowRun<ModelFieldsInput> = {
      id: runId,
      model,
      operation: "create",
      entityId,
      body: [validated],
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
    ports.runs.set(runId, run);
    const rt = ports.runtimeFor(runId, model, entityId, "create");
    const signal = await executeRun(rt, run);
    await ports.settleHttpRun(runId, run, signal, entityId, rt.rooms.names);
    if (signal === Done) {
      for (const created of run.created) {
        sendJson(res, 200, { signal: Done, id: entityId, runId, entity: created });
        return;
      }
    }
    if (signal === Running) {
      sendJson(res, 200, { signal: Running, id: entityId, runId });
      return;
    }
    sendJson(res, 200, { signal: Failed, id: entityId, runId });
    return;
  }
  if (action === "list") {
    if (parts.length !== 2) {
      sendJson(res, 404, { error: "not found" });
      return;
    }
    const listFilterHits = filterFromPayload(model, payload, "list");
    if (listFilterHits.length < 1) {
      sendJson(res, 400, { error: "invalid filter" });
      return;
    }
    const filter = firstPresent(listFilterHits, "invalid filter");
    const runId = uuidV7();
    const run: FlowRun<ModelFieldsInput> = {
      id: runId,
      model,
      operation: "list",
      entityId: runId,
      body: [],
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
    ports.runs.set(runId, run);
    const listRt = ports.runtimeFor(runId, model, runId, "list");
    const signal = await executeRun(listRt, run);
    await ports.settleHttpRun(runId, run, signal, runId, listRt.rooms.names);
    sendJson(res, 200, { signal, runId, results: run.results });
    return;
  }
  if (action === "update") {
    if (parts.length !== 2) {
      sendJson(res, 404, { error: "not found" });
      return;
    }
    const updateFilterHits = filterFromPayload(model, payload, "update");
    if (updateFilterHits.length < 1) {
      sendJson(res, 400, { error: "invalid filter" });
      return;
    }
    const filter = firstPresent(updateFilterHits, "invalid filter");
    const bodyHits = recordFromPayload(payload, "body");
    if (bodyHits.length < 1) {
      sendJson(res, 400, { error: "invalid body" });
      return;
    }
    const updateBody = firstPresent(bodyHits, "http update body required");
    const bodyValidHits = catchValidation(() => model.validateUpdateBody(updateBody));
    if (bodyValidHits.length < 1) {
      sendJson(res, 400, { error: "invalid body" });
      return;
    }
    const bodyValid = firstPresent(bodyValidHits, "update body required");
    const runId = uuidV7();
    const run: FlowRun<ModelFieldsInput> = {
      id: runId,
      model,
      operation: "update",
      entityId: runId,
      body: [bodyValid],
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
    ports.runs.set(runId, run);
    const updateRt = ports.runtimeFor(runId, model, runId, "update");
    const signal = await executeRun(updateRt, run);
    await ports.settleHttpRun(runId, run, signal, runId, updateRt.rooms.names);
    sendJson(res, 200, updateResult(signal, entityIdsOf(run.entity), runId));
    return;
  }
  if (parts.length !== 3) {
    sendJson(res, 404, { error: "not found" });
    return;
  }
  const entityIdHits = pathPartAt(parts, 1);
  const mutationHits = pathPartAt(parts, 2);
  if (entityIdHits.length < 1 || mutationHits.length < 1) {
    sendJson(res, 404, { error: "not found" });
    return;
  }
  const entityId = firstPresent(entityIdHits, "entity id required");
  const mutation = firstPresent(mutationHits, "mutation required");
  if (mutation !== "delete") {
    sendJson(res, 404, { error: "not found" });
    return;
  }
  if (uuidSchema.safeParse(entityId).success === false) {
    sendJson(res, 400, { error: "invalid id" });
    return;
  }
  if (mutation === "delete") {
    const deleteFilterHits = filterFromPayload(model, payload, "delete");
    if (deleteFilterHits.length < 1) {
      sendJson(res, 400, { error: "invalid filter" });
      return;
    }
    const filter = firstPresent(deleteFilterHits, "invalid filter");
    const runId = uuidV7();
    const run: FlowRun<ModelFieldsInput> = {
      id: runId,
      model,
      operation: "delete",
      entityId,
      body: [],
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
    ports.runs.set(runId, run);
    const deleteRt = ports.runtimeFor(runId, model, entityId, "delete");
    const signal = await executeRun(deleteRt, run);
    await ports.settleHttpRun(runId, run, signal, entityId, deleteRt.rooms.names);
    sendJson(res, 200, mutationResult(signal, entityId, runId));
  }
}
