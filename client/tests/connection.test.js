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
  stubGlobal(t, "sessionStorage", storage);
  stubGlobal(t, "WebSocket", class {
    listeners = new Map();
    addEventListener(name, callback) { this.listeners.set(name, callback); }
    send(raw) { this.hello = JSON.parse(raw); }
  });
  const { connect } = await import(`../connection.js?${name}`);
  return () => {
    const ws = connect({
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

test("unavailable storage still allows hello and a stable ID on reconnect", async t => {
  t.mock.method(Math, "random", () => 0.5);
  const denied = () => { throw new Error("Storage disabled"); };
  const hello = await helloFixture(t, { getItem: denied, setItem: denied }, "denied");
  const first = hello();
  t.mock.method(Math, "random", () => 0.9);
  assert.equal(hello().client_id, first.client_id);
  assert.equal(first.client_id, 550000);
});
