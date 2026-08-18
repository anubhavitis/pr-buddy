import { flattenGuide, parseGuide, type OrderedFile } from "./guide";
import {
  fileSetSignature,
  findFileTreeHost,
  isFileFilterField,
  isOurMutationTarget,
  nativeTreesIn,
  orderedPaths,
  type PathTreeNode,
  pathsFromTreeItems,
  pathsToTree,
  placeBesideCommitsPicker,
  placeBeforeMergeStatus,
  readTreeItems,
  shortStatus,
} from "./overlay";
import { buildPrompt } from "./prompt";
import { defaultSettings, type Backend, type Settings } from "./settings";
import {
  collectFilePathsFromDOM,
  findHeadSHAFromDOM,
  isFilesView,
  parseBody,
  parsePRURL,
  parseTitle,
} from "./pr";

let applying = false;
let scheduled = 0;
let lastKey = "";
let lastRows: OrderedFile[] | null = null;
let lastFiles: string[] = [];
let lastStatus = "";
let diffsOrdered = false;
let dialogOpen = false;

function tick(): void {
  if (!parsePRURL(location.href)) {
    document.getElementById("pr-buddy-panel")?.remove();
    lastKey = "";
    lastRows = null;
    lastFiles = [];
    lastStatus = "";
    diffsOrdered = false;
    closeDialog();
    return;
  }
  mountPanel();
  if (!isFilesView(location.href, document)) {
    lastKey = "";
    lastRows = null;
    lastFiles = [];
    diffsOrdered = false;
    setStatus("open the Files changed tab");
    return;
  }
  if (lastRows) applyOverlay(lastRows);
  void loadAndApply(false);
}

function schedule(): void {
  window.clearTimeout(scheduled);
  scheduled = window.setTimeout(tick, 250);
}

function currentFiles(): string[] {
  const fromAttrs = collectFilePathsFromDOM(document);
  const fromTree = pathsFromTreeItems(readTreeItems(document));
  const seen = new Set(fromAttrs);
  const out = [...fromAttrs];
  for (const path of fromTree) {
    if (seen.has(path)) continue;
    seen.add(path);
    out.push(path);
  }
  return out;
}

async function loadAndApply(force: boolean): Promise<void> {
  if (applying) return;
  const id = parsePRURL(location.href);
  if (!id) return;
  const files = currentFiles();
  if (files.length === 0) {
    setStatus("waiting for file list…");
    return;
  }
  const headSHA = findHeadSHAFromDOM(document);
  const key = `${id.owner}/${id.repo}#${id.number}:${headSHA || "no-sha"}:${fileSetSignature(files)}`;
  const sameOrSubset =
    lastFiles.length > 0 &&
    (fileSetSignature(files) === fileSetSignature(lastFiles) ||
      files.every((p) => lastFiles.includes(p)));
  if (!force && lastRows && sameOrSubset) {
    applyOverlay(lastRows);
    return;
  }
  if (!force && lastRows) applyOverlay(lastRows);
  else setStatus(force ? "reordering…" : "ordering…");
  applying = true;
  try {
    const res = (await chrome.runtime.sendMessage({
      type: "complete",
      owner: id.owner,
      repo: id.repo,
      number: id.number,
      headSHA,
      prompt: buildPrompt({
        ...id,
        title: parseTitle(document.documentElement.innerHTML) || document.title,
        body: parseBody(document.documentElement.innerHTML),
        headSHA,
        files: files.map((path) => ({ path })),
      }),
      force,
    })) as { ok: boolean; text?: string; error?: string; cached?: boolean };
    if (!res?.ok || !res.text) {
      setStatus(res?.error || "host offline — start pr-buddy-host");
      return;
    }
    const rows = flattenGuide(parseGuide(res.text), files);
    lastRows = rows;
    lastKey = key;
    lastFiles = files;
    diffsOrdered = false;
    applyOverlay(rows);
    setStatus(res.cached ? `${rows.length} files · cached` : `${rows.length} files`);
  } catch (err) {
    setStatus(err instanceof Error ? err.message : String(err));
  } finally {
    applying = false;
  }
}

