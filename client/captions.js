// Caption state and revision rules 
export function createCaptionStore() {
  // TODO: bound retention when recent caption history is implemented.
  const segments = new Map();

  // Attempt to include update information to caption store - returns null if not saved
  function acceptUpdate(msg){
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

  return { acceptUpdate };
}
