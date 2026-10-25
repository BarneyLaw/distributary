// DOM rendering and user interactions; caption acceptance belongs to the store.
export function createUI() {
  const statusEl = document.getElementById("status");
  const liveEl = document.getElementById("live");
  const recallEl = document.getElementById("recall");
  const rows = new Map();

  for (const button of document.querySelectorAll("button[data-seconds]")) {
    button.addEventListener("click", () => {
      const seconds = Number(button.dataset.seconds);
      // TODO: show recent finalized captions from local state.
      recallEl.textContent = `TODO: show finalized captions from the previous ${seconds} seconds using local state.`;
    });
  }

  return {
    setStatus(status) {
      statusEl.textContent = status;
    },

    renderCaption(key, caption) {
      let row = rows.get(key);
      if (!row) {
        row = document.createElement("div");
        rows.set(key, row);
        liveEl.appendChild(row);
      }

      row.className = `caption-line ${caption.final ? "caption-final" : "caption-provisional"}`;
      row.textContent = caption.text;
      liveEl.scrollTop = liveEl.scrollHeight;
    }
  };
}