function applyOverlay(rows: OrderedFile[]): void {
  const host = findFileTreeHost(document);
  const tree = document.getElementById("pr-buddy-tree");
  const sig = orderedPaths(rows).join("\n");
  if (!tree || !host || !host.contains(tree) || tree.dataset.order !== sig) renderTree(rows);
  else hideNativeTrees(host);
  renderDiffs(rows);
}

function hideNativeTrees(host: Element): void {
  document.documentElement.classList.add("pr-buddy-has-guide");
  host.setAttribute("data-pr-buddy-host", "");
  for (const native of nativeTreesIn(host)) {
    if (native instanceof HTMLElement) native.hidden = true;
  }
  const tooWide = host.querySelector(
    ".js-diff-progressive-container, #files, copilot-diff-entry, .file.js-file",
  );
  if (tooWide) return;
  for (const child of [...host.children]) {
    if (child.id === "pr-buddy-tree") continue;
    if (child.querySelector("input")) continue;
    const rows = child.querySelectorAll("a, button, [role='treeitem'], li").length;
    if (rows >= 4 && child instanceof HTMLElement) child.hidden = true;
  }
}

function renderTree(rows: OrderedFile[]): void {
  const host = findFileTreeHost(document);
  if (!host) return;
  hideNativeTrees(host);

  const existing = document.getElementById("pr-buddy-tree");
  const tree = existing ?? document.createElement("div");
  tree.id = "pr-buddy-tree";
  tree.dataset.order = orderedPaths(rows).join("\n");
  tree.replaceChildren();
  tree.append(renderPathTree(pathsToTree(orderedPaths(rows))));
  if (host.contains(tree)) return;
  const input = [...host.querySelectorAll("input")].find((el) =>
    isFileFilterField(el.getAttribute("placeholder") || "", el.getAttribute("aria-label") || ""),
  );
  const bar = input?.closest("form") || input?.parentElement;
  if (bar) bar.insertAdjacentElement("afterend", tree);
  else host.prepend(tree);
}

function renderPathTree(nodes: PathTreeNode[]): HTMLOListElement {
  const list = document.createElement("ol");
  list.className = "pr-buddy-file-list";
  for (const node of nodes) {
    const li = document.createElement("li");
    if (node.kind === "dir") {
      const dir = document.createElement("div");
      dir.className = "pr-buddy-dir";
      dir.textContent = node.name;
      li.append(dir, renderPathTree(node.children));
    } else {
      const a = document.createElement("a");
      a.className = "pr-buddy-file";
      a.href = anchorFor(node.path) || "#";
      a.title = node.path;
      a.addEventListener("click", (ev) => {
        if (activateNativeFile(node.path)) ev.preventDefault();
      });
      const n = document.createElement("span");
      n.className = "pr-buddy-file-n";
      n.textContent = String(node.index);
      const name = document.createElement("span");
      name.className = "pr-buddy-file-path";
      name.textContent = node.name;
      a.append(n, name);
      li.append(a);
    }
    list.append(li);
  }
  return list;
}

function activateNativeFile(path: string): boolean {
  const name = path.split("/").pop() || path;
  const exact = document.querySelector(
    `[data-file-path="${cssAttr(path)}"], [data-path="${cssAttr(path)}"], [data-tagsearch-path="${cssAttr(path)}"]`,
  );
  const clickable = exact instanceof HTMLElement ? exact : exact?.closest("a, button, [role='treeitem']");
  if (clickable instanceof HTMLElement) {
    clickable.click();
    return true;
  }
  for (const item of document.querySelectorAll("[role='treeitem'], [data-tree-entry-type='file']")) {
    const text = (item.textContent || "").replace(/\s+/g, " ").trim();
    if (text !== path && text !== name && !text.endsWith(path) && !text.endsWith(name)) continue;
    const target = item instanceof HTMLElement ? item : null;
    const inner = item.querySelector("a, button");
    (inner instanceof HTMLElement ? inner : target)?.click();
    return true;
  }
  return false;
}

