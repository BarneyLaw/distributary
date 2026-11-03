let clientId;
const clientIdKey = "liverecall-client-id";

function generateClientId(previousId) {
  const value = Math.floor(100000 + Math.random() * 900000);
  return value === previousId ? (value === 999999 ? 100000 : value + 1) : value;
}

// Save a different ID for the next connection and the page reload that follows.
export function resetClientId() {
  const nextId = generateClientId(getClientId());
  let storage;
  try {
    storage = globalThis.sessionStorage;
  } catch {
    // If storage access is blocked entirely, the next page starts in memory too.
  }
  storage?.setItem(clientIdKey, String(nextId));
  clientId = nextId;
  return clientId;
}

// Keep one client ID per tab, including across reloads and reconnects.
export function getClientId() {
  if (clientId !== undefined) {
    return clientId;
  }
  try {
    const value = Number(sessionStorage.getItem(clientIdKey));
    if (Number.isSafeInteger(value) && value > 0) {
      clientId = value;
      return clientId;
    }
  } catch {
    // Retain an in-memory ID when browser storage is unavailable.
  }
  clientId = generateClientId();
  try {
    sessionStorage.setItem(clientIdKey, String(clientId));
  } catch {
    // Live captions can continue without persistence.
  }
  return clientId;
}

// Generate hello message
function getHelloMsg(session, last_segment, last_revision, history_seconds){
  return JSON.stringify({
    type: "CLIENT_HELLO",
    client_id: getClientId(),
    session,
    last_segment: last_segment,
    last_revision: last_revision,
    history_seconds: history_seconds
  })
}

// Report messages and status changes without knowing how the UI uses them.
export function connect({ url, session, onMessage, onStatus, getResumePosition }) {
  let ws = null;
  let reconnectTimer = null;
  let stopped = false;

  function openSocket() {
    if (stopped) {
      return;
    }
    onStatus("connecting");
    const socket = new WebSocket(url);
    ws = socket;
    const isCurrent = () => ws === socket && !stopped;

    socket.addEventListener("open", () => {
      if (!isCurrent()) return;
      onStatus("connected");
      const { last_segment, last_revision } = getResumePosition();
      socket.send(getHelloMsg(session, last_segment, last_revision, 60));
    });

    socket.addEventListener("message", (event) => {
      if (isCurrent()) {
        onMessage(JSON.parse(event.data));
      }
    });

    socket.addEventListener("close", () => {
      if (!isCurrent()) return;
      ws = null;
      onStatus("disconnected");
      reconnectTimer = setTimeout(() => {
        reconnectTimer = null;
        openSocket();
      }, 1000);
    });

    socket.addEventListener("error", () => {
      if (isCurrent()) onStatus("connection error");
    });
  }

  function closeSocket() {
    const socket = ws;
    // Ignore queued events from this socket, including its close-triggered retry.
    ws = null;
    socket?.close();
  }

  function reconnect() {
    if (!stopped) return;
    stopped = false;
    openSocket();
  }

  function disconnect() {
    stopped = true;
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
    closeSocket();
    onStatus("disconnected");
  }

  openSocket();
  return { reconnect, disconnect };
}
