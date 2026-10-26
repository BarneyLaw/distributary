import { createCaptionStore } from "./captions.js";
import { connect } from "./connection.js";
import { createUI } from "./ui.js";

const captions = createCaptionStore();
const ui = createUI();


// Protocol Logic - Runs on every message received by websocket 
function wsMsgCallback(msg) {

  if (msg.type === "CAPTION_UPDATE") {
    // Attempt to update caption in caption store
    const update = captions.acceptUpdate(msg);
    if (update) {
      ui.renderCaption(update.key, update.caption);
    }

  } else if (msg.type === "HISTORY") {
    // Merge incoming history with locacl caption store

    console.log(`HISTORY with ${msg.items?.length ?? 0} item(s)`, msg);
    // TODO: merge history without stale/duplicate final captions.
  }
}


// Start Web Socket Connection =============================================
connect({
  url: "ws://127.0.0.1:8765",
  session: "L1",
  onStatus: ui.setStatus,
  onMessage(msg) {wsMsgCallback(msg)}
});