function pathOfDiffCard(el: Element): string {
  return (
    el.getAttribute("data-file-path") ||
    el.getAttribute("data-tagsearch-path") ||
    el.querySelector(".file-header[data-path], [data-path]")?.getAttribute("data-path") ||
    ""
  );
}

function findDiffCards(root: ParentNode): Element[] {
  const found = [...root.querySelectorAll("copilot-diff-entry, .file.js-file")];
  if (found.length) return found;
  return [...root.querySelectorAll(".file-header[data-path]")].map(
    (h) => h.closest(".file, [class*='Diff'], [class*='diff']") || h.parentElement || h,
  );
}

function attachFileNote(card: Element, file: OrderedFile): void {
  if (!file.blurb) return;
  const existing = card.querySelector(".pr-buddy-file-note");
  if (existing) {
    const body = existing.querySelector(".pr-buddy-file-note-body");
    if (body) body.textContent = file.blurb;
    return;
  }
  const note = document.createElement("details");
  note.className = "pr-buddy-file-note";
  note.dataset.path = file.path;
  const summary = document.createElement("summary");
  summary.textContent = "Summary";
  const body = document.createElement("div");
  body.className = "pr-buddy-file-note-body";
  body.textContent = file.blurb;
  note.append(summary, body);
  const header = card.querySelector(".file-header");
  if (header) header.insertAdjacentElement("afterend", note);
  else card.prepend(note);
}

function renderDiffs(rows: OrderedFile[]): void {
  const root =
    document.querySelector(".js-diff-progressive-container") ||
    document.querySelector("#files") ||
    document;
  const byPath = new Map<string, Element>();
  for (const el of findDiffCards(root)) {
    const path = pathOfDiffCard(el);
    if (path && !byPath.has(path)) byPath.set(path, el);
  }
  const parent =
    document.querySelector(".js-diff-progressive-container") || document.querySelector("#files");
  if (parent && !diffsOrdered) {
    for (const file of rows) {
      const el = byPath.get(file.path);
      if (el) parent.append(el);
    }
    diffsOrdered = true;
  }
  for (const file of rows) {
    const el = byPath.get(file.path);
    if (el) attachFileNote(el, file);
  }
}

function anchorFor(path: string): string {
  const header = document.querySelector(`.file-header[data-path="${cssAttr(path)}"]`);
  const id = header?.closest("[id^='diff-']")?.id;
  return id ? `#${id}` : "";
}

function cssAttr(value: string): string {
  return value.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
}

function mountPanel(): void {
  let panel = document.getElementById("pr-buddy-panel");
  if (!panel) {
    lastStatus = "";
    panel = document.createElement("div");
    panel.id = "pr-buddy-panel";
    panel.innerHTML = `
    <img class="pr-buddy-logo" src="${chrome.runtime.getURL("icons/icon32.png")}" width="16" height="16" alt="" />
    <span class="pr-buddy-name">pr-buddy</span>
    <span class="pr-buddy-sep" aria-hidden="true"></span>
    <span id="pr-buddy-status">starting…</span>
  `;
    panel.title = "Model and refresh";
    document.body.append(panel);
    wirePanel(panel);
  }
  const docked =
    placeBesideCommitsPicker(panel, document) || placeBeforeMergeStatus(panel, document);
  panel.classList.toggle("pr-buddy-panel--toolbar", docked);
}

function wirePanel(panel: HTMLElement): void {
  panel.addEventListener("click", (ev) => {
    if ((ev.target as Element | null)?.closest("#pr-buddy-dialog")) return;
    ev.preventDefault();
    ev.stopPropagation();
    void toggleDialog();
  });
}

function closeDialog(): void {
  dialogOpen = false;
  document.getElementById("pr-buddy-dialog")?.remove();
}

