export type ModelInfo = {
  id: string;
  label?: string;
};

export const CLAUDE_MODELS: ModelInfo[] = [
  { id: "sonnet" },
  { id: "opus" },
  { id: "haiku" },
  { id: "fable" },
];

export function modelsForBackend(backend: string, fetched?: ModelInfo[] | null): ModelInfo[] {
  if (fetched && fetched.length > 0) return fetched;
  if (backend === "claude") return CLAUDE_MODELS;
  return [];
}

export type ModelsResponse = {
  ok: boolean;
  backend?: string;
  default?: string;
  models?: ModelInfo[];
  error?: string;
};

export function modelsPath(hostUrl: string, backend: string, mlxUrl?: string): string {
  const q = new URLSearchParams({ backend });
  if (backend === "mlx" && mlxUrl) q.set("mlx_url", mlxUrl);
  return `${hostUrl.replace(/\/$/, "")}/models?${q.toString()}`;
}

export function fillModelControl(opts: {
  select: HTMLSelectElement;
  input: HTMLInputElement;
  models: ModelInfo[];
  value: string;
  emptyLabel: string;
}): "select" | "input" {
  const has = opts.models.length > 0;
  opts.select.hidden = !has;
  opts.input.hidden = has;
  if (!has) {
    opts.input.placeholder = opts.emptyLabel;
    opts.input.value = opts.value;
    return "input";
  }
  const seen = new Set<string>([""]);
  opts.select.replaceChildren();
  const def = document.createElement("option");
  def.value = "";
  def.textContent = opts.emptyLabel;
  opts.select.append(def);
  for (const m of opts.models) {
    const id = m.id.trim();
    if (!id || seen.has(id)) continue;
    seen.add(id);
    const o = document.createElement("option");
    o.value = id;
    o.textContent = m.label || id;
    opts.select.append(o);
  }
  if (opts.value && !seen.has(opts.value)) {
    const o = document.createElement("option");
    o.value = opts.value;
    o.textContent = opts.value;
    opts.select.append(o);
  }
  opts.select.value = opts.value;
  return "select";
}
