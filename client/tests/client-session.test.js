import assert from "node:assert/strict";
import { test } from "node:test";

class Element {
  children = [];
  parent = null;
  listeners = new Map();
  attributes = new Map();
  dataset = {};
  hidden = false;
  disabled = false;
  textContent = "";
  className = "";
  value = "";
  defaultValue = "";
  classes = new Set();
  classList = {
    remove: name => this.classes.delete(name),
    toggle: (name, active) => active ? this.classes.add(name) : this.classes.delete(name)
  };
  get valueAsNumber() { return Number(this.value); }
  addEventListener(name, callback) { this.listeners.set(name, callback); }
  setAttribute(name, value) { this.attributes.set(name, value); }
  appendChild(child) { child.remove(); child.parent = this; this.children.push(child); }
  append(...children) { children.forEach(child => this.appendChild(child)); }
  replaceChildren() { this.children.forEach(child => { child.parent = null; }); this.children = []; }
  remove() {
    if (this.parent) this.parent.children = this.parent.children.filter(child => child !== this);
    this.parent = null;
  }
  getBoundingClientRect() { return { top: 0 }; }
  animate() {}
  reportValidity() { return true; }
  querySelectorAll(selector) {
    const descendants = this.children.flatMap(child => [child, ...child.querySelectorAll("*")]);
    return selector === "*" ? descendants : descendants.filter(child => child.tagName === selector);
  }
  click() { this.listeners.get("click")?.(); }
}

function createDocument() {
  const nodes = new Map();
  const get = id => {
    if (!nodes.has(id)) nodes.set(id, new Element());
    return nodes.get(id);
  };
  const windowButtons = [15, 30, 60].map(seconds => {
    const button = new Element();
    button.dataset.seconds = String(seconds);
    return button;
  });
  get("recall-new").hidden = true;
  get("live").classes.add("live-empty");
  get("live-current").appendChild(get("live-placeholder"));
  get("recall-time-format").value = "local";
  get("simulate-disconnection").disabled = true;
  get("disconnect-duration").disabled = true;
  get("disconnect-duration").defaultValue = "5";
  // Simulate browser form restoration: initialization must reset this value.
  get("disconnect-duration").value = "27";
  return {
    get,
    document: {
      getElementById: get,
      querySelectorAll: () => windowButtons,
      createElement(tagName) { const element = new Element(); element.tagName = tagName; return element; }
    },
    recallTexts: () => get("recall").querySelectorAll("*")
      .filter(element => element.className === "recall-text").map(element => element.textContent)
  };
}

test("a confirmed client reset boots an empty client, sends a new HELLO, and recalls incoming history", async t => {
  t.mock.timers.enable({ apis: ["setInterval", "setTimeout"] });
  for (const name of ["document", "window", "sessionStorage", "WebSocket"]) {
    const original = Object.getOwnPropertyDescriptor(globalThis, name);
    t.after(() => original
      ? Object.defineProperty(globalThis, name, original)
      : delete globalThis[name]);
  }
  const saved = new Map();
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
  let reloads = 0;
  globalThis.window = {
    confirm: () => true,
    location: { reload() { reloads++; } },
    getComputedStyle: () => ({ opacity: "1" }),
    alert(message) { assert.fail(message); }
  };
  const caption = (segment, overrides = {}) => ({
    type: "CAPTION_UPDATE", session: "L1", segment, revision: 3,
    source_ts: Date.now() / 1000, final: true, text: `Caption ${segment}`, ...overrides
  });
  const deliver = (socket, message) => socket.emit("message", { data: JSON.stringify(message) });
  let page = createDocument();
  globalThis.document = page.document;
  await import("../app.js?before-full-reset");
  sockets[0].emit("open");
  const originalId = sockets[0].sent[0].client_id;
  deliver(sockets[0], caption(50));
  page.get("recall-toggle").click();
  assert.deepEqual(page.recallTexts(), ["Caption 50"]);
  page.get("recall-time-format").value = "local";
  page.get("disconnect-duration").value = "12";
  page.get("disconnect-form").listeners.get("submit")({ preventDefault() {} });
  saved.set('liverecall-captions:["ws://another-server","L2"]', "old state");
  page.get("refresh-client-session").click();
  assert.equal(reloads, 1);
  assert.equal(sockets[0].closed, true);
  assert.deepEqual([...saved.keys()], ["liverecall-client-id"]);
  deliver(sockets[0], caption(51));
  t.mock.timers.tick(20000);
  assert.equal(sockets.length, 1);
  assert.deepEqual([...saved.keys()], ["liverecall-client-id"]);

  // A reload runs the entry point again with new DOM and new caption/UI stores.
  page = createDocument();
  globalThis.document = page.document;
  await import("../app.js?after-full-reset");
  sockets[1].emit("open");
  const hello = sockets[1].sent[0];
  assert.notEqual(hello.client_id, originalId);
  assert.deepEqual(hello, {
    type: "CLIENT_HELLO", client_id: hello.client_id, session: "L1",
    last_segment: 0, last_revision: 0, history_seconds: 60
  });
  assert.equal(page.get("recall-content").hidden, true);
  assert.equal(page.get("recall-time-format").value, "relative");
  assert.equal(page.get("disconnect-duration").value, "5");
  assert.equal(page.get("simulate-disconnection").disabled, false);
  assert.equal(page.get("disconnection-status").hidden, true);
  assert.equal(page.get("live").classes.has("live-empty"), true);
  assert.equal(page.get("live-placeholder").hidden, false);
  assert.deepEqual(page.recallTexts(), []);

  deliver(sockets[1], {
    type: "HISTORY",
    items: [caption(1, { source_ts: Date.now() / 1000 - 61 }), caption(2), caption(3),
      caption(4, { final: false })]
  });
  page.get("recall-toggle").click();
  assert.deepEqual(page.recallTexts(), ["Caption 2", "Caption 3"]);
  deliver(sockets[1], caption(4));
  assert.equal(page.get("recall-new").textContent, "1 new caption available");
  page.get("recall-refresh").click();
  assert.deepEqual(page.recallTexts(), ["Caption 2", "Caption 3", "Caption 4"]);
});
