import { createCaptionStore } from "./captions.js";
import { connect, getClientId, resetClientId } from "./connection.js";
import { createUI } from "./ui.js";

const url = "ws://127.0.0.1:8765";
const session = "L1";
let restarting = false;
const captions = createCaptionStore({ url, session });
const ui = createUI({
  getHistory: captions.getHistory,
  clientId: getClientId(),
  onRefreshClientSession() {
    if (restarting) {
      return;
    }
    restarting = true;
    try {
      captions.clear();
      resetClientId();
      window.location.reload();
    } catch {
      restarting = false;
      window.alert("The client session could not be restarted. Check your browser's site storage permissions and try again.");
    }
  }
});

// Restore the three live lines before waiting for the WebSocket or its history.
for (const caption of captions.getCaptions().slice(-3)) {
  ui.renderCaption(JSON.stringify([caption.session, caption.segment]), caption);
}

// Purge expired captions even when no updates arrive or the connection closes
// Update the current recall window length shown on UI
setInterval(() => {
  if (restarting) {
    return;
  }
  captions.purgeExpired();
  ui.updateRecall();
}, 1000);


// Protocol Logic - Runs on every message received by websocket 
function wsMsgCallback(msg) {
  // Messages from the old connection must not repopulate storage during reload.
  if (restarting) {
    return;
  }

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
  url,
  session,
  onStatus: ui.setStatus,
  getResumePosition: captions.getResumePosition,
  onMessage(msg) {wsMsgCallback(msg)}
});
