import type { Settings } from "./settings";

export type CompleteRequest = {
  backend: string;
  prompt: string;
  mlx_url: string;
  mlx_model: string;
};

export function completeRequest(settings: Settings, prompt: string): CompleteRequest {
  return {
    backend: settings.backend,
    prompt,
    mlx_url: settings.mlxUrl,
    mlx_model: settings.mlxModel,
  };
}
