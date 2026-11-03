import { createCaptionStore } from "./captions.js";
import { connect, getClientId } from "./connection.js";
import { createUI } from "./ui.js";
import { createTestingTools } from "./testing-tools.js";
import { getWebSocketUrl } from "./config.js";

const url = getWebSocketUrl();
const session = "L1";
const captions = createCaptionStore({ url, session });
const ui = createUI({ getHistory: captions.getHistory, clientId: getClientId() });

// Restore the three live lines before waiting for the WebSocket or its history.
for (const caption of captions.getCaptions().slice(-3)) {
  ui.renderCaption(JSON.stringify([caption.session, caption.segment]), caption);
}

// Purge expired captions even when no updates arrive or the connection closes
// Update the current recall window length shown on UI
const purgeTimer = setInterval(() => {
  captions.purgeExpired();
  ui.updateRecall();
}, 1000);


// Protocol Logic - Runs on every message received by websocket 
function wsMsgCallback(msg) {
  if (msg.type === "CAPTION_UPDATE") {
    // Attempt to update caption in caption store
    const update = captions.acceptUpdate(msg);
    if (update) {
      ui.renderCaption(update.key, update.caption);
    }

  } else if (msg.type === "HISTORY") {
    // Merge incoming history with local caption store
    for (const item of msg.items ?? []) {
      if (!item.final) {
        continue;
      }

      const update = captions.acceptUpdate(item);
      if (update) {
        ui.renderCaption(update.key, update.caption);
      }
    }
  }
}


// Start Web Socket Connection =============================================
const connection = connect({
  url,
  session,
  onStatus: ui.setStatus,
  getResumePosition: captions.getResumePosition,
  onMessage(msg) {wsMsgCallback(msg)}
});

createTestingTools({ captions, connection, onBeforeReset: () => clearInterval(purgeTimer) });
