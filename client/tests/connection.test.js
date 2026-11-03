import assert from "node:assert/strict";
import { test } from "node:test";

function stubGlobal(t, name, value) {
  const original = Object.getOwnPropertyDescriptor(globalThis, name);
  Object.defineProperty(globalThis, name, { configurable: true, writable: true, value });
  t.after(() => {
    if (original) {
      Object.defineProperty(globalThis, name, original);
    } else {
      delete globalThis[name];
    }
  });
}

async function helloFixture(t, storage, name) {
  let ws;
  stubGlobal(t, "sessionStorage", storage);
  stubGlobal(t, "WebSocket", class {
    constructor() { ws = this; }
    listeners = new Map();
    addEventListener(name, callback) { this.listeners.set(name, callback); }
    send(raw) { this.hello = JSON.parse(raw); }
  });
  const { connect } = await import(`../connection.js?${name}`);
  return () => {
    connect({
      url: "ws://localhost:8765", session: "L1", onStatus() {}, onMessage() {},
      getResumePosition: () => ({ last_segment: 4, last_revision: 3 })
    });
    ws.listeners.get("open")();
    return ws.hello;
  };
}

test("hello reuses the saved tab ID and sends the restored resume position", async t => {
  const hello = await helloFixture(t, {
    getItem: () => "123456",
    setItem() { assert.fail("A saved ID should be reused"); }
  }, "saved");
  assert.deepEqual(hello(), {
    type: "CLIENT_HELLO", client_id: 123456, session: "L1",
    last_segment: 4, last_revision: 3, history_seconds: 60
  });
});

async function connectionFixture(t, name) {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const sockets = [];
  const statuses = [];
  const messages = [];
  const position = { last_segment: 4, last_revision: 3 };
  stubGlobal(t, "sessionStorage", { getItem: () => "123456" });
  stubGlobal(t, "WebSocket", class {
    listeners = new Map();
    sent = [];
    closed = false;
    constructor() { sockets.push(this); }
    addEventListener(name, callback) { this.listeners.set(name, callback); }
    send(raw) { this.sent.push(JSON.parse(raw)); }
    emit(name, event) { this.listeners.get(name)?.(event); }
    close() { this.closed = true; this.emit("close"); }
  });
  const { connect } = await import(`../connection.js?${name}`);
  const connection = connect({
    url: "ws://localhost:8765", session: "L1",
    getResumePosition: () => position,
    onMessage: msg => messages.push(msg),
    onStatus: status => statuses.push(status)
  });
  t.after(() => connection.disconnect());
  return { connection, sockets, statuses, messages, position };
}

test("disconnect suppresses stale events and reconnect sends the same ID and latest cursor", async t => {
  const f = await connectionFixture(t, "manual-reconnect");
  const original = f.sockets[0];
  original.emit("open");
  assert.equal(original.sent[0].client_id, 123456);
  f.connection.disconnect();
  assert.equal(original.closed, true);
  assert.equal(f.statuses.at(-1), "disconnected");

  // A late event from the retired socket cannot change state or start a retry.
  original.emit("message", { data: '{"type":"CAPTION_UPDATE"}' });
  original.emit("open");
  original.emit("error");
  original.emit("close");
  assert.deepEqual(f.messages, []);
  assert.equal(f.statuses.at(-1), "disconnected");
  assert.equal(original.sent.length, 1);
  t.mock.timers.tick(5000);
  assert.equal(f.sockets.length, 1);
  f.position.last_segment = 6;
  f.connection.reconnect();
  f.connection.reconnect();
  assert.equal(f.sockets.length, 2);
  f.sockets[1].emit("open");
  assert.deepEqual(f.sockets[1].sent[0], {
    type: "CLIENT_HELLO", client_id: 123456, session: "L1",
    last_segment: 6, last_revision: 3, history_seconds: 60
  });
  f.sockets[1].emit("message", { data: '{"type":"HISTORY","items":[]}' });
  assert.deepEqual(f.messages, [{ type: "HISTORY", items: [] }]);
});

test("disconnect cancels a pending automatic retry and reconnect restores normal retries", async t => {
  const f = await connectionFixture(t, "pending-retry");
  f.sockets[0].emit("open");
  f.sockets[0].emit("close");
  t.mock.timers.tick(500);
  f.connection.disconnect();
  t.mock.timers.tick(3000);
  assert.equal(f.sockets.length, 1);
  f.connection.reconnect();
  assert.equal(f.sockets.length, 2);
  f.sockets[1].emit("close");
  t.mock.timers.tick(999);
  assert.equal(f.sockets.length, 2);
  t.mock.timers.tick(1);
  assert.equal(f.sockets.length, 3);
});

test("disconnect can interrupt a connecting socket without scheduling a retry", async t => {
  const f = await connectionFixture(t, "connecting-outage");
  f.connection.disconnect();
  assert.equal(f.sockets[0].closed, true);
  f.connection.disconnect();
  t.mock.timers.tick(5000);
  assert.equal(f.sockets.length, 1);
});

test("unavailable storage still allows hello and a stable ID on reconnect", async t => {
  t.mock.method(Math, "random", () => 0.5);
  const denied = () => { throw new Error("Storage disabled"); };
  const hello = await helloFixture(t, { getItem: denied, setItem: denied }, "denied");
  const first = hello();
  t.mock.method(Math, "random", () => 0.9);
  assert.equal(hello().client_id, first.client_id);
  assert.equal(first.client_id, 550000);
});

test("resetting the client ID persists a different ID even when random returns the old value", async t => {
  let savedId = "550000";
  stubGlobal(t, "sessionStorage", {
    getItem: () => savedId,
    setItem: (key, value) => { savedId = value; }
  });
  t.mock.method(Math, "random", () => 0.5);
  const { getClientId, resetClientId } = await import("../connection.js?reset");
  const previousId = getClientId();
  const nextId = resetClientId();
  assert.notEqual(nextId, previousId);
  assert.equal(getClientId(), nextId);
  assert.equal(Number(savedId), nextId);
  const afterReload = await import("../connection.js?after-reset");
  assert.equal(afterReload.getClientId(), nextId);
});
