import { fillModelControl, modelsForBackend, type ModelsResponse } from "./models";
import { isFilesTab, parsePRURL } from "./pr";
import { promptTemplate } from "./prompt";
import {
  defaultSettings,
  modelSettingKey,
  normalizeSettings,
  selectedModel,
  type Backend,
  type Settings,
} from "./settings";

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
const model = document.getElementById("model") as HTMLInputElement;
const modelPick = document.getElementById("model-pick") as HTMLSelectElement;
const prompt = document.getElementById("prompt")!;
prompt.textContent = promptTemplate();

let live = defaultSettings;

function showSettings(raw: Settings): void {
  live = normalizeSettings(raw);
  backend.value = live.backend;
  mlxUrl.value = live.mlxUrl;
  mlxFields.hidden = live.backend !== "mlx";
  paintModels(modelsForBackend(live.backend));
  void loadModels();
}

function paintModels(models: { id: string; label?: string }[]): void {
  fillModelControl({
    select: modelPick,
    input: model,
    models,
    value: selectedModel(live),
    emptyLabel: live.backend === "mlx" ? "model id" : "CLI default",
  });
}

function saveModel(value: string): void {
  const key = modelSettingKey(live.backend);
  live = { ...live, [key]: value };
  void chrome.runtime.sendMessage({ type: "setSettings", settings: { [key]: value } });
}

async function loadModels(): Promise<void> {
  const res = (await chrome.runtime.sendMessage({
    type: "listModels",
    backend: live.backend,
  })) as ModelsResponse;
  paintModels(modelsForBackend(live.backend, res?.ok ? res.models ?? [] : []));
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
  showSettings(s ?? defaultSettings);
  pingHost(live.hostUrl);
});

backend.addEventListener("change", () => {
  void chrome.runtime
    .sendMessage({ type: "setSettings", settings: { backend: backend.value } })
    .then((s: Settings) => showSettings(s));
});
mlxUrl.addEventListener("change", () => {
  live = { ...live, mlxUrl: mlxUrl.value };
  void chrome.runtime.sendMessage({ type: "setSettings", settings: { mlxUrl: mlxUrl.value } }).then(() => {
    if (live.backend === "mlx") void loadModels();
  });
});
model.addEventListener("change", () => saveModel(model.value));
modelPick.addEventListener("change", () => saveModel(modelPick.value));

document.getElementById("reload")?.addEventListener("click", () => {
  void chrome.tabs.query({ active: true, currentWindow: true }).then(([tab]) => {
    if (tab?.id != null) void chrome.tabs.reload(tab.id);
  });
});
