import type { OrderedFile } from "./guide";

export type OverlayGroup = {
  name: string;
  summary: string;
  files: OrderedFile[];
};

export function orderedPaths(rows: OrderedFile[]): string[] {
  return rows.map((row) => row.path);
}

export function sha256Hex(text: string): string {
  const bytes = sha256Bytes(new TextEncoder().encode(text));
  return [...bytes].map((b) => b.toString(16).padStart(2, "0")).join("");
}

export function diffAnchor(path: string): string {
  return `#diff-${sha256Hex(path)}`;
}

export function pathForDiffFragment(paths: string[], fragment: string): string {
  const m = fragment.match(/^#?diff-([a-f0-9]{64})$/i);
  if (!m) return "";
  const want = m[1].toLowerCase();
  return paths.find((p) => sha256Hex(p) === want) ?? "";
}

export function fileNoteCopy(file: { blurb: string; groupSummary: string }): string {
  return (file.blurb || file.groupSummary).trim();
}

export function pathFromVisibleLabel(label: string, known: string[]): string {
  const t = label.replace(/\s+/g, " ").trim();
  if (!t) return "";
  const exact = known.find((p) => p === t || t.endsWith(p) || t.endsWith(p.split("/").pop() || p));
  if (exact) return exact;
  const name = t.split("/").pop() || t;
  const hits = known.filter((p) => (p.split("/").pop() || p) === name);
  return hits.length === 1 ? hits[0] : "";
}

export function currentNotePath(opts: {
  paths: string[];
  hash: string;
  activePath: string;
}): string {
  return pathForDiffFragment(opts.paths, opts.hash) || opts.activePath || opts.paths[0] || "";
}

export function isDiffRegionId(id: string): boolean {
  return /^diff-[a-f0-9]{64}$/i.test(id);
}

export function pickNoteInsert(card: {
  hasHeaderWrapper: boolean;
  hasDiffTable: boolean;
}): "inside-table-wrap" | "after-header-wrapper" | "prepend" {
  if (card.hasDiffTable) return "inside-table-wrap";
  if (card.hasHeaderWrapper) return "after-header-wrapper";
  return "prepend";
}

function sha256Bytes(message: Uint8Array): Uint8Array {
  const K = [
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
  ];
  let h0 = 0x6a09e667;
  let h1 = 0xbb67ae85;
  let h2 = 0x3c6ef372;
  let h3 = 0xa54ff53a;
  let h4 = 0x510e527f;
  let h5 = 0x9b05688c;
  let h6 = 0x1f83d9ab;
  let h7 = 0x5be0cd19;
  const bitLen = message.length * 8;
  const withOne = message.length + 1;
  const pad = (64 - ((withOne + 8) % 64)) % 64;
  const block = new Uint8Array(withOne + pad + 8);
  block.set(message);
  block[message.length] = 0x80;
  const view = new DataView(block.buffer);
  view.setUint32(block.length - 4, bitLen >>> 0);
  const rr = (x: number, n: number) => (x >>> n) | (x << (32 - n));
  for (let i = 0; i < block.length; i += 64) {
    const w = new Uint32Array(64);
    for (let t = 0; t < 16; t++) w[t] = view.getUint32(i + t * 4);
    for (let t = 16; t < 64; t++) {
      const s0 = rr(w[t - 15], 7) ^ rr(w[t - 15], 18) ^ (w[t - 15] >>> 3);
      const s1 = rr(w[t - 2], 17) ^ rr(w[t - 2], 19) ^ (w[t - 2] >>> 10);
      w[t] = (w[t - 16] + s0 + w[t - 7] + s1) >>> 0;
    }
    let a = h0;
    let b = h1;
    let c = h2;
    let d = h3;
    let e = h4;
    let f = h5;
    let g = h6;
    let h = h7;
    for (let t = 0; t < 64; t++) {
      const S1 = rr(e, 6) ^ rr(e, 11) ^ rr(e, 25);
      const ch = (e & f) ^ (~e & g);
      const t1 = (h + S1 + ch + K[t] + w[t]) >>> 0;
      const S0 = rr(a, 2) ^ rr(a, 13) ^ rr(a, 22);
      const maj = (a & b) ^ (a & c) ^ (b & c);
      const t2 = (S0 + maj) >>> 0;
      h = g;
      g = f;
      f = e;
      e = (d + t1) >>> 0;
      d = c;
      c = b;
      b = a;
      a = (t1 + t2) >>> 0;
    }
    h0 = (h0 + a) >>> 0;
    h1 = (h1 + b) >>> 0;
    h2 = (h2 + c) >>> 0;
    h3 = (h3 + d) >>> 0;
    h4 = (h4 + e) >>> 0;
    h5 = (h5 + f) >>> 0;
    h6 = (h6 + g) >>> 0;
    h7 = (h7 + h) >>> 0;
  }
  const out = new Uint8Array(32);
  const outView = new DataView(out.buffer);
  outView.setUint32(0, h0);
  outView.setUint32(4, h1);
  outView.setUint32(8, h2);
  outView.setUint32(12, h3);
  outView.setUint32(16, h4);
  outView.setUint32(20, h5);
  outView.setUint32(24, h6);
  outView.setUint32(28, h7);
  return out;
}

export type PathTreeNode =
  | { kind: "dir"; name: string; children: PathTreeNode[] }
  | { kind: "file"; name: string; path: string; index: number };

export function pathsToTree(paths: string[]): PathTreeNode[] {
  type Dir = { kind: "dir"; name: string; children: PathTreeNode[]; files: Map<string, PathTreeNode> };
  const root: Dir = { kind: "dir", name: "", children: [], files: new Map() };

  const dirOf = (parent: Dir, name: string): Dir => {
    const existing = parent.children.find((c) => c.kind === "dir" && c.name === name) as Dir | undefined;
    if (existing) return existing;
    const created: Dir = { kind: "dir", name, children: [], files: new Map() };
    parent.children.push(created);
    return created;
  };

  paths.forEach((path, i) => {
    const parts = path.split("/").filter(Boolean);
    if (parts.length === 0) return;
    let dir = root;
    for (const part of parts.slice(0, -1)) dir = dirOf(dir, part);
    const name = parts[parts.length - 1];
    if (dir.files.has(name)) return;
    const file: PathTreeNode = { kind: "file", name, path, index: i + 1 };
    dir.files.set(name, file);
    dir.children.push(file);
  });

  const strip = (nodes: PathTreeNode[]): PathTreeNode[] =>
    nodes.map((n) => (n.kind === "dir" ? { kind: "dir", name: n.name, children: strip(n.children) } : n));

  return collapseUnaryDirs(strip(root.children));
}

export function collapseUnaryDirs(nodes: PathTreeNode[]): PathTreeNode[] {
  return nodes.map((node) => {
    if (node.kind !== "dir") return node;
    let name = node.name;
    let children = collapseUnaryDirs(node.children);
    while (children.length === 1 && children[0].kind === "dir") {
      const only = children[0];
      name = `${name}/${only.name}`;
      children = only.children;
    }
    return { kind: "dir", name, children };
  });
}

export function groupRows(rows: OrderedFile[]): OverlayGroup[] {
  const groups: OverlayGroup[] = [];
  for (const row of rows) {
    const last = groups[groups.length - 1];
    if (last && last.name === row.group) {
      last.files.push(row);
      continue;
    }
    groups.push({
      name: row.group,
      summary: row.groupSummary,
      files: [row],
    });
  }
  return groups;
}

export function isMergeStatusLabel(text: string): boolean {
  const t = text.replace(/\s+/g, " ").trim();
  if (!t || t.length > 48) return false;
  return /^(Ready to merge|Review required|Merging is blocked|This branch has conflicts|This branch is out-of-date|Draft)$/i.test(
    t,
  );
}

export function findMergeStatusControl(root: ParentNode): Element | null {
  let best: Element | null = null;
  let bestLen = Infinity;
  for (const el of root.querySelectorAll("button, a, [role='button'], span")) {
    const t = (el.textContent || "").replace(/\s+/g, " ").trim();
    if (!isMergeStatusLabel(t)) continue;
    if (t.length >= bestLen) continue;
    best = el.closest("button, a, [role='button']") || el;
    bestLen = t.length;
  }
  return best;
}

export function isToolbarCompanionLabel(text: string): boolean {
  return /^(Code|Preview)$/i.test(text.replace(/\s+/g, " ").trim());
}

export function isCommitsPickerLabel(text: string): boolean {
  const t = text.replace(/\s+/g, " ").trim();
  if (!t || t.length > 32) return false;
  return /^(All commits|\d+\s+commits?)$/i.test(t);
}

export function shortStatus(raw: string): string {
  const t = raw.replace(/\s+/g, " ").trim();
  if (!t) return "";
  if (/Failed to fetch|NetworkError|host offline/i.test(t)) return "host offline";
  if (/^claude\b|\bclaude\b.*exit status/i.test(t)) return "claude failed";
  if (/^grok\b|\bgrok\b.*exit status/i.test(t)) return "grok failed";
  if (/\bmlx\b|loopback/i.test(t)) return "mlx failed";
  if (t.length > 36) return `${t.slice(0, 33)}…`;
  return t;
}

function nodeHasLabel(el: Element, pred: (text: string) => boolean): boolean {
  if (pred((el.textContent || "").replace(/\s+/g, " ").trim())) return true;
  for (const child of el.querySelectorAll("button, a, [role='button'], summary, span")) {
    if (pred((child.textContent || "").replace(/\s+/g, " ").trim())) return true;
  }
  return false;
}

export function fileSetSignature(paths: string[]): string {
  return [...new Set(paths)].sort().join("\n");
}

export function shouldRedockPanel(panelParent: object | null, toolbarParent: object): boolean {
  return panelParent !== toolbarParent;
}

export function isOurMutationTarget(id: string | null | undefined): boolean {
  return Boolean(id && id.startsWith("pr-buddy-"));
}

export function isFileFilterField(placeholder: string, ariaLabel: string): boolean {
  return /filter\s+(changed\s+)?files/i.test(`${placeholder} ${ariaLabel}`);
}

export function isFilesToolbarRow(el: {
  component: string | null;
  direction: string | null;
  hasTreeToggle: boolean;
}): boolean {
  return el.component === "Stack" && el.direction === "horizontal" && el.hasTreeToggle;
}

export function commitsDockAction(parent: {
  isFilesToolbarRow: boolean;
  hasFileFilter: boolean;
  hasFileTree: boolean;
}): "walk" | "insert" | "abort" {
  if (parent.hasFileFilter || parent.hasFileTree) return "abort";
  if (parent.isFilesToolbarRow) return "insert";
  return "walk";
}

export type TreeItem = {
  level: number;
  name: string;
  kind: "file" | "directory";
};

export function pathsFromTreeItems(items: TreeItem[]): string[] {
  const stack: string[] = [];
  const files: string[] = [];
  const seen = new Set<string>();
  for (const item of items) {
    const name = item.name.trim().replace(/\/$/, "");
    if (!name) continue;
    const level = Math.max(1, item.level);
    stack.length = level - 1;
    if (item.kind === "directory") {
      stack.push(name);
      continue;
    }
    const path = name.includes("/") ? name : [...stack, name].join("/");
    if (!path || seen.has(path)) continue;
    seen.add(path);
    files.push(path);
  }
  return files;
}

export function findFileTreeHost(root: ParentNode): Element | null {
  const classic = root.querySelector("file-tree");
  if (classic) return classic;

  const labeled = root.querySelector("[role='tree'][aria-label*='file' i], [role='tree'][aria-label*='File']");
  if (labeled) return labeled.parentElement || labeled;

  const input = [...root.querySelectorAll("input")].find((el) =>
    isFileFilterField(el.getAttribute("placeholder") || "", el.getAttribute("aria-label") || ""),
  );
  if (input) {
    let el: HTMLElement | null = input;
    while (el && el !== document.body && el !== document.documentElement) {
      const tree = el.querySelector("[role='tree'], file-tree");
      if (tree) return tree.parentElement || tree;
      el = el.parentElement;
    }
    return input.parentElement?.parentElement || input.parentElement;
  }

  const tree = root.querySelector("[role='tree']");
  return tree ? tree.parentElement || tree : null;
}

export function nativeTreesIn(host: Element): Element[] {
  return [...host.querySelectorAll("nav, [role='tree']")].filter((el) => !el.closest("#pr-buddy-tree"));
}

export function readTreeItems(root: ParentNode): TreeItem[] {
  const nodes = root.querySelectorAll(
    "[role='treeitem'], [data-tree-entry-type='file'], [data-tree-entry-type='directory']",
  );
  const items: TreeItem[] = [];
  for (const el of nodes) {
    const type = el.getAttribute("data-tree-entry-type");
    const expanded = el.getAttribute("aria-expanded");
    const kind: TreeItem["kind"] =
      type === "directory" || (type !== "file" && expanded != null) ? "directory" : "file";
    const name = (
      el.getAttribute("data-path") ||
      el.getAttribute("data-file-path") ||
      el.querySelector("[data-filterable-item-text]")?.textContent ||
      visibleTreeLabel(el)
    ).trim();
    if (!name) continue;
    const level = Number(el.getAttribute("aria-level") || "") || inferTreeLevel(el);
    items.push({ level: level || 1, name, kind });
  }
  return items;
}

function visibleTreeLabel(el: Element): string {
  const clone = el.cloneNode(true) as Element;
  clone.querySelectorAll("[role='treeitem'], [role='group'], ul, ol").forEach((child) => {
    if (child !== clone) child.remove();
  });
  return (clone.textContent || "").replace(/\s+/g, " ").trim();
}

function inferTreeLevel(el: Element): number {
  let level = 0;
  let cur: Element | null = el;
  while (cur) {
    if (cur.getAttribute("role") === "treeitem" || cur.getAttribute("data-tree-entry-type")) level += 1;
    if (cur.getAttribute("role") === "tree" || cur.localName === "file-tree") break;
    cur = cur.parentElement;
  }
  return level;
}

export function findCommitsPicker(root: ParentNode): Element | null {
  let best: Element | null = null;
  let bestLen = Infinity;
  for (const el of root.querySelectorAll("button, summary, [role='button']")) {
    const t = (el.textContent || "").replace(/\s+/g, " ").trim();
    if (!isCommitsPickerLabel(t)) continue;
    if (t.length >= bestLen) continue;
    best = el.closest("button, summary, details, [role='button']") || el;
    bestLen = t.length;
  }
  return best;
}

function nodeHasFileFilter(root: Element): boolean {
  return [...root.querySelectorAll("input")].some((el) =>
    isFileFilterField(el.getAttribute("placeholder") || "", el.getAttribute("aria-label") || ""),
  );
}

function nodeHasFileTree(root: Element): boolean {
  return Boolean(root.querySelector("#pr-buddy-tree, file-tree, [role='tree']"));
}

function nodeHasTreeToggle(root: Element): boolean {
  return Boolean(
    root.querySelector(
      "[data-testid='expand-file-tree-button'], [data-testid='collapse-file-tree-button']",
    ),
  );
}

function filesToolbarRowOf(el: Element): Element | null {
  let cur: Element | null = el;
  while (cur && cur !== document.body && cur !== document.documentElement) {
    if (
      isFilesToolbarRow({
        component: cur.getAttribute("data-component"),
        direction: cur.getAttribute("data-direction"),
        hasTreeToggle: nodeHasTreeToggle(cur),
      })
    ) {
      return cur;
    }
    cur = cur.parentElement;
  }
  return null;
}

function commitsCellInToolbar(picker: Element, toolbar: Element): Element {
  let host: Element = picker.closest("details") || picker;
  let parent = host.parentElement;
  while (parent && parent !== toolbar) {
    host = parent;
    parent = host.parentElement;
  }
  return host;
}

export function placeBesideCommitsPicker(panel: Element, root: ParentNode): boolean {
  const picker = findCommitsPicker(root);
  if (!picker) return false;

  const toolbar = filesToolbarRowOf(picker);
  const host = toolbar ? commitsCellInToolbar(picker, toolbar) : picker.closest("details") || picker;
  const parent = toolbar || host.parentElement;
  if (!parent) return false;

  const decision = commitsDockAction({
    isFilesToolbarRow: parent === toolbar,
    hasFileFilter: nodeHasFileFilter(parent),
    hasFileTree: nodeHasFileTree(parent),
  });
  if (decision === "abort") return false;

  let dock = parent.querySelector(":scope > #pr-buddy-dock");
  if (!(dock instanceof HTMLElement)) {
    const stray = root.querySelector("#pr-buddy-dock");
    dock = stray instanceof HTMLElement ? stray : document.createElement("div");
    dock.id = "pr-buddy-dock";
    host.insertAdjacentElement("afterend", dock);
  }
  if (dock.contains(host) || nodeHasFileFilter(dock) || nodeHasFileTree(dock)) return false;
  if (dock.parentElement !== parent) {
    host.insertAdjacentElement("afterend", dock);
  }
  if (panel.parentElement === dock) return true;
  dock.append(panel);
  return true;
}

export function placeBeforeMergeStatus(panel: Element, root: ParentNode): boolean {
  const merge = findMergeStatusControl(root);
  if (!merge) return false;

  let before: Element = merge;
  let parent = merge.parentElement;
  while (parent && parent !== document.body && parent !== document.documentElement) {
    const onToolbarRow = [...parent.children].some(
      (c) => c !== before && c !== panel && nodeHasLabel(c, isToolbarCompanionLabel),
    );
    if (onToolbarRow) {
      // Already on this row — do not insertBefore. GitHub recreates the merge
      // button often; moving the pill is what makes it flicker.
      if (!shouldRedockPanel(panel.parentElement, parent)) return true;
      parent.insertBefore(panel, before);
      return true;
    }
    before = parent;
    parent = parent.parentElement;
  }
  const fallback = merge.parentElement;
  if (!fallback) return false;
  if (shouldRedockPanel(panel.parentElement, fallback)) {
    fallback.insertBefore(panel, merge);
  }
  return true;
}
