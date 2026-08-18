import { flattenGuide, parseGuide, type OrderedFile } from "./guide";
import {
  diffAnchor,
  fileNoteCopy,
  fileSetSignature,
  noteShouldBeOpen,
  findFileTreeHost,
  isDiffRegionId,
  isFileFilterField,
  isOurMutationTarget,
  nativeTreesIn,
  orderedPaths,
  type FileChange,
  FILE_CHANGE_ICON,
  FILE_CHANGE_LABEL,
  collectFileChanges,
  diffsNeedReorder,
  fileRowLabel,
  firstCommonAncestor,
  hostUnder,
  pathForDiffFragment,
  pathFromVisibleLabel,
  pathsFromTreeItems,
  pickNoteInsert,
  placeBesideCommitsPicker,
  placeBeforeMergeStatus,
  readTreeItems,
  shortStatus,
  shouldReuseGuide,
  wantedDiffOrder,
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

let dialogOpen = false;
let lastActivePath = "";
let activePinnedUntil = 0;
let fileObserver: IntersectionObserver | null = null;
const closedNotes = new Set<string>();

function tick(): void {
  if (!parsePRURL(location.href)) {
    document.getElementById("pr-buddy-panel")?.remove();
    lastKey = "";
    lastRows = null;
    lastFiles = [];
    lastStatus = "";
    lastActivePath = "";
    closedNotes.clear();
    fileObserver?.disconnect();
    closeDialog();
    return;
  }
  mountPanel();
  if (!isFilesView(location.href, document)) {
    lastKey = "";
    lastRows = null;
    lastFiles = [];
    fileObserver?.disconnect();
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
  if (!force && lastRows && shouldReuseGuide(lastFiles, files)) {
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
      files,
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
  const paths = orderedPaths(rows);
  const changes = collectFileChanges(document, paths);
  const sig = paths.join("\n");
  if (!tree || !host || !host.contains(tree) || tree.dataset.order !== sig) renderTree(rows, changes);
  else {
    hideNativeTrees(host);
    paintFileChanges(tree, changes);
  }
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

function renderTree(rows: OrderedFile[], changes: Map<string, FileChange>): void {
  const host = findFileTreeHost(document);
  if (!host) return;
  hideNativeTrees(host);

  const existing = document.getElementById("pr-buddy-tree");
  const tree = existing ?? document.createElement("div");
  tree.id = "pr-buddy-tree";
  tree.dataset.order = orderedPaths(rows).join("\n");
  tree.replaceChildren();
  tree.append(renderFileList(rows, changes));
  syncActiveFromHash();
  if (lastActivePath) setActiveTreePath(lastActivePath);
  if (host.contains(tree)) return;
  const input = [...host.querySelectorAll("input")].find((el) =>
    isFileFilterField(el.getAttribute("placeholder") || "", el.getAttribute("aria-label") || ""),
  );
  const bar = input?.closest("form") || input?.parentElement;
  if (bar) bar.insertAdjacentElement("afterend", tree);
  else host.prepend(tree);
}

function fileChangeOf(path: string, changes: Map<string, FileChange>): FileChange {
  return changes.get(path) || "edited";
}

function changeIcon(change: FileChange): SVGSVGElement {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", "0 0 16 16");
  svg.setAttribute("aria-hidden", "true");
  svg.classList.add("pr-buddy-file-kind", `is-${change}`);
  const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
  path.setAttribute("d", FILE_CHANGE_ICON[change]);
  svg.append(path);
  return svg;
}

function paintFileRow(a: HTMLElement, path: string, change: FileChange): void {
  a.classList.remove("is-added", "is-deleted", "is-edited");
  a.classList.add(`is-${change}`);
  a.dataset.change = change;
  a.title = `${path} · ${FILE_CHANGE_LABEL[change]}`;
  const existing = a.querySelector(".pr-buddy-file-kind");
  const icon = changeIcon(change);
  if (existing) existing.replaceWith(icon);
  else a.querySelector(".pr-buddy-file-n")?.insertAdjacentElement("afterend", icon);
}

function paintFileChanges(tree: Element, changes: Map<string, FileChange>): void {
  for (const el of tree.querySelectorAll(".pr-buddy-file")) {
    if (!(el instanceof HTMLElement)) continue;
    const path = el.dataset.path || "";
    if (!path) continue;
    paintFileRow(el, path, fileChangeOf(path, changes));
  }
}

function renderFileList(rows: OrderedFile[], changes: Map<string, FileChange>): HTMLOListElement {
  const list = document.createElement("ol");
  list.className = "pr-buddy-file-list";
  rows.forEach((file, i) => {
    const li = document.createElement("li");
    const a = document.createElement("a");
    a.className = "pr-buddy-file";
    a.href = diffAnchor(file.path);
    a.dataset.path = file.path;
    a.addEventListener("click", (ev) => {
      ev.preventDefault();
      setActiveTreePath(file.path, true);
      goToDiff(file.path);
    });
    const n = document.createElement("span");
    n.className = "pr-buddy-file-n";
    n.textContent = String(i + 1);
    const label = fileRowLabel(file.path);
    const text = document.createElement("span");
    text.className = "pr-buddy-file-text";
    const name = document.createElement("span");
    name.className = "pr-buddy-file-path";
    name.textContent = label.name;
    text.append(name);
    if (label.dir) {
      const dir = document.createElement("span");
      dir.className = "pr-buddy-file-dir";
      dir.textContent = label.dir;
      text.append(dir);
    }
    a.append(n, text);
    paintFileRow(a, file.path, fileChangeOf(file.path, changes));
    li.append(a);
    list.append(li);
  });
  return list;
}

function setActiveTreePath(path: string, pin = false): void {
  lastActivePath = path;
  if (pin) activePinnedUntil = Date.now() + 1000;
  const tree = document.getElementById("pr-buddy-tree");
  if (!tree) return;
  for (const el of tree.querySelectorAll(".pr-buddy-file")) {
    const on = el.getAttribute("data-path") === path;
    el.classList.toggle("is-active", on);
    if (on) el.setAttribute("aria-current", "true");
    else el.removeAttribute("aria-current");
  }
}

function goToDiff(path: string): void {
  const frag = diffAnchor(path);
  const id = frag.slice(1);
  if (location.hash === frag) {
    document.getElementById(id)?.scrollIntoView({ block: "start" });
    return;
  }
  location.hash = frag;
}

function syncActiveFromHash(): void {
  if (!lastRows) return;
  const path = pathForDiffFragment(
    lastRows.map((r) => r.path),
    location.hash,
  );
  if (path) setActiveTreePath(path);
}

function watchVisibleFile(cards: Element[]): void {
  fileObserver?.disconnect();
  if (!cards.length || typeof IntersectionObserver === "undefined") return;
  fileObserver = new IntersectionObserver(
    (entries) => {
      if (Date.now() < activePinnedUntil) return;
      const visible = entries
        .filter((e) => e.isIntersecting)
        .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top);
      const top = visible[0]?.target;
      if (!(top instanceof Element)) return;
      const path = lastRows ? pathOfDiffCard(top, orderedPaths(lastRows)) : "";
      if (path) setActiveTreePath(path);
    },
    { rootMargin: "-15% 0px -65% 0px", threshold: 0 },
  );
  for (const el of cards) fileObserver.observe(el);
}

function diffRegionOf(el: Element): Element {
  const region = el.closest("[id^='diff-']");
  if (region && isDiffRegionId(region.id)) return region;
  return el;
}

function pathOfDiffCard(el: Element, paths: string[]): string {
  const region = diffRegionOf(el);
  if (isDiffRegionId(region.id)) {
    const fromId = pathForDiffFragment(paths, region.id);
    if (fromId) return fromId;
  }
  const marked = region.querySelector("[data-file-path], [data-tagsearch-path]");
  const direct =
    region.getAttribute("data-file-path") ||
    marked?.getAttribute("data-file-path") ||
    marked?.getAttribute("data-tagsearch-path") ||
    "";
  if (direct && paths.includes(direct)) return direct;
  const href = region.querySelector("a[href*='#diff-']")?.getAttribute("href") || "";
  const fromHref = pathForDiffFragment(paths, href.split("#")[1] || "");
  if (fromHref) return fromHref;
  return pathFromVisibleLabel(region.textContent || "", paths);
}

function findDiffCards(root: ParentNode, paths: string[]): Element[] {
  const seen = new Set<Element>();
  const add = (el: Element | null | undefined) => {
    if (!el || el.closest("#pr-buddy-tree, #pr-buddy-panel, #pr-buddy-dialog, .pr-buddy-file-note")) return;
    seen.add(diffRegionOf(el));
  };
  for (const el of root.querySelectorAll("[id^='diff-']")) {
    if (isDiffRegionId(el.id)) add(el);
  }
  for (const el of root.querySelectorAll(
    "copilot-diff-entry, .file.js-file, [data-diff-header-wrapper], [data-file-path], [data-tagsearch-path], table[data-diff-anchor]",
  )) {
    add(el);
  }
  for (const path of paths) add(document.getElementById(diffAnchor(path).slice(1)));
  return [...seen];
}

function insertNote(card: Element, note: HTMLElement): void {
  const where = pickNoteInsert({
    hasHeaderWrapper: Boolean(card.querySelector("[data-diff-header-wrapper]")),
    hasDiffTable: Boolean(card.querySelector("table[data-diff-anchor], table[aria-label^='Diff for']")),
  });
  if (where === "inside-table-wrap") {
    const table = card.querySelector("table[data-diff-anchor], table[aria-label^='Diff for']");
    if (table) {
      (table.parentElement || table).prepend(note);
      return;
    }
  }
  if (where === "after-header-wrapper") {
    card.querySelector("[data-diff-header-wrapper]")?.insertAdjacentElement("afterend", note);
    return;
  }
  card.prepend(note);
}

function noteTitle(file: OrderedFile): string {
  return file.group && file.group !== "Other" ? file.group : "Review";
}

function fileIsViewed(card: Element): boolean {
  for (const el of card.querySelectorAll("input[type='checkbox'], [role='checkbox']")) {
    const label = `${el.getAttribute("aria-label") || ""} ${el.textContent || ""}`;
    if (!/viewed/i.test(label)) continue;
    if (el instanceof HTMLInputElement) return el.checked;
    return el.getAttribute("aria-checked") === "true";
  }
  return false;
}

function fileIsCollapsed(card: Element): boolean {
  if (card instanceof HTMLDetailsElement) return !card.open;
  const inner = [...card.children].find((el) => el.localName === "details");
  return inner instanceof HTMLDetailsElement ? !inner.open : false;
}

function setNoteOpen(note: HTMLElement, path: string, open: boolean): void {
  note.classList.toggle("is-closed", !open);
  const chip = note.querySelector(".pr-buddy-file-note-chip");
  if (chip) chip.setAttribute("aria-expanded", String(open));
  if (open) closedNotes.delete(path);
  else closedNotes.add(path);
}

function attachFileNote(card: Element, file: OrderedFile): void {
  const text = fileNoteCopy(file) || "No summary for this file.";
  const region = diffRegionOf(card);
  const existing = region.querySelector(".pr-buddy-file-note");
  if (existing instanceof HTMLElement) {
    existing.dataset.path = file.path;
    const body = existing.querySelector(".pr-buddy-file-note-body");
    const label = existing.querySelector(".pr-buddy-file-note-title");
    if (body) body.textContent = text;
    if (label) label.textContent = noteTitle(file);
    const chipBtn = existing.querySelector(".pr-buddy-file-note-chip");
    if (chipBtn && chipBtn.textContent !== "👀") {
      chipBtn.textContent = "👀";
      chipBtn.setAttribute("aria-label", "Buddy review");
    }
    if (!existing.querySelector(".pr-buddy-file-note-brand")) {
      const brand = document.createElement("div");
      brand.className = "pr-buddy-file-note-brand";
      brand.textContent = "Buddy review";
      existing.querySelector(".pr-buddy-file-note-card")?.prepend(brand);
    }
    const open = noteShouldBeOpen({
      userClosed: closedNotes.has(file.path),
      viewed: fileIsViewed(region),
      collapsed: fileIsCollapsed(region),
    });
    if (existing.classList.contains("is-closed") === open) {
      setNoteOpen(existing, file.path, open);
    }
    if (existing.parentElement !== region || region.firstElementChild !== existing) {
      insertNote(region, existing);
    }
    return;
  }
  const note = document.createElement("div");
  note.className = "pr-buddy-file-note";
  note.dataset.path = file.path;
  const chip = document.createElement("button");
  chip.type = "button";
  chip.className = "pr-buddy-file-note-chip";
  chip.textContent = "👀";
  chip.setAttribute("aria-label", "Buddy review");
  chip.setAttribute("aria-expanded", "true");
  chip.addEventListener("click", (ev) => {
    ev.preventDefault();
    ev.stopPropagation();
    setNoteOpen(note, file.path, note.classList.contains("is-closed"));
  });
  const cardEl = document.createElement("div");
  cardEl.className = "pr-buddy-file-note-card";
  const brand = document.createElement("div");
  brand.className = "pr-buddy-file-note-brand";
  brand.textContent = "Buddy review";
  const bar = document.createElement("div");
  bar.className = "pr-buddy-file-note-bar";
  const title = document.createElement("div");
  title.className = "pr-buddy-file-note-title";
  title.textContent = noteTitle(file);
  const close = document.createElement("button");
  close.type = "button";
  close.className = "pr-buddy-file-note-close";
  close.setAttribute("aria-label", "Close summary");
  close.textContent = "×";
  close.addEventListener("click", (ev) => {
    ev.preventDefault();
    ev.stopPropagation();
    setNoteOpen(note, file.path, false);
  });
  bar.append(title, close);
  const body = document.createElement("div");
  body.className = "pr-buddy-file-note-body";
  body.textContent = text;
  cardEl.append(brand, bar, body);
  note.append(chip, cardEl);
  setNoteOpen(
    note,
    file.path,
    noteShouldBeOpen({
      userClosed: closedNotes.has(file.path),
      viewed: fileIsViewed(region),
      collapsed: fileIsCollapsed(region),
    }),
  );
  insertNote(region, note);
}

function renderDiffs(rows: OrderedFile[]): void {
  const paths = orderedPaths(rows);
  const root =
    document.querySelector(".js-diff-progressive-container") ||
    document.querySelector("#files") ||
    document;
  const byPath = new Map<string, Element>();
  for (const el of findDiffCards(root, paths)) {
    const path = pathOfDiffCard(el, paths);
    if (path && !byPath.has(path)) byPath.set(path, el);
  }
  reorderDiffCards(rows, byPath);
  for (const file of rows) {
    const el = byPath.get(file.path) || document.getElementById(diffAnchor(file.path).slice(1));
    if (el) attachFileNote(el, file);
  }
  watchVisibleFile([...byPath.values()]);
}

function reorderDiffCards(rows: OrderedFile[], byPath: Map<string, Element>): void {
  const cards = [...byPath.values()];
  if (cards.length < 2) return;
  const parent = firstCommonAncestor(cards);
  if (!parent || parent === document.body || parent === document.documentElement) return;
  if (parent.id === "pr-buddy-tree" || parent.closest("#pr-buddy-tree")) return;
  const hosts = new Map<string, Element>();
  for (const [path, el] of byPath) {
    const host = hostUnder(el, parent);
    if (host) hosts.set(path, host);
  }
  const current = [...parent.children]
    .map((child) => {
      for (const [path, host] of hosts) {
        if (host === child) return path;
      }
      return "";
    })
    .filter(Boolean);
  const wanted = wantedDiffOrder([...hosts.keys()], orderedPaths(rows));
  if (!diffsNeedReorder(current, wanted)) return;
  for (const path of wanted) {
    const host = hosts.get(path);
    if (host) parent.append(host);
  }
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
  field.className = "pr-buddy-models";
  const legend = document.createElement("legend");
  legend.textContent = "Model";
  field.append(legend);
  const seg = document.createElement("div");
  seg.className = "pr-buddy-seg";
  for (const id of backends) {
    const label = document.createElement("label");
    label.className = "pr-buddy-seg-item";
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
    const text = document.createElement("span");
    text.textContent = id;
    label.append(input, text);
    seg.append(label);
  }
  field.append(seg);
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
  refresh.className = "pr-buddy-refresh";
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
      if (
        el &&
        (isOurMutationTarget(el.id) ||
          el.closest("#pr-buddy-panel, #pr-buddy-tree, #pr-buddy-dialog, #pr-buddy-dock, .pr-buddy-file-note"))
      ) {
        continue;
      }
      schedule();
      return;
    }
  }).observe(document.documentElement, {
    childList: true,
    subtree: true,
  });
}
tick();
