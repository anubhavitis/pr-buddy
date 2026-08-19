export type Backend = "claude" | "grok" | "mlx";

export type Settings = {
  backend: Backend;
  mlxUrl: string;
  mlxModel: string;
  claudeModel: string;
  grokModel: string;
  hostUrl: string;
};

export const defaultSettings: Settings = {
  backend: "claude",
  mlxUrl: "http://127.0.0.1:8080/v1",
  mlxModel: "",
  claudeModel: "",
  grokModel: "",
  hostUrl: "http://127.0.0.1:17342",
};

function cleanModel(raw: unknown): string {
  const s = typeof raw === "string" ? raw.trim() : "";
  if (!s || s === "undefined" || s === "null") return "";
  return s;
}

export function normalizeSettings(raw: Partial<Settings> | undefined): Settings {
  return {
    backend:
      raw?.backend === "grok" || raw?.backend === "mlx" || raw?.backend === "claude"
        ? raw.backend
        : defaultSettings.backend,
    mlxUrl: (raw?.mlxUrl || defaultSettings.mlxUrl).trim(),
    mlxModel: cleanModel(raw?.mlxModel),
    claudeModel: cleanModel(raw?.claudeModel),
    grokModel: cleanModel(raw?.grokModel),
    hostUrl: (raw?.hostUrl || defaultSettings.hostUrl).trim().replace(/\/$/, ""),
  };
}

export function modelSettingKey(backend: Backend): "claudeModel" | "grokModel" | "mlxModel" {
  if (backend === "grok") return "grokModel";
  if (backend === "mlx") return "mlxModel";
  return "claudeModel";
}

export function selectedModel(s: Settings): string {
  return s[modelSettingKey(s.backend)];
}

export function modelLabel(s: Settings): string {
  return selectedModel(s) || s.backend;
}
