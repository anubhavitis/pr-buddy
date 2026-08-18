import type { OrderedFile } from "./guide";

export type OverlayGroup = {
  name: string;
  summary: string;
  files: OrderedFile[];
};

export function orderedPaths(rows: OrderedFile[]): string[] {
  return rows.map((row) => row.path);
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

export function placeBesideCommitsPicker(panel: Element, root: ParentNode): boolean {
  const picker = findCommitsPicker(root);
  if (!picker) return false;

  let host: Element = picker;
  let parent = picker.parentElement;
  while (parent && parent !== document.body && parent !== document.documentElement) {
    const siblings = [...parent.children].filter((c) => c !== panel);
    if (siblings.length >= 2 && siblings.length <= 8) {
      if (!shouldRedockPanel(panel.parentElement, parent)) return true;
      const after = host.nextSibling;
      if (after) parent.insertBefore(panel, after);
      else parent.append(panel);
      return true;
    }
    host = parent;
    parent = parent.parentElement;
  }
  const fallback = picker.parentElement;
  if (!fallback) return false;
  if (shouldRedockPanel(panel.parentElement, fallback)) {
    if (picker.nextSibling) fallback.insertBefore(panel, picker.nextSibling);
    else fallback.append(panel);
  }
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
