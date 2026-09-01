import { z } from "zod";
import { beforeEach, describe, it } from "node:test";
import assert from "node:assert/strict";
import { Done, Model, app } from "../src/index.ts";
import type { OperationEvent } from "../src/index.ts";
import { MockDb, LiveApps } from "./mock-db.ts";
import { Postgres } from "./engines.ts";
import { httpPost } from "./http-client.ts";

const child = Model({
  name: "CursorChild",
  fields: { label: z.string() },
  flow: {
    async create() {
      return Done;
    },
    async list() {
      return Done;
    },
    async update() {
      return Done;
    },
    async delete() {
      return Done;
    },
  },
});

const parent = Model({
  name: "CursorParent",
  fields: { email: z.string().email() },
  flow: {
    async create(flow) {
      flow.log("parent created", {});
      const nested = await flow.create(child, { label: "n" });
      return nested.signal;
    },
    async list() {
      return Done;
    },
    async update() {
      return Done;
    },
    async delete() {
      return Done;
    },
  },
});

describe("observability cursor, parents and rooms", () => {
  let db: MockDb;
  let apps: LiveApps;

  beforeEach(() => {
    db = new MockDb();
    apps = new LiveApps();
  });

  function boot() {
    return apps.track(
      app({
        listen: "0",
        database: Postgres("postgres://mock", [db]),
        models: [parent, child],
        externals: [] as const,
        onExternalEvent: async () => {},
      }),
    );
  }

  it("gives every entry a place in one monotonic sequence", async () => {
    const fookie = boot();
    await fookie.create(parent, { email: "seq@x.com" });

    const page = fookie.observability(0);
    const seqs: number[] = [];
    for (const logEntry of page.logs) {
      seqs.push(logEntry.seq);
    }
    for (const metricEntry of page.metrics) {
      seqs.push(metricEntry.seq);
    }
    for (const spanEntry of page.spans) {
      seqs.push(spanEntry.seq);
    }
    assert.ok(seqs.length > 0);
    assert.equal(new Set(seqs).size, seqs.length, "no sequence number repeats across buffers");
    assert.equal(page.oldestSeq, Math.min(...seqs));
    assert.equal(page.nextSeq, Math.max(...seqs));
    await apps.shutdown();
  });

  it("returns only what the cursor has not seen", async () => {
    const fookie = boot();
    await fookie.create(parent, { email: "one@x.com" });
    const first = fookie.observability(0);

    await fookie.create(parent, { email: "two@x.com" });
    const second = fookie.observability(first.nextSeq);

    for (const logEntry of second.logs) {
      assert.ok(logEntry.seq > first.nextSeq);
    }
    assert.ok(second.nextSeq > first.nextSeq);
    const empty = fookie.observability(second.nextSeq);
    assert.equal(empty.logs.length, 0);
    assert.equal(empty.spans.length, 0);
    await apps.shutdown();
  });

  it("records who called a nested operation instead of leaving it to be guessed", async () => {
    const fookie = boot();
    await fookie.create(parent, { email: "nest@x.com" });

    const spans = fookie.observability(0).spans;
    const nested = spans.filter((spanEntry) => spanEntry.model === "CursorChild");
    assert.ok(nested.length > 0, "the nested create produced a span");
    for (const spanEntry of nested) {
      assert.deepEqual(spanEntry.parentModel, ["CursorParent"]);
      assert.equal(spanEntry.parentEntityId.length, 1);
    }

    const roots = spans.filter((spanEntry) => spanEntry.name === "cursorparent.create");
    for (const spanEntry of roots) {
      assert.deepEqual(spanEntry.parentModel, [], "a root operation has no parent");
    }
    await apps.shutdown();
  });

  it("carries metric entity ids and span attributes", async () => {
    const fookie = boot();
    await fookie.create(parent, { email: "attr@x.com" });
    const page = fookie.observability(0);

    for (const metricEntry of page.metrics) {
      assert.equal(typeof metricEntry.entityId === "string", true);
    }
    const withModel = page.spans.filter((spanEntry) => spanEntry.attributes.model !== undefined);
    assert.ok(withModel.length > 0, "span attributes reach the buffer");
    await apps.shutdown();
  });

  it("delivers a settled event without rooms", async () => {
    const fookie = boot();
    const seen: OperationEvent[] = [];
    const subscription = fookie.onOperationSettled((event) => {
      seen.push(event);
    });

    const created = await fookie.create(parent, { email: "room@x.com" });
    assert.equal(seen.length, 1);
    for (const event of seen) {
      assert.equal(event.model, "CursorParent");
      assert.equal(event.operation, "create");
      assert.equal(event.signal, created.signal);
      assert.equal("rooms" in event, false);
    }

    assert.equal(subscription.stop(), true);
    await fookie.create(parent, { email: "after@x.com" });
    assert.equal(seen.length, 1, "a stopped listener hears nothing more");
    await apps.shutdown();
  });

  it("survives a listener that throws", async () => {
    const fookie = boot();
    fookie.onOperationSettled(() => {
      throw new Error("subscriber exploded");
    });

    const created = await fookie.create(parent, { email: "boom@x.com" });
    assert.equal(created.signal, "done", "a broken subscriber must not fail the run");
    await apps.shutdown();
  });

  it("polls observability deltas over http", async () => {
    const fookie = boot();
    fookie.run();
    const portHits = await fookie.listening();
    assert.equal(portHits.length > 0, true);
    const port = portHits[0];
    await fookie.create(parent, { email: "http-obs@x.com" });
    const first = fookie.observability(0);
    const polled = await httpPost(port, "/realtime/observability", { since: 0 });
    assert.equal(polled.status, 200);
    assert.equal(Array.isArray(polled.json.logs), true);
    assert.equal(typeof polled.json.nextSeq === "number", true);
    assert.ok(Number(polled.json.nextSeq) >= first.nextSeq);
    await apps.shutdown();
  });
});
