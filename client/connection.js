function numericClientId() {
  const key = "liverecall-client-id";
  let value = sessionStorage.getItem(key);
  if (!value) {
    value = String(Math.floor(100000 + Math.random() * 900000));
    sessionStorage.setItem(key, value);
  }
  return Number(value);
}

// Report messages and status changes without knowing how the UI uses them.
export function connect({ url, session, onMessage, onStatus }) {
  onStatus("connecting");
  const ws = new WebSocket(url);

  ws.addEventListener("open", () => {
    onStatus("connected");
    ws.send(JSON.stringify({
      type: "CLIENT_HELLO",
      client_id: numericClientId(),
      session,
      last_segment: 0, // TODO: resume latest finalized state.
      last_revision: 0,
      history_seconds: 30
    }));
  });

  ws.addEventListener("message", (event) => {
    onMessage(JSON.parse(event.data));
  });

  ws.addEventListener("close", () => {
    onStatus("disconnected");
    // TODO: automatic reconnect using finalized resume state.
  });

  ws.addEventListener("error", () => {
    onStatus("connection error");
  });

  return ws;
}
