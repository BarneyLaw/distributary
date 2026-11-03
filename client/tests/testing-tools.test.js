import assert from "node:assert/strict";
import { test } from "node:test";
import { createTestingTools } from "../testing-tools.js";
import { connect, getClientId } from "../connection.js";
import { createCaptionStore } from "../captions.js";

function fixture(t) {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  for (const name of ["document", "window", "sessionStorage", "WebSocket"]) {
    const original = Object.getOwnPropertyDescriptor(globalThis, name);
    t.after(() => {
      if (original) {
        Object.defineProperty(globalThis, name, original);
      } else {
        delete globalThis[name];
      }
    });
  }
  const elements = new Map();
  globalThis.document = {
    getElementById(id) {
      if (!elements.has(id)) {
        elements.set(id, {
          listeners: new Map(),
          addEventListener(event, callback) { this.listeners.set(event, callback); }
        });
      }
      return elements.get(id);
    }
  };
  let confirmed = false;
  let reloads = 0;
  let beforeResets = 0;
  const alerts = [];
  globalThis.window = {
    confirm(message) {
      assert.match(message, /remove all captions saved/i);
      assert.match(message, /new client ID/);
      return confirmed;
    },
    location: { reload() { reloads++; } },
    alert: message => alerts.push(message)
  };
  const saved = new Map([["liverecall-client-id", "123456"]]);
  globalThis.sessionStorage = {
    get length() { return saved.size; },
    key: index => [...saved.keys()][index] ?? null,
    getItem: key => saved.get(key) ?? null,
    setItem: (key, value) => saved.set(key, value),
    removeItem: key => saved.delete(key)
  };
  const sockets = [];
  globalThis.WebSocket = class {
    listeners = new Map();
    sent = [];
    closed = false;
    constructor() { sockets.push(this); }
    addEventListener(name, callback) { this.listeners.set(name, callback); }
    emit(name, event) { this.listeners.get(name)?.(event); }
    send(raw) { this.sent.push(JSON.parse(raw)); }
    close() { this.closed = true; this.emit("close"); }
  };
  const captions = createCaptionStore({ url: "ws://localhost:8765", session: "L1" });
  captions.acceptUpdate({ session: "L1", segment: 4, revision: 3,
    source_ts: Date.now() / 1000, final: true, text: "Saved caption" });
  const connection = connect({
    url: "ws://localhost:8765", session: "L1", onMessage() {}, onStatus() {},
    getResumePosition: captions.getResumePosition
  });
  t.after(() => connection.disconnect());
  sockets[0].emit("open");
  const originalId = getClientId();
  createTestingTools({ captions, connection, onBeforeReset() { beforeResets++; } });
  const form = elements.get("disconnect-form");
  form.reportValidity = () => true;
  elements.get("disconnect-duration").valueAsNumber = 5;
  return {
    elements, sockets, captions, saved, alerts, originalId,
    confirm: () => { confirmed = true; },
    reloads: () => reloads,
    beforeResets: () => beforeResets,
    refresh: () => elements.get("refresh-client-session").listeners.get("click")(),
    simulate: () => form.listeners.get("submit")({ preventDefault() {} })
  };
}

test("refresh requires confirmation, clears captions, changes ID, and stops the old connection", t => {
  const f = fixture(t);
  f.saved.set('liverecall-captions:["ws://another-server:8765","L2"]', "old captions");
  f.saved.set("liverecall-preference", "old preference");
  f.saved.set("unrelated-app-state", "keep");
  f.refresh();
  assert.equal(f.reloads(), 0);
  assert.equal(f.captions.getCaptions().length, 1);
  assert.equal(getClientId(), f.originalId);
  assert.equal(f.sockets[0].closed, false);
  f.confirm();
  f.refresh();
  assert.equal(f.reloads(), 1);
  assert.equal(f.beforeResets(), 1);
  assert.equal(f.sockets[0].closed, true);
  assert.deepEqual(f.captions.getCaptions(), []);
  assert.deepEqual(f.captions.getResumePosition(), { last_segment: 0, last_revision: 0 });
  assert.notEqual(getClientId(), f.originalId);
  assert.equal(Number(f.saved.get("liverecall-client-id")), getClientId());
  assert.deepEqual([...f.saved.keys()].sort(), ["liverecall-client-id", "unrelated-app-state"]);
  assert.equal(f.saved.get("unrelated-app-state"), "keep");
});

test("temporary disconnection waits the full duration then sends CLIENT_HELLO with the same identity", t => {
  const f = fixture(t);
  f.simulate();
  assert.equal(f.sockets[0].closed, true);
  assert.equal(f.elements.get("simulate-disconnection").disabled, true);
  assert.equal(f.elements.get("disconnect-duration").disabled, true);
  assert.match(f.elements.get("disconnection-status").textContent, /5 seconds/);
  assert.equal(f.captions.getCaptions().length, 1);
  // Overlapping submissions must not shorten or duplicate the outage.
  f.elements.get("disconnect-duration").valueAsNumber = 1;
  f.simulate();
  t.mock.timers.tick(4999);
  assert.equal(f.sockets.length, 1);
  t.mock.timers.tick(1);
  assert.equal(f.sockets.length, 2);
  assert.equal(f.elements.get("simulate-disconnection").disabled, false);
  assert.equal(f.elements.get("disconnection-status").hidden, true);
  f.sockets[1].emit("open");
  assert.deepEqual(f.sockets[1].sent[0], {
    type: "CLIENT_HELLO", client_id: f.originalId, session: "L1",
    last_segment: 4, last_revision: 3, history_seconds: 60
  });
  assert.equal(f.reloads(), 0);
});

test("simulation suppresses pending automatic retries and resetting cancels the simulation", t => {
  const f = fixture(t);
  f.sockets[0].emit("close");
  t.mock.timers.tick(500);
  f.simulate();
  t.mock.timers.tick(1000);
  assert.equal(f.sockets.length, 1);
  f.confirm();
  f.refresh();
  t.mock.timers.tick(10000);
  assert.equal(f.sockets.length, 1);
  assert.equal(f.reloads(), 1);
});

test("invalid durations and browser validation failures do not disconnect", t => {
  const f = fixture(t);
  for (const seconds of [0, -1, 0.5, 3601, NaN, Infinity]) {
    f.elements.get("disconnect-duration").valueAsNumber = seconds;
    f.simulate();
    assert.equal(f.sockets[0].closed, false);
  }
  f.elements.get("disconnect-duration").valueAsNumber = 5;
  f.elements.get("disconnect-form").reportValidity = () => false;
  f.simulate();
  assert.equal(f.sockets[0].closed, false);
});

test("storage failures during reset report the error and restore the connection", t => {
  const f = fixture(t);
  globalThis.sessionStorage.removeItem = () => { throw new Error("Storage denied"); };
  f.confirm();
  f.refresh();
  assert.equal(f.reloads(), 0);
  assert.equal(f.alerts.length, 1);
  assert.equal(f.sockets.length, 2);
});
