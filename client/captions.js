function browserSessionStorage() {
  try {
    return globalThis.sessionStorage;
  } catch {
    // Storage can be disabled by the browser; captions still work in memory.
    return null;
  }
}

// Caption state and revision rules, persisted separately for each tab and stream.
export function createCaptionStore({ url, session, storage = browserSessionStorage() } = {}) {
  const segments = new Map();
  const retentionSeconds = 60;
  const storageKey = `liverecall-captions:${JSON.stringify([url, session])}`;
  let lastFinalSegment = 0;
  let lastFinalRevision = 0;

  function persist() {
    try {
      storage?.setItem(storageKey, JSON.stringify({
        version: 1,
        captions: Array.from(segments.values()),
        last_segment: lastFinalSegment,
        last_revision: lastFinalRevision
      }));
    } catch {
      // A denied or full storage area must not interrupt live captions.
    }
  }

  function validPosition(value) {
    return Number.isSafeInteger(value) && value >= 0;
  }

  function validCaption(caption) {
    return caption && typeof caption.session === "string" &&
      (session === undefined || caption.session === session) &&
      validPosition(caption.segment) && validPosition(caption.revision) &&
      Number.isFinite(caption.source_ts) && typeof caption.final === "boolean" &&
      typeof caption.text === "string";
  }

  function restore() {
    let saved;
    try {
      saved = JSON.parse(storage?.getItem(storageKey) ?? "null");
    } catch {
      return;
    }
    if (!saved || saved.version !== 1 || !Array.isArray(saved.captions) ||
        !validPosition(saved.last_segment) || !validPosition(saved.last_revision) ||
        !saved.captions.every(validCaption)) {
      return;
    }

    lastFinalSegment = saved.last_segment;
    lastFinalRevision = saved.last_revision;
    const cutoff = Date.now() / 1000 - retentionSeconds;
    for (const caption of saved.captions) {
      if (caption.source_ts >= cutoff) {
        segments.set(JSON.stringify([caption.session, caption.segment]), caption);
      }
    }
  }

  // Remove captions outside of the recent history window
  function purgeExpired(){
    const cutoff = Date.now() / 1000 - retentionSeconds;
    let removed = false;
    for (const [key, caption] of segments) {
      if (caption.source_ts < cutoff) {
        segments.delete(key);
        removed = true;
      }
    }
    if (removed) {
      persist();
    }
  }

  // Attempt to include update information to caption store - returns null if not saved
  function acceptUpdate(msg){
    purgeExpired();
    // Drop expired captions even if they are no longer in the store
    if (!validCaption(msg) || msg.source_ts < Date.now() / 1000 - retentionSeconds) {
      return null;
    }

    const key = JSON.stringify([msg.session, msg.segment]);
    const existing = segments.get(key);

    // incoming segment already final or a lower revision number than stored - changes dropped
    if (existing && (existing.final || msg.revision <= existing.revision)) {
      return null;
    }

    // Save incoming caption updates 
    const caption = { ...msg };
    segments.set(key, caption);

    // Remember the newest finalized position even after captions expire
    if (caption.final && (caption.segment > lastFinalSegment ||
        (caption.segment === lastFinalSegment && caption.revision > lastFinalRevision))) {
      lastFinalSegment = caption.segment;
      lastFinalRevision = caption.revision;
    }

    persist();
    return { key, caption };
  }

  // Get the finalized position to send when reconnecting
  function getResumePosition(){
    return { last_segment: lastFinalSegment, last_revision: lastFinalRevision };
  }

  // Include provisional captions so the current line can return immediately on reload.
  function getCaptions() {
    purgeExpired();
    return Array.from(segments.values())
      .sort((a, b) => a.segment - b.segment)
      .map(caption => ({ ...caption }));
  }

  // Get recent finalized captions in segment order
  function getHistory(seconds = retentionSeconds){
    purgeExpired();
    const historySeconds = Math.min(Math.max(seconds, 0), retentionSeconds);
    if (historySeconds === 0) {
      return [];
    }

    const cutoff = Date.now() / 1000 - historySeconds;
    return Array.from(segments.values())
      .filter(caption => caption.final && caption.source_ts >= cutoff)
      .sort((a, b) => a.segment - b.segment)
      .map(caption => ({ ...caption }));
  }

  restore();
  // Rewrite the snapshot after restoration to remove expired or invalid saved data.
  persist();

  return { acceptUpdate, purgeExpired, getHistory, getResumePosition, getCaptions };
}
