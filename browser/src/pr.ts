export type PRId = {
  owner: string;
  repo: string;
  number: number;
};

export function parsePRURL(href: string): PRId | null {
  const u = new URL(href);
  const m = u.pathname.match(/^\/([^/]+)\/([^/]+)\/pull\/(\d+)(?:\/|$)/);
  if (!m) return null;
  return { owner: m[1], repo: m[2], number: Number(m[3]) };
}

export function isFilesTab(href: string): boolean {
  try {
    // New GitHub Files experience uses /changes; the old one uses /files.
    return /\/pull\/\d+\/(files|changes)\b/.test(new URL(href).pathname);
  } catch {
    return false;
  }
}

export function isFilesView(href: string, doc?: { querySelector(sel: string): unknown }): boolean {
  if (isFilesTab(href)) return true;
  if (!doc || !parsePRURL(href)) return false;
  return Boolean(
    doc.querySelector(
      [
        'a[href$="/files"][aria-current="page"]',
        'a[href$="/changes"][aria-current="page"]',
        '[data-tab-item="files_tab"][aria-current="page"]',
        '[data-tab-item="files_tab"].selected',
        "file-tree",
        "copilot-diff-entry",
        "[role='tree']",
        "#file-tree-filter-field",
      ].join(","),
    ),
  );
}

export function parseHeadSHA(html: string): string {
  const decoded = html.replace(/&amp;/g, "&");
  const m = decoded.match(/[?&]sha2=([a-f0-9]{40})/i);
  return m ? m[1] : "";
}

export function findHeadSHAFromDOM(root: ParentNode): string {
  for (const el of root.querySelectorAll("[href], [src]")) {
    const v = (el.getAttribute("href") || el.getAttribute("src") || "").replace(/&amp;/g, "&");
    const m = v.match(/[?&]sha2=([a-f0-9]{40})/i);
    if (m) return m[1];
  }
  return "";
}

export function collectFilePathsFromDOM(root: ParentNode): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  const add = (raw: string) => {
    const path = decodeEntities(raw).trim().replace(/^\.\//, "");
    if (!path || path.includes(" ") || path.includes("\n") || seen.has(path)) return;
    if (path.length > 512) return;
    seen.add(path);
    out.push(path);
  };
  for (const el of root.querySelectorAll(
    "[data-file-path], [data-tagsearch-path], [data-path], [data-filterable-item-text]",
  )) {
    add(
      el.getAttribute("data-file-path") ||
        el.getAttribute("data-tagsearch-path") ||
        el.getAttribute("data-path") ||
        el.textContent ||
        "",
    );
  }
  return out;
}

export function parseTitle(html: string): string {
  const m = html.match(/js-issue-title[^>]*>([^<]+)/);
  return m ? decodeEntities(m[1]).trim() : "";
}

export function parseFilePaths(html: string): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  const patterns = [
    /data-filterable-item-text[^>]*>([^<]+)/g,
    /data-file-path="([^"]+)"/g,
    /data-tagsearch-path="([^"]+)"/g,
    /data-path="([^"]+)"/g,
  ];
  for (const re of patterns) {
    for (const m of html.matchAll(re)) {
      const path = decodeEntities(m[1]).trim();
      if (!path || path.includes(" ") || seen.has(path)) continue;
      seen.add(path);
      out.push(path);
    }
  }
  return out;
}

export function parseBody(html: string): string {
  const m = html.match(
    /<(?:td|div)[^>]*class="[^"]*js-comment-body[^"]*"[^>]*>([\s\S]*?)<\/(?:td|div)>/,
  );
  if (!m) return "";
  return decodeEntities(m[1].replace(/<[^>]+>/g, " ").replace(/\s+/g, " ")).trim();
}

function decodeEntities(s: string): string {
  return s
    .replace(/&amp;/g, "&")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'");
}
