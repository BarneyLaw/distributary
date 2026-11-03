import assert from "node:assert/strict";
import { test } from "node:test";
import { createCaptionStore } from "../captions.js";

function memoryStorage() {
  const entries = new Map();
  return {
    getItem: key => entries.get(key) ?? null,
    setItem: (key, value) => entries.set(key, value),
    removeItem: key => entries.delete(key),
    entries
  };
}

function fixture(t) {
  let now = 1_000_000;
  t.mock.method(Date, "now", () => now);
  const storage = memoryStorage();
  const options = { url: "ws://localhost:8765", session: "L1", storage };
  const caption = (segment, overrides = {}) => ({
    type: "CAPTION_UPDATE", session: "L1", segment, revision: 1,
    source_ts: now / 1000, final: true, text: `Caption ${segment}`, ...overrides
  });
  return {
    storage, options, caption,
    advance: seconds => { now += seconds * 1000; },
    reload: () => createCaptionStore(options),
    saved: () => JSON.parse([...storage.entries.values()][0])
  };
}

test("reload restores finalized and provisional captions with the final resume position", t => {
  const f = fixture(t);
  const store = f.reload();
  const final = f.caption(1, { revision: 3 });
  const provisional = f.caption(2, { final: false, revision: 2 });
  store.acceptUpdate(final);
  store.acceptUpdate(provisional);

  const restored = f.reload();
  assert.deepEqual(restored.getCaptions(), [final, provisional]);
  assert.deepEqual(restored.getHistory(), [final]);
  assert.deepEqual(restored.getResumePosition(), { last_segment: 1, last_revision: 3 });
  assert.equal(restored.acceptUpdate(final), null);
  assert.equal(restored.acceptUpdate({ ...provisional, revision: 1 }), null);
  assert.ok(restored.acceptUpdate({ ...provisional, revision: 3, final: true }));
  assert.deepEqual(f.reload().getResumePosition(), { last_segment: 2, last_revision: 3 });
});

test("refresh preserves the original expiration deadline, including its boundary", t => {
  const f = fixture(t);
  f.reload().acceptUpdate(f.caption(1));
  f.advance(50);
  const restored = f.reload();
  assert.equal(restored.getCaptions()[0].source_ts, 1000);
  f.advance(10);
  assert.equal(restored.getCaptions().length, 1);
  f.advance(0.001);
  restored.purgeExpired();
  assert.deepEqual(restored.getCaptions(), []);
  assert.deepEqual(f.saved().captions, []);
});

test("reload discards expired captions from storage but retains the final cursor", t => {
  const f = fixture(t);
  f.reload().acceptUpdate(f.caption(4, { revision: 3 }));
  f.advance(61);
  const restored = f.reload();
  assert.deepEqual(restored.getCaptions(), []);
  assert.deepEqual(f.saved().captions, []);
  assert.deepEqual(restored.getResumePosition(), { last_segment: 4, last_revision: 3 });
});

test("accepting a new update also removes expired persisted captions", t => {
  const f = fixture(t);
  const store = f.reload();
  const old = f.caption(1);
  store.acceptUpdate(old);
  f.advance(61);
  store.acceptUpdate(f.caption(2));
  assert.deepEqual(f.saved().captions.map(caption => caption.segment), [2]);
  assert.equal(store.acceptUpdate(old), null);
});

test("tabs and server/session namespaces keep independent state", t => {
  const f = fixture(t);
  f.reload().acceptUpdate(f.caption(1));
  for (const overrides of [
    { storage: memoryStorage() },
    { url: "ws://another-server:8765" },
    { session: "L2" }
  ]) {
    const fresh = createCaptionStore({ ...f.options, ...overrides });
    assert.deepEqual(fresh.getCaptions(), []);
    assert.deepEqual(fresh.getResumePosition(), { last_segment: 0, last_revision: 0 });
  }
  assert.equal(f.reload().getCaptions().length, 1);
});

test("malformed or unsupported saved state resets captions and cursor together", t => {
  const f = fixture(t);
  f.reload();
  const key = [...f.storage.entries.keys()][0];
  const valid = { version: 1, captions: [f.caption(1)], last_segment: 1, last_revision: 1 };
  for (const raw of [
    "{broken", "null",
    JSON.stringify({ ...valid, version: 2 }),
    JSON.stringify({ ...valid, last_segment: -1 }),
    JSON.stringify({ ...valid, captions: [{ ...f.caption(1), source_ts: "invalid" }] }),
    JSON.stringify({ ...valid, captions: [{ ...f.caption(1), session: "L2" }] })
  ]) {
    f.storage.setItem(key, raw);
    const restored = f.reload();
    assert.deepEqual(restored.getCaptions(), []);
    assert.deepEqual(restored.getResumePosition(), { last_segment: 0, last_revision: 0 });
  }
});

test("denied reads and failed writes allow live captions to continue in memory", t => {
  const f = fixture(t);
  const denied = () => { throw new Error("Storage unavailable"); };
  for (const storage of [
    null,
    { getItem: denied, setItem: denied },
    { getItem: () => null, setItem: denied }
  ]) {
    const store = createCaptionStore({ ...f.options, storage });
    const caption = f.caption(1);
    assert.ok(store.acceptUpdate(caption));
    assert.deepEqual(store.getCaptions(), [caption]);
    assert.deepEqual(store.getResumePosition(), { last_segment: 1, last_revision: 1 });
  }
});

test("a blocked sessionStorage getter does not prevent store initialization", t => {
  const f = fixture(t);
  Object.defineProperty(globalThis, "sessionStorage", {
    configurable: true,
    get() { throw new Error("SecurityError"); }
  });
  t.after(() => { delete globalThis.sessionStorage; });
  const store = createCaptionStore({ url: f.options.url, session: f.options.session });
  assert.ok(store.acceptUpdate(f.caption(1)));
  assert.equal(store.getCaptions().length, 1);
});

test("clearing a session removes its saved captions and cursor without clearing other data", t => {
  const f = fixture(t);
  const store = f.reload();
  store.acceptUpdate(f.caption(4, { revision: 3 }));
  const otherSession = createCaptionStore({ ...f.options, session: "L2" });
  otherSession.acceptUpdate(f.caption(7, { session: "L2" }));
  f.storage.setItem("unrelated-preference", "keep");

  store.clear();
  assert.deepEqual(store.getCaptions(), []);
  assert.deepEqual(store.getResumePosition(), { last_segment: 0, last_revision: 0 });
  assert.equal(f.storage.entries.size, 2);
  assert.equal(f.storage.getItem("unrelated-preference"), "keep");
  const fresh = f.reload();
  assert.deepEqual(fresh.getCaptions(), []);
  assert.deepEqual(fresh.getResumePosition(), { last_segment: 0, last_revision: 0 });
  assert.equal(createCaptionStore({ ...f.options, session: "L2" }).getCaptions().length, 1);
});
