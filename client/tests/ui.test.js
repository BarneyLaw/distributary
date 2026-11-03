import assert from "node:assert/strict";
import { test } from "node:test";
import { createUI } from "../ui.js";

test("refreshing the client requires confirmation and cancellation leaves it untouched", t => {
  const originalDocument = Object.getOwnPropertyDescriptor(globalThis, "document");
  const originalWindow = Object.getOwnPropertyDescriptor(globalThis, "window");
  t.after(() => {
    for (const [name, original] of [["document", originalDocument], ["window", originalWindow]]) {
      if (original) {
        Object.defineProperty(globalThis, name, original);
      } else {
        delete globalThis[name];
      }
    }
  });
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
    },
    querySelectorAll: () => []
  };
  let confirmed = false;
  let restarts = 0;
  globalThis.window = {
    confirm(message) {
      assert.match(message, /remove all captions saved/);
      assert.match(message, /new client ID/);
      return confirmed;
    }
  };
  createUI({ getHistory: () => [], clientId: 123456, onRefreshClientSession() { restarts++; } });
  const click = elements.get("refresh-client-session").listeners.get("click");
  click();
  assert.equal(restarts, 0);
  assert.equal(elements.get("client-id").textContent, "Client ID: 123456");
  confirmed = true;
  click();
  assert.equal(restarts, 1);
});
