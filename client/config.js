// Use the page's address by default. Separate frontend/backend deployments can
// specify ?serverPort=8765 or a full ?server=ws://backend-host:8765/captions URL.
export function getWebSocketUrl(pageHref = window.location.href) {
  const page = new URL(pageHref);
  const server = page.searchParams.get("server");
  const endpoint = server ? new URL(server) : new URL("/", page);
  if (endpoint.protocol === "http:") endpoint.protocol = "ws:";
  if (endpoint.protocol === "https:") endpoint.protocol = "wss:";
  if (endpoint.protocol !== "ws:" && endpoint.protocol !== "wss:") {
    throw new Error("The backend URL must use ws://, wss://, http://, or https://.");
  }

  // A full server URL takes precedence over the same-host port option.
  const port = page.searchParams.get("serverPort");
  if (!server && port !== null) {
    if (!/^\d+$/.test(port) || Number(port) < 1 || Number(port) > 65535) {
      throw new Error("serverPort must be a port number between 1 and 65535.");
    }
    endpoint.port = port;
  }
  return server ? endpoint.href : `${endpoint.protocol}//${endpoint.host}`;
}
