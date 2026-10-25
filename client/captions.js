// Caption state and revision rules, independent of the DOM and WebSocket.
export function createCaptionStore() {
  // TODO: bound retention when recent caption history is implemented.
  const segments = new Map();

  return {
    acceptUpdate(msg) {
      const key = JSON.stringify([msg.session, msg.segment]);
      const existing = segments.get(key);

      // Finalized segments are immutable; ignore stale or duplicate revisions.
      if (existing && (existing.final || msg.revision <= existing.revision)) {
        return null;
      }

      const caption = { ...msg };
      segments.set(key, caption);
      return { key, caption };
    }
  };
}
