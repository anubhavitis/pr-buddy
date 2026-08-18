export type GuideFile = {
  path: string;
  blurb: string;
};

export type GuideGroup = {
  name: string;
  summary: string;
  files: GuideFile[];
};

export type Guide = {
  groups: GuideGroup[];
};

export type OrderedFile = {
  path: string;
  group: string;
  groupSummary: string;
  blurb: string;
};

export function parseGuide(raw: string): Guide {
  const parsed = JSON.parse(extractJSON(raw)) as { groups?: unknown };
  if (!Array.isArray(parsed.groups)) {
    throw new Error("guide is missing groups");
  }
  const groups: GuideGroup[] = [];
  for (const g of parsed.groups) {
    if (!g || typeof g !== "object") continue;
    const rec = g as Record<string, unknown>;
    const name = String(rec.name ?? "").trim();
    if (!name) continue;
    const files: GuideFile[] = [];
    if (Array.isArray(rec.files)) {
      for (const f of rec.files) {
        if (!f || typeof f !== "object") continue;
        const fr = f as Record<string, unknown>;
        const path = String(fr.path ?? "").trim().replace(/^\.\//, "");
        if (!path) continue;
        files.push({ path, blurb: String(fr.blurb ?? "").trim() });
      }
    }
    groups.push({
      name,
      summary: String(rec.summary ?? "").trim(),
      files,
    });
  }
  if (groups.length === 0) {
    throw new Error("guide has no groups");
  }
  return { groups };
}

export function flattenGuide(guide: Guide, prPaths: string[]): OrderedFile[] {
  const present = new Set(prPaths);
  const seen = new Set<string>();
  const out: OrderedFile[] = [];
  for (const group of guide.groups) {
    for (const file of group.files) {
      if (!present.has(file.path) || seen.has(file.path)) continue;
      seen.add(file.path);
      out.push({
        path: file.path,
        group: group.name,
        groupSummary: group.summary,
        blurb: file.blurb,
      });
    }
  }
  for (const path of prPaths) {
    if (seen.has(path)) continue;
    out.push({ path, group: "Other", groupSummary: "", blurb: "" });
  }
  return out;
}

// Bump when parseGuide or the prompt JSON shape changes so stored guides
// produced under the old contract cannot be served.
export const GuideCacheVersion = 1;

export function fileSetSignature(paths: string[]): string {
  return [...new Set(paths)].sort().join("\n");
}

export function cacheKey(p: {
  owner: string;
  repo: string;
  number: number;
  headSHA: string;
  backend: string;
  files?: string[];
}): string {
  return [
    `${p.owner}/${p.repo}#${p.number}`,
    p.headSHA || "no-sha",
    p.backend,
    `v${GuideCacheVersion}`,
    fileSetSignature(p.files ?? []),
  ].join(":");
}

function extractJSON(raw: string): string {
  const trimmed = raw.trim();
  const fenced = trimmed.match(/```(?:json)?\s*([\s\S]*?)```/i);
  const candidate = (fenced ? fenced[1] : trimmed).trim();
  const start = candidate.indexOf("{");
  const end = candidate.lastIndexOf("}");
  if (start === -1 || end === -1 || end <= start) {
    throw new Error("guide is not JSON");
  }
  return candidate.slice(start, end + 1);
}
