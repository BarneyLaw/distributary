// Caption state and revision rules 
export function createCaptionStore() {
  // TODO: bound retention when recent caption history is implemented.
  const segments = new Map();
  const retentionSeconds = 60;
  let lastFinalSegment = 0;
  let lastFinalRevision = 0;

  // Remove captions outside of the recent history window
  function purgeExpired(){
    const cutoff = Date.now() / 1000 - retentionSeconds;
    for (const [key, caption] of segments) {
      if (caption.source_ts < cutoff) {
        segments.delete(key);
      }
    }
  }

  // Attempt to include update information to caption store - returns null if not saved
  function acceptUpdate(msg){
    // Drop expired captions even if they are no longer in the store
    if (msg.source_ts < Date.now() / 1000 - retentionSeconds) {
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

    return { key, caption };
  }

  // Get the finalized position to send when reconnecting
  function getResumePosition(){
    return { last_segment: lastFinalSegment, last_revision: lastFinalRevision };
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

  return { acceptUpdate, purgeExpired, getHistory, getResumePosition };
}