async function toggleDialog(): Promise<void> {
  if (dialogOpen) {
    closeDialog();
    return;
  }
  const panel = document.getElementById("pr-buddy-panel");
  if (!panel) return;
  const settings = (await chrome.runtime.sendMessage({ type: "getSettings" })) as Settings;
  const s = settings ?? defaultSettings;
  const dialog = document.createElement("div");
  dialog.id = "pr-buddy-dialog";
  dialog.setAttribute("role", "dialog");
  dialog.setAttribute("aria-label", "pr-buddy");
  const backends: Backend[] = ["claude", "grok", "mlx"];
  const field = document.createElement("fieldset");
  const legend = document.createElement("legend");
  legend.textContent = "Model";
  field.append(legend);
  for (const id of backends) {
    const label = document.createElement("label");
    const input = document.createElement("input");
    input.type = "radio";
    input.name = "pr-buddy-backend";
    input.value = id;
    input.checked = s.backend === id;
    input.addEventListener("change", () => {
      void chrome.runtime.sendMessage({ type: "setSettings", settings: { backend: id } }).then(() => {
        showMlx(dialog, id === "mlx");
      });
    });
    label.append(input, ` ${id}`);
    field.append(label);
  }
  const mlx = document.createElement("div");
  mlx.id = "pr-buddy-mlx";
  mlx.hidden = s.backend !== "mlx";
  const url = document.createElement("input");
  url.type = "text";
  url.placeholder = "http://127.0.0.1:8080/v1";
  url.value = s.mlxUrl;
  url.addEventListener("change", () => {
    void chrome.runtime.sendMessage({ type: "setSettings", settings: { mlxUrl: url.value } });
  });
  const model = document.createElement("input");
  model.type = "text";
  model.placeholder = "model id";
  model.value = s.mlxModel;
  model.addEventListener("change", () => {
    void chrome.runtime.sendMessage({ type: "setSettings", settings: { mlxModel: model.value } });
  });
  mlx.append(url, model);
  const refresh = document.createElement("button");
  refresh.type = "button";
  refresh.textContent = "Refresh order";
  refresh.addEventListener("click", (ev) => {
    ev.stopPropagation();
    closeDialog();
    void loadAndApply(true);
  });
  dialog.append(field, mlx, refresh);
  dialog.addEventListener("click", (ev) => ev.stopPropagation());
  panel.append(dialog);
  dialogOpen = true;
}

function showMlx(dialog: HTMLElement, on: boolean): void {
  const mlx = dialog.querySelector("#pr-buddy-mlx");
  if (mlx instanceof HTMLElement) mlx.hidden = !on;
}

function setStatus(text: string): void {
  if (text === lastStatus) return;
  const el = document.getElementById("pr-buddy-status");
  const panel = document.getElementById("pr-buddy-panel");
  if (!el || !panel) return;
  const short = shortStatus(text);
  lastStatus = text;
  el.textContent = short;
  panel.title = text;
  panel.classList.toggle("is-err", /fail|offline|error|invalid|missing/i.test(short));
  panel.classList.toggle("is-busy", /ordering|starting|waiting|reordering/i.test(short));
  panel.classList.toggle("is-ok", /\bfiles\b/i.test(short));
}

const boot = globalThis as typeof globalThis & { __prBuddyInstalled?: boolean };
if (!boot.__prBuddyInstalled) {
  boot.__prBuddyInstalled = true;
  document.addEventListener("turbo:load", schedule);
  document.addEventListener("turbo:render", schedule);
  document.addEventListener("turbo:visit", schedule);
  document.addEventListener("pjax:end", schedule);
  window.addEventListener("popstate", schedule);
  window.addEventListener("hashchange", schedule);
  document.addEventListener("click", (ev) => {
    if (!dialogOpen) return;
    const t = ev.target as Element | null;
    if (t?.closest("#pr-buddy-panel, #pr-buddy-dialog")) return;
    closeDialog();
  });
  document.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape") closeDialog();
  });
  new MutationObserver((muts) => {
    if (applying) return;
    for (const m of muts) {
      const el = m.target instanceof Element ? m.target : m.target.parentElement;
      if (el && (isOurMutationTarget(el.id) || el.closest("#pr-buddy-panel, #pr-buddy-tree, #pr-buddy-dialog, #pr-buddy-dock"))) continue;
      schedule();
      return;
    }
  }).observe(document.documentElement, {
    childList: true,
    subtree: true,
  });
}
tick();
