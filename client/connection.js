let clientId;

// Keep one client ID per tab, including across reloads and reconnects.
function numericClientId() {
  if (clientId !== undefined) {
    return clientId;
  }
  const key = "liverecall-client-id";
  try {
    const value = Number(sessionStorage.getItem(key));
    if (Number.isSafeInteger(value) && value > 0) {
      clientId = value;
      return clientId;
    }
  } catch {
    // Retain an in-memory ID when browser storage is unavailable.
  }
  clientId = Math.floor(100000 + Math.random() * 900000);
  try {
    sessionStorage.setItem(key, String(clientId));
  } catch {
    // Live captions can continue without persistence.
  }
  return clientId;
}

// Generate hello message
function getHelloMsg(session, last_segment, last_revision, history_seconds){
  return JSON.stringify({
    type: "CLIENT_HELLO",
    client_id: numericClientId(),
    session,
    last_segment: last_segment,
    last_revision: last_revision,
    history_seconds: history_seconds
  })
}

// Report messages and status changes without knowing how the UI uses them.
export function connect({ url, session, onMessage, onStatus, getResumePosition }) {
  onStatus("connecting");
  const ws = new WebSocket(url);

  // Web Socket opens - Send Initial Hello Message 
  ws.addEventListener("open", () => {
    onStatus("connected");
    const { last_segment, last_revision } = getResumePosition();
    ws.send(getHelloMsg(session, last_segment, last_revision, 60));
  });

  // Web socket receives incoming message - Pass to handler
  ws.addEventListener("message", (event) => {
    onMessage(JSON.parse(event.data));
  });

  // Websocket connection closed - Attempt autoreconnect sequence (with last segment and revision HELLO)
  ws.addEventListener("close", () => {
    onStatus("disconnected");
    setTimeout(() => {
      connect({ url, session, onMessage, onStatus, getResumePosition });
    }, 1000);
  });

  // Websocket error - show connection error to user
  ws.addEventListener("error", () => {
    onStatus("connection error");
  });

  return ws;
}
