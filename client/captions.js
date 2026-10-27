// Caption state and revision rules 
export function createCaptionStore() {
  // TODO: bound retention when recent caption history is implemented.
  const segments = new Map();
  const retentionSeconds = 60;

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

    return { key, caption };
  }

  return { acceptUpdate, purgeExpired };
}
