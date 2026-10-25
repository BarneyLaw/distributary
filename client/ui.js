// DOM rendering and user interactions; caption acceptance belongs to the store.
export function createUI() {
  const statusEl = document.getElementById("status");
  const historyEl = document.getElementById("live-history");
  const currentEl = document.getElementById("live-current");
  const recallEl = document.getElementById("recall");
  const rows = new Map();

  function updateRow(row, caption) {
    row.classList.toggle("caption-final", caption.final);
    row.classList.toggle("caption-provisional", !caption.final);
    row.textContent = caption.text;
  }

  for (const button of document.querySelectorAll("button[data-seconds]")) {
    button.addEventListener("click", () => {
      const seconds = Number(button.dataset.seconds);
      // TODO: show recent finalized captions from local state.
      recallEl.textContent = `TODO: show finalized captions from the previous ${seconds} seconds using local state.`;
    });
  }

  // Update the captions shown on screen 
  function renderCaption(key, caption) {
    const existing = rows.get(key);
    if (existing) {
      updateRow(existing.row, caption);
      return;
    }

    // Older updates must not bring a segment back after it leaves the display.
    const oldestSegment = Math.min(...Array.from(rows.values(), entry => entry.segment));
    if (rows.size === 3 && caption.segment <= oldestSegment) {
      return;
    }

    // Save the current row positions for the movement animation.
    const previousPositions = new Map(
      Array.from(rows.values(), ({ row }) => [row, row.getBoundingClientRect().top])
    );

    // Create and store a row for the new segment.
    const row = document.createElement("div");
    row.className = "caption-line";
    updateRow(row, caption);
    rows.set(key, { row, segment: caption.segment });

    // Keep the three newest segments in order and remove the oldest row.
    const visible = Array.from(rows.entries()).sort((a, b) => a[1].segment - b[1].segment);
    if (visible.length > 3) {
      const [oldestKey, oldest] = visible.shift();
      oldest.row.remove();
      rows.delete(oldestKey);
    }

    // Place the latest segment in the main area and the previous two above it.
    visible.forEach(([, entry], index) => {
      const isCurrent = index === visible.length - 1;
      entry.row.classList.toggle("caption-current", isCurrent);
      entry.row.classList.toggle("caption-previous", index === visible.length - 2);
      entry.row.classList.toggle("caption-oldest", index === visible.length - 3);
      (isCurrent ? currentEl : historyEl).appendChild(entry.row);
    });


    // Animate the captions fading out upwards as more comes in
    visible.forEach(([, entry]) => {
      const previousTop = previousPositions.get(entry.row);
      if (previousTop === undefined) {
        entry.row.animate([
          { transform: "translateY(0.75rem)", opacity: 0 },
          { transform: "translateY(0)", opacity: window.getComputedStyle(entry.row).opacity }
        ], { duration: 220, easing: "ease-out" });
      } else {
        const offset = previousTop - entry.row.getBoundingClientRect().top;
        if (offset !== 0) {
          entry.row.animate([
            { transform: `translateY(${offset}px)` },
            { transform: "translateY(0)" }
          ], { duration: 220, easing: "ease-out" });
        }
      }
    });
  }

  // Update connection status label shown on screen
  function setStatus(newStatus){
    statusEl.textContent = newStatus;
  }


  // Expose 
  return {setStatus, renderCaption};
}
