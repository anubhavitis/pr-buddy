import { isFilesTab, parsePRURL } from "./pr";
import { defaultSettings, type Settings } from "./settings";

const page = document.getElementById("page")!;
const host = document.getElementById("host")!;

void chrome.tabs.query({ active: true, currentWindow: true }).then(([tab]) => {
  const url = tab?.url || "";
  const pr = url ? parsePRURL(url) : null;
  if (!pr) {
    page.textContent = "Open a GitHub pull request.";
    return;
  }
  if (isFilesTab(url)) {
    page.textContent = `${pr.owner}/${pr.repo}#${pr.number} · Files tab`;
    return;
  }
  page.textContent = `${pr.owner}/${pr.repo}#${pr.number} · open Files changed`;
});

void fetch("http://127.0.0.1:17342/health")
  .then((r) => {
    host.className = r.ok ? "ok" : "bad";
    host.textContent = r.ok ? "host: running on :17342" : `host: HTTP ${r.status}`;
  })
  .catch(() => {
    host.className = "bad";
    host.textContent = "host: offline — run ./pr-buddy-host";
  });

const backend = document.getElementById("backend") as HTMLSelectElement;
const mlxFields = document.getElementById("mlx-fields")!;
const mlxUrl = document.getElementById("mlx-url") as HTMLInputElement;
const mlxModel = document.getElementById("mlx-model") as HTMLInputElement;

function showSettings(s: Settings): void {
  backend.value = s.backend;
  mlxUrl.value = s.mlxUrl;
  mlxModel.value = s.mlxModel;
  mlxFields.hidden = s.backend !== "mlx";
}

void chrome.runtime.sendMessage({ type: "getSettings" }).then((s: Settings) => {
  showSettings(s ?? defaultSettings);
});

backend.addEventListener("change", () => {
  void chrome.runtime
    .sendMessage({ type: "setSettings", settings: { backend: backend.value } })
    .then((s: Settings) => showSettings(s));
});
mlxUrl.addEventListener("change", () => {
  void chrome.runtime.sendMessage({ type: "setSettings", settings: { mlxUrl: mlxUrl.value } });
});
mlxModel.addEventListener("change", () => {
  void chrome.runtime.sendMessage({ type: "setSettings", settings: { mlxModel: mlxModel.value } });
});

document.getElementById("reload")?.addEventListener("click", () => {
  void chrome.tabs.query({ active: true, currentWindow: true }).then(([tab]) => {
    if (tab?.id != null) void chrome.tabs.reload(tab.id);
  });
});
