import { createCaptionStore } from "./captions.js";
import { connect } from "./connection.js";
import { createUI } from "./ui.js";

const captions = createCaptionStore();
const ui = createUI({ getHistory: captions.getHistory });

// Purge expired captions even when no updates arrive or the connection closes
setInterval(captions.purgeExpired, 1000);


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
connect({
  url: "ws://127.0.0.1:8765",
  session: "L1",
  onStatus: ui.setStatus,
  getResumePosition: captions.getResumePosition,
  onMessage(msg) {wsMsgCallback(msg)}
});
