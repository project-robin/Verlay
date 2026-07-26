const tauri = window.__TAURI__;

const elements = {
  sendButton: document.querySelector("#send-button"),
  receiveButton: document.querySelector("#receive-button"),
  codeInput: document.querySelector("#code-input"),
  statusCard: document.querySelector("#status-card"),
  statusTitle: document.querySelector("#status-title"),
  statusMessage: document.querySelector("#status-message"),
  codeCard: document.querySelector("#code-card"),
  transferCode: document.querySelector("#transfer-code"),
  copyButton: document.querySelector("#copy-button"),
  cancelButton: document.querySelector("#cancel-button"),
  setupError: document.querySelector("#setup-error"),
};

const terminalStatuses = new Set(["completed", "failed", "cancelled"]);
let transferActive = false;

function setControlsBusy(busy) {
  transferActive = busy;
  elements.sendButton.disabled = busy;
  elements.receiveButton.disabled = busy;
  elements.codeInput.disabled = busy;
  elements.cancelButton.hidden = !busy;
}

function statusLabel(status) {
  return {
    preparing: "Preparing",
    waiting: "Waiting for the other device",
    transferring: "Transferring",
    completed: "Transfer complete",
    failed: "Couldn’t transfer",
    cancelled: "Transfer cancelled",
  }[status] ?? "Transfer status";
}

function showStatus(event) {
  elements.statusCard.hidden = false;
  elements.statusCard.dataset.status = event.status;
  elements.statusTitle.textContent = statusLabel(event.status);
  elements.statusMessage.textContent = event.message;

  if (event.code) {
    elements.transferCode.textContent = event.code;
    elements.codeCard.hidden = false;
  }

  if (terminalStatuses.has(event.status)) {
    setControlsBusy(false);
  }
}

function showSetupError(message) {
  elements.setupError.textContent = message;
  elements.setupError.hidden = false;
}

async function invoke(command, args = {}) {
  if (!tauri?.core?.invoke) {
    throw new Error("The desktop bridge did not load. Run this page through the Tauri app.");
  }
  return tauri.core.invoke(command, args);
}

elements.sendButton.addEventListener("click", async () => {
  try {
    const path = await invoke("choose_file");
    if (!path) return;
    elements.codeCard.hidden = true;
    setControlsBusy(true);
    await invoke("send_file", { path });
  } catch (error) {
    setControlsBusy(false);
    showStatus({ status: "failed", message: String(error) });
  }
});

elements.receiveButton.addEventListener("click", async () => {
  const code = elements.codeInput.value.trim();
  if (!code) {
    elements.codeInput.focus();
    showStatus({ status: "failed", message: "Enter the code shown on your phone first." });
    return;
  }

  try {
    const directory = await invoke("choose_directory");
    if (!directory) return;
    elements.codeCard.hidden = true;
    setControlsBusy(true);
    await invoke("receive_file", { code, directory });
  } catch (error) {
    setControlsBusy(false);
    showStatus({ status: "failed", message: String(error) });
  }
});

elements.cancelButton.addEventListener("click", async () => {
  if (!transferActive) return;
  elements.cancelButton.disabled = true;
  try {
    await invoke("cancel_transfer");
  } catch (error) {
    showStatus({ status: "failed", message: String(error) });
  } finally {
    elements.cancelButton.disabled = false;
  }
});

elements.copyButton.addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(elements.transferCode.textContent);
    elements.copyButton.textContent = "Copied";
    window.setTimeout(() => {
      elements.copyButton.textContent = "Copy code";
    }, 1400);
  } catch {
    elements.copyButton.textContent = "Select and copy the code";
  }
});

async function start() {
  if (!tauri?.event?.listen) {
    showSetupError("This interface must be opened through Croc Transfer, not in a browser.");
    return;
  }

  await tauri.event.listen("transfer-state", ({ payload }) => showStatus(payload));
}

start().catch((error) => showSetupError(String(error)));
