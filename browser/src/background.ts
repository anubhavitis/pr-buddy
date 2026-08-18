import { cacheKey } from "./guide";
import { completeRequest } from "./host";
import { normalizeSettings, type Settings } from "./settings";

type CompleteMsg = {
  type: "complete";
  owner: string;
  repo: string;
  number: number;
  headSHA: string;
  files?: string[];
  prompt: string;
  force?: boolean;
};

type GetSettingsMsg = { type: "getSettings" };
type SetSettingsMsg = { type: "setSettings"; settings: Partial<Settings> };

chrome.tabs.onUpdated.addListener((tabId, info, tab) => {
  const url = info.url || tab.url;
  if (!url || !url.startsWith("https://github.com/")) return;
  if (info.status !== "complete" && !info.url) return;
  void chrome.scripting
    .executeScript({ target: { tabId }, files: ["out/content.js"] })
    .catch(() => undefined);
  void chrome.scripting
    .insertCSS({ target: { tabId }, files: ["out/styles.css"] })
    .catch(() => undefined);
});

chrome.runtime.onMessage.addListener((msg: CompleteMsg | GetSettingsMsg | SetSettingsMsg, _s, send) => {
  if (msg.type === "getSettings") {
    chrome.storage.local.get("settings").then((stored) => {
      send(normalizeSettings(stored.settings as Partial<Settings> | undefined));
    });
    return true;
  }
  if (msg.type === "setSettings") {
    chrome.storage.local.get("settings").then((stored) => {
      const next = normalizeSettings({
        ...(stored.settings as Partial<Settings> | undefined),
        ...msg.settings,
      });
      return chrome.storage.local.set({ settings: next }).then(() => send(next));
    });
    return true;
  }
  if (msg.type === "complete") {
    complete(msg).then(send).catch((err: unknown) => {
      send({ ok: false, error: err instanceof Error ? err.message : String(err) });
    });
    return true;
  }
  return false;
});

async function complete(msg: CompleteMsg): Promise<{ ok: boolean; text?: string; error?: string; cached?: boolean }> {
  const settings = normalizeSettings(
    ((await chrome.storage.local.get("settings")).settings as Partial<Settings> | undefined),
  );
  const key = cacheKey({
    owner: msg.owner,
    repo: msg.repo,
    number: msg.number,
    headSHA: msg.headSHA || "no-sha",
    backend: settings.backend,
    files: msg.files,
  });
  if (!msg.force) {
    const cached = (await chrome.storage.local.get(key))[key] as string | undefined;
    if (cached) return { ok: true, text: cached, cached: true };
  }
  const res = await fetch(`${settings.hostUrl}/complete`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(completeRequest(settings, msg.prompt)),
  });
  const body = (await res.json()) as { ok?: boolean; text?: string; error?: string };
  if (!res.ok || !body.ok || !body.text) {
    return { ok: false, error: body.error || `host HTTP ${res.status}` };
  }
  await chrome.storage.local.set({ [key]: body.text });
  return { ok: true, text: body.text };
}
