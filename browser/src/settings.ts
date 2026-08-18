export type Backend = "claude" | "grok" | "mlx";

export type Settings = {
  backend: Backend;
  mlxUrl: string;
  mlxModel: string;
  hostUrl: string;
};

export const defaultSettings: Settings = {
  backend: "claude",
  mlxUrl: "http://127.0.0.1:8080/v1",
  mlxModel: "",
  hostUrl: "http://127.0.0.1:17342",
};

export function normalizeSettings(raw: Partial<Settings> | undefined): Settings {
  return {
    backend:
      raw?.backend === "grok" || raw?.backend === "mlx" || raw?.backend === "claude"
        ? raw.backend
        : defaultSettings.backend,
    mlxUrl: (raw?.mlxUrl || defaultSettings.mlxUrl).trim(),
    mlxModel: (raw?.mlxModel || "").trim(),
    hostUrl: (raw?.hostUrl || defaultSettings.hostUrl).trim().replace(/\/$/, ""),
  };
}
