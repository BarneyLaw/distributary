// DOM rendering and user interactions; caption acceptance belongs to the store.
export function createUI({ getHistory }) {
  const statusEl = document.getElementById("status");
  const historyEl = document.getElementById("live-history");
  const currentEl = document.getElementById("live-current");
  const recallEl = document.getElementById("recall");
  const recallToggle = document.getElementById("recall-toggle");
  const recallContent = document.getElementById("recall-content");
  const recallSummary = document.getElementById("recall-summary");
  const recallNew = document.getElementById("recall-new");
  const timeFormat = document.getElementById("recall-time-format");
  const windowButtons = document.querySelectorAll("button[data-seconds]");
  const rows = new Map();
  let recallSeconds = 30;
  let snapshotTime = 0;
  let snapshotKeys = new Set();
  const localTime = new Intl.DateTimeFormat(undefined, {
    hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false
  });

  function updateRow(row, caption) {
    row.classList.toggle("caption-final", caption.final);
    row.classList.toggle("caption-provisional", !caption.final);
    row.textContent = caption.text;
  }

  // Show source timestamps in the browser's local time or as seconds ago
  function formatTimestamp(sourceTs){
    if (timeFormat.value === "local") {
      return localTime.format(new Date(sourceTs * 1000));
    }
    const secondsAgo = Math.max(0, Math.floor(snapshotTime - sourceTs));
    return `${secondsAgo}s ago`;
  }

  // Keep timestamps and availability current without changing the snapshot
  function updateRecall(){
    if (recallContent.hidden) {
      return;
    }

    const newCount = getHistory(recallSeconds).filter(caption =>
      !snapshotKeys.has(JSON.stringify([caption.session, caption.segment]))
    ).length;
    const message = `${newCount} new caption${newCount === 1 ? "" : "s"} available`;
    if (recallNew.textContent !== message) {
      recallNew.textContent = message;
    }
    recallNew.hidden = newCount === 0;
  }

  // Render each finalized segment with its timestamp beside the text
  function createRecallRow(caption){
    const row = document.createElement("div");
    row.className = "recall-row";
    row.setAttribute("role", "listitem");

    const timestamp = document.createElement("time");
    const date = new Date(caption.source_ts * 1000);
    timestamp.dateTime = date.toISOString();
    timestamp.title = date.toLocaleString();
    timestamp.dataset.sourceTs = caption.source_ts;
    timestamp.textContent = formatTimestamp(caption.source_ts);

    const text = document.createElement("p");
    text.className = "recall-text";
    text.textContent = caption.text;
    row.append(timestamp, text);
    return row;
  }

  // Take a fresh snapshot only when opening, refreshing or changing the window
  function takeSnapshot(){
    snapshotTime = Date.now() / 1000;
    const history = getHistory(recallSeconds);
    snapshotKeys = new Set(history.map(caption => JSON.stringify([caption.session, caption.segment])));
    recallSummary.textContent = `Previous ${recallSeconds} seconds · captured at ${localTime.format(new Date(snapshotTime * 1000))}`;
    recallEl.replaceChildren();

    for (const caption of history) {
      recallEl.appendChild(createRecallRow(caption));
    }
    if (!history.length) {
      const empty = document.createElement("p");
      empty.className = "recall-empty";
      empty.textContent = `No finalized captions from the previous ${recallSeconds} seconds.`;
      recallEl.appendChild(empty);
    }

    recallEl.scrollTop = 0;
    for (const button of windowButtons) {
      button.setAttribute("aria-pressed", String(Number(button.dataset.seconds) === recallSeconds));
    }
    updateRecall();
  }

  recallToggle.addEventListener("click", () => {
    recallContent.hidden = !recallContent.hidden;
    recallToggle.setAttribute("aria-expanded", String(!recallContent.hidden));
    if (!recallContent.hidden) {
      recallSeconds = 30;
      takeSnapshot();
    }
  });

  document.getElementById("recall-refresh").addEventListener("click", takeSnapshot);
  timeFormat.addEventListener("change", () => {
    for (const timestamp of recallEl.querySelectorAll("time")) {
      timestamp.textContent = formatTimestamp(Number(timestamp.dataset.sourceTs));
    }
  });

  for (const button of windowButtons) {
    button.addEventListener("click", () => {
      recallSeconds = Number(button.dataset.seconds);
      takeSnapshot();
    });
  }

  // Update the captions shown on screen 
  function renderCaption(key, caption) {
    updateRecall();
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
    statusEl.dataset.status = newStatus;
  }


  // Expose 
  return {setStatus, renderCaption, updateRecall};
}
