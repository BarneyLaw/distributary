import { resetClientId } from "./connection.js";

// Testing controls belong here; caption rendering and protocol handling stay separate.
export function createTestingTools({ captions, connection, onBeforeReset = () => {} }) {
  const refreshButton = document.getElementById("refresh-client-session");
  const disconnectForm = document.getElementById("disconnect-form");
  const disconnectDuration = document.getElementById("disconnect-duration");
  const disconnectButton = document.getElementById("simulate-disconnection");
  const disconnectStatus = document.getElementById("disconnection-status");
  let simulationTimer = null;
  let restarting = false;

  function showSimulation(seconds) {
    const active = seconds > 0;
    disconnectButton.disabled = active;
    disconnectDuration.disabled = active;
    disconnectStatus.textContent = active
      ? `Disconnected for ${seconds} second${seconds === 1 ? "" : "s"}. Reconnecting automatically…`
      : "";
    disconnectStatus.hidden = !active;
  }

  refreshButton.addEventListener("click", () => {
    if (restarting || !window.confirm(
      "Refresh client session?\n\n" +
      "This will remove ALL captions saved for the current session in this tab! " +
      "and restart it with a new client ID. This cannot be undone."
    )) {
      return;
    }
    restarting = true;
    clearTimeout(simulationTimer);
    simulationTimer = null;
    connection.disconnect();
    try {
      captions.clear();
      resetClientId();
      onBeforeReset();
      window.location.reload();
    } catch {
      restarting = false;
      showSimulation(0);
      connection.reconnect();
      window.alert("The client session could not be restarted. Check your browser's site storage permissions and try again.");
    }
  });

  disconnectForm.addEventListener("submit", event => {
    event.preventDefault();
    if (restarting || simulationTimer !== null || !disconnectForm.reportValidity()) {
      return;
    }
    const seconds = disconnectDuration.valueAsNumber;
    if (!Number.isInteger(seconds) || seconds < 1 || seconds > 3600) {
      return;
    }

    connection.disconnect();
    showSimulation(seconds);
    simulationTimer = setTimeout(() => {
      simulationTimer = null;
      showSimulation(0);
      connection.reconnect();
    }, seconds * 1000);
  });
}
