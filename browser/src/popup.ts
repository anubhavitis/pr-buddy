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

function pingHost(url: string): void {
  void fetch(`${url.replace(/\/$/, "")}/health`)
    .then((r) => {
      host.className = r.ok ? "ok" : "bad";
      host.textContent = r.ok ? `host: running at ${url}` : `host: HTTP ${r.status}`;
    })
    .catch(() => {
      host.className = "bad";
      host.textContent = "host: offline — run ./pr-buddy-host";
    });
}

void chrome.runtime.sendMessage({ type: "getSettings" }).then((s: Settings) => {
  const settings = s ?? defaultSettings;
  showSettings(settings);
  pingHost(settings.hostUrl);
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
