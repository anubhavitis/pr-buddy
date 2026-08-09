import * as fs from "fs";
import * as path from "path";
import * as vscode from "vscode";
import {
  CheckRun,
  checks as fetchChecks,
  Finding,
  Prepared,
  PrBuddyError,
  prepare,
  remove as removeWorktree,
  progress,
  review as runReview,
  setupDeps,
} from "./prBuddy";
import { ChecksTreeProvider } from "./checksTree";
import { BASE_SCHEME, BaseContentProvider, baseUri } from "./baseContent";
import { PullRequestNode, PullRequestTreeProvider } from "./pullRequestTree";
import {
  orderedFiles,
  readStoredReview,
  ReviewTreeProvider,
} from "./reviewTree";

/**
 * Opening a pull request checks it out and shows the diff immediately, then
 * reviews in the background. Waiting minutes before any code is readable would
 * defeat the point.
 */

let prTree: PullRequestTreeProvider;
let reviewTree: ReviewTreeProvider;
let checksTree: ChecksTreeProvider;
let terminal: vscode.Terminal | undefined;
let diagnostics: vscode.DiagnosticCollection;
/** Watches the current review artifact so an out-of-band run is picked up. */
let artifactWatcher: vscode.FileSystemWatcher | undefined;
/**
 * Re-reads CI while anything is still running. Held as a single handle and
 * cleared before each replacement, so switching pull requests cannot leave a
 * timer polling the one the reviewer left behind.
 */
let checksPoll: NodeJS.Timeout | undefined;

export function activate(context: vscode.ExtensionContext): void {
  prTree = new PullRequestTreeProvider(context.workspaceState);
  reviewTree = new ReviewTreeProvider();
  checksTree = new ChecksTreeProvider();
  diagnostics = vscode.languages.createDiagnosticCollection("pr-buddy");

  const prView = vscode.window.createTreeView("prBuddy.pullRequests", {
    treeDataProvider: prTree,
  });
  // The view title is the only place the current selection is visible, since
  // the org and repo are no longer rows.
  const showSelection = () => {
    const repo = prTree.repo();
    prView.description = repo ?? prTree.org() ?? "no repository selected";
  };
  showSelection();
  prTree.onDidChangeTreeData(showSelection);

  context.subscriptions.push(
    prView,
    vscode.window.createTreeView("prBuddy.review", {
      treeDataProvider: reviewTree,
    }),
    vscode.window.createTreeView("prBuddy.checks", {
      treeDataProvider: checksTree,
    }),
    vscode.workspace.registerTextDocumentContentProvider(
      BASE_SCHEME,
      new BaseContentProvider(),
    ),
    diagnostics,
    vscode.commands.registerCommand("prBuddy.refresh", () => prTree.refresh()),
    vscode.commands.registerCommand("prBuddy.selectOrg", () =>
      prTree.selectOrg(),
    ),
    vscode.commands.registerCommand("prBuddy.selectRepo", () =>
      prTree.selectRepo(),
    ),
    vscode.commands.registerCommand("prBuddy.reloadReview", () =>
      reviewTree.reload(),
    ),
    vscode.commands.registerCommand("prBuddy.openPullRequest", openPullRequest),
    vscode.commands.registerCommand("prBuddy.runReview", () => review(false)),
    vscode.commands.registerCommand("prBuddy.rerunReview", () => review(true)),
    vscode.commands.registerCommand("prBuddy.endReview", endReview),
    vscode.commands.registerCommand("prBuddy.markReviewed", (node) =>
      toggleReviewed(node, false),
    ),
    vscode.commands.registerCommand("prBuddy.unmarkReviewed", (node) =>
      toggleReviewed(node, true),
    ),
    vscode.commands.registerCommand("prBuddy.openTerminal", openTerminal),
    vscode.commands.registerCommand("prBuddy.resumeChat", resumeChat),
    vscode.commands.registerCommand("prBuddy.openFileDiff", openFileDiff),
    vscode.commands.registerCommand("prBuddy.refreshChecks", () =>
      loadChecks(true),
    ),
    vscode.commands.registerCommand("prBuddy.openCheck", openCheck),
    vscode.commands.registerCommand("prBuddy.rerunCheck", rerunCheck),
    vscode.commands.registerCommand("prBuddy.openOnGitHub", openOnGitHub),
    vscode.commands.registerCommand(
      "prBuddy.openReviewMarkdown",
      openReviewMarkdown,
    ),
  );
}

export function deactivate(): void {
  terminal?.dispose();
  artifactWatcher?.dispose();
  stopChecksPoll();
}

/**
 * Watches one pull request's review.json and adopts a completed review written
 * by anything other than this extension — a `pr-buddy` run in the terminal, or
 * a run whose notification was cancelled after the work finished. The provider
 * verifies provenance before adopting, so a watch cannot surface findings for a
 * different head.
 */
function watchArtifact(prepared: Prepared): void {
  artifactWatcher?.dispose();
  artifactWatcher = vscode.workspace.createFileSystemWatcher(
    new vscode.RelativePattern(
      path.dirname(prepared.review_json),
      path.basename(prepared.review_json),
    ),
  );
  const adopt = () => {
    reviewTree.reload();
    const current = reviewTree.current();
    if (current?.review) {
      publishDiagnostics(current.prepared, current.review.findings);
    }
  };
  artifactWatcher.onDidCreate(adopt);
  artifactWatcher.onDidChange(adopt);
}

async function openPullRequest(node: PullRequestNode): Promise<void> {
  if (!node || node.kind !== "pr") {
    return;
  }
  const { repo, pr } = node;

  const prepared = await vscode.window.withProgress(
    {
      location: vscode.ProgressLocation.Notification,
      title: `Preparing ${repo}#${pr.number}`,
      cancellable: true,
    },
    (progress, token) => {
      progress.report({ message: "checking out the pull request head…" });
      return prepare(repo, pr.number, token).catch((err) => {
        showError(err);
        return undefined;
      });
    },
  );
  if (!prepared) {
    return;
  }

  // Only a review whose provenance still matches this head may be shown.
  // Anything else — stale, failed, mid-flight — describes different code, and
  // pinning its findings to the current checkout puts them on lines that have
  // since moved.
  const isCurrent = prepared.review_status === "complete";
  const stored = isCurrent ? readStoredReview(prepared.review_json) : undefined;
  reviewTree.set({
    prepared,
    review: stored,
    running: false,
    staleReason:
      prepared.review_status === "stale"
        ? (prepared.stale_reason ?? "the pull request has moved")
        : undefined,
  });
  publishDiagnostics(prepared, stored?.findings);
  watchArtifact(prepared);

  await openTerminal();
  await openFirstFile(prepared);

  void loadProgress(prepared);
  void loadChecks();
  // Deliberately not awaited: the diff is already readable, and the dependency
  // copy takes far longer than the checkout it follows.
  void setupDependencies(prepared);

  const auto = vscode.workspace
    .getConfiguration("prBuddy")
    .get<boolean>("autoReviewOnOpen", true);
  if (isCurrent) {
    vscode.window.setStatusBarMessage("pr-buddy: using cached review", 4000);
  } else if (auto) {
    void review(false);
  } else if (prepared.review_status === "stale") {
    vscode.window.setStatusBarMessage(
      `pr-buddy: cached review is out of date (${prepared.stale_reason ?? "head moved"}) — re-review to refresh`,
      6000,
    );
  }
}

/** Runs a review in the background, leaving the editor usable throughout. */
async function review(force: boolean): Promise<void> {
  const state = reviewTree.current();
  if (!state) {
    vscode.window.showInformationMessage("Open a pull request first.");
    return;
  }
  const { repo, pr_number: prNumber } = state.prepared;

  state.running = true;
  reviewTree.set(state);

  await vscode.window.withProgress(
    {
      location: vscode.ProgressLocation.Window,
      title: `pr-buddy: reviewing #${prNumber}`,
      cancellable: true,
    },
    async (_progress, token) => {
      try {
        const result = await runReview(repo, prNumber, force, token);
        const current = reviewTree.current();
        // The user may have moved on to another pull request while this ran.
        if (!current || current.prepared.pr_number !== prNumber) {
          return;
        }
        current.running = false;
        current.sessionId = result.session_id;
        current.resumeCommand = result.resume_command;
        current.review = readStoredReview(result.review_json);
        // A completed run supersedes whatever was stale before it.
        current.staleReason = undefined;
        reviewTree.set(current);
        publishDiagnostics(current.prepared, result.findings);

        const { error = 0, warning = 0 } = result.counts;
        vscode.window.setStatusBarMessage(
          `pr-buddy: ${error} error, ${warning} warning${result.from_cache ? " (cached)" : ""}`,
          6000,
        );
      } catch (err) {
        const current = reviewTree.current();
        if (current) {
          current.running = false;
          reviewTree.set(current);
        }
        showError(err);
      }
    },
  );
}

/**
 * Marks a file read, or clears the mark.
 *
 * The binary owns the record because a mark is tied to the file's content, not
 * its path: it must survive a push that touches other files and clear itself
 * when the author changes this one. Deriving that here would duplicate a rule
 * that belongs in one place.
 */
async function toggleReviewed(node: unknown, reviewed: boolean): Promise<void> {
  const state = reviewTree.current();
  const relPath = filePathOf(node);
  if (!state || !relPath) {
    return;
  }
  try {
    const result = await progress(
      state.prepared.repo,
      state.prepared.pr_number,
      reviewed ? { unmark: relPath } : { mark: relPath },
    );
    const current = reviewTree.current();
    if (!current || current.prepared.pr_number !== state.prepared.pr_number) {
      return;
    }
    current.reviewed = new Set(result.reviewed);
    reviewTree.set(current);
  } catch (err) {
    showError(err);
  }
}

/** Reads the path out of a review-tree file node. */
function filePathOf(node: unknown): string | undefined {
  if (node && typeof node === "object" && "relPath" in node) {
    const { relPath } = node as { relPath?: unknown };
    return typeof relPath === "string" ? relPath : undefined;
  }
  return undefined;
}

/** How often CI is re-read while any check is still running. */
const CHECKS_POLL_MS = 20_000;

/**
 * Reads CI for the open pull request, and keeps reading while anything is still
 * running.
 *
 * The timer is rescheduled after each response rather than run on a fixed
 * interval, so a slow or failing request cannot stack up overlapping fetches,
 * and it stops as soon as every check has settled.
 */
async function loadChecks(manual = false): Promise<void> {
  stopChecksPoll();
  const state = reviewTree.current();
  if (!state) {
    checksTree.set(undefined);
    return;
  }
  const { repo, pr_number: prNumber } = state.prepared;
  const existing = checksTree.current();
  checksTree.set({
    repo,
    prNumber,
    checks: existing?.prNumber === prNumber ? existing.checks : [],
    loading: true,
  });

  try {
    const result = await fetchChecks(repo, prNumber);
    // The reviewer may have moved on while this ran.
    if (reviewTree.current()?.prepared.pr_number !== prNumber) {
      return;
    }
    checksTree.set({ repo, prNumber, checks: result.checks });
    if (checksTree.anyRunning()) {
      scheduleChecksPoll();
    }
  } catch (err) {
    if (reviewTree.current()?.prepared.pr_number !== prNumber) {
      return;
    }
    const message = err instanceof Error ? err.message : String(err);
    checksTree.set({ repo, prNumber, checks: [], error: message });
    // Only a refresh the reviewer asked for is worth a dialog; a background
    // poll failing is already visible as a row in the panel.
    if (manual) {
      showError(err);
    }
  }
}

function scheduleChecksPoll(): void {
  stopChecksPoll();
  checksPoll = setTimeout(() => void loadChecks(), CHECKS_POLL_MS);
}

function stopChecksPoll(): void {
  if (checksPoll) {
    clearTimeout(checksPoll);
    checksPoll = undefined;
  }
}

async function openCheck(check: CheckRun): Promise<void> {
  if (check?.url) {
    await vscode.env.openExternal(vscode.Uri.parse(check.url));
  }
}

/**
 * Re-runs a workflow run's failed jobs.
 *
 * The only action in pr-buddy that changes anything on GitHub, so it is
 * confirmed rather than fired from a click: it spends the repository's CI
 * minutes and sets off whatever the workflow does on completion.
 */
async function rerunCheck(node: unknown): Promise<void> {
  const state = reviewTree.current();
  const check = checkOf(node);
  if (!state || !check?.workflow_run_id) {
    return;
  }
  const { repo, pr_number: prNumber } = state.prepared;

  const confirmed = await vscode.window.showWarningMessage(
    `Re-run failed jobs for ${repo}#${prNumber}?`,
    {
      modal: true,
      detail:
        "This runs on GitHub, spends the repository's CI minutes, and triggers whatever the workflow does on completion. It is the only action pr-buddy takes that writes to GitHub.",
    },
    "Re-run Failed Jobs",
  );
  if (confirmed !== "Re-run Failed Jobs") {
    return;
  }

  try {
    const result = await fetchChecks(repo, prNumber, check.workflow_run_id);
    if (reviewTree.current()?.prepared.pr_number !== prNumber) {
      return;
    }
    checksTree.set({ repo, prNumber, checks: result.checks });
    vscode.window.setStatusBarMessage("pr-buddy: re-run requested", 4000);
    // GitHub takes a moment to report the restarted jobs as running, so the
    // panel would otherwise sit on the old conclusions until the next manual
    // refresh.
    scheduleChecksPoll();
  } catch (err) {
    showError(err);
  }
}

/** Reads the check out of a checks-tree node. */
function checkOf(node: unknown): CheckRun | undefined {
  if (node && typeof node === "object" && "check" in node) {
    return (node as { check?: CheckRun }).check;
  }
  return undefined;
}

/** Loads which files still carry a valid reviewed mark. */
async function loadProgress(prepared: Prepared): Promise<void> {
  try {
    const result = await progress(prepared.repo, prepared.pr_number);
    const current = reviewTree.current();
    if (!current || current.prepared.pr_number !== prepared.pr_number) {
      return;
    }
    current.reviewed = new Set(result.reviewed);
    reviewTree.set(current);
  } catch {
    // Progress is an aid, not the review. A pull request with none yet, or a
    // worktree that has gone missing, is not worth interrupting the reviewer
    // over.
  }
}

/**
 * Copies the reviewer's installed dependencies into the worktree, so imports
 * resolve and go-to-definition works.
 *
 * Runs in the background: a checkout takes seconds and this can take a minute,
 * and the reviewer should be reading the diff throughout. Failure is reported in
 * the panel rather than as an error dialog — the worktree is still readable
 * without dependencies, just harder to navigate.
 */
async function setupDependencies(prepared: Prepared): Promise<void> {
  const config = vscode.workspace.getConfiguration("prBuddy");
  if (!config.get<boolean>("setupDependencies", true)) {
    return;
  }
  const sources = config.get<Record<string, string>>("sourceCheckouts", {});
  const source = sources[prepared.repo];
  if (!source) {
    return;
  }

  const prNumber = prepared.pr_number;
  await vscode.window.withProgress(
    {
      location: vscode.ProgressLocation.Window,
      title: `pr-buddy: setting up dependencies for #${prNumber}`,
      cancellable: true,
    },
    async (_progress, token) => {
      try {
        const result = await setupDeps(prepared.repo, prNumber, source, token);
        const current = reviewTree.current();
        // The reviewer may have moved on while this ran.
        if (!current || current.prepared.pr_number !== prNumber) {
          return;
        }
        current.depsReady = result.cloned || result.already_present;
        current.depsLockfileDiffers = result.lockfile_differs;
        reviewTree.set(current);
        if (result.cloned) {
          vscode.window.setStatusBarMessage(
            "pr-buddy: dependencies ready — imports now resolve",
            5000,
          );
        }
      } catch (err) {
        const current = reviewTree.current();
        if (current && current.prepared.pr_number === prNumber) {
          current.depsError = err instanceof Error ? err.message : String(err);
          reviewTree.set(current);
        }
      }
    },
  );
}

/**
 * Ends a review: deletes the worktree and returns the panel to its empty state.
 *
 * The binary refuses to delete a worktree holding the reviewer's own edits. That
 * refusal must leave everything on screen intact, or the reviewer loses sight of
 * the work the refusal exists to protect.
 */
async function endReview(): Promise<void> {
  const state = reviewTree.current();
  if (!state) {
    vscode.window.showInformationMessage("Open a pull request first.");
    return;
  }
  const { repo, pr_number: prNumber, worktree } = state.prepared;

  const confirmed = await vscode.window.showWarningMessage(
    `End the review of ${repo}#${prNumber}?`,
    {
      modal: true,
      detail: `This deletes the worktree at ${worktree} and its cached review.`,
    },
    "Delete Worktree",
  );
  if (confirmed !== "Delete Worktree") {
    return;
  }

  try {
    await removeWorktree(repo, prNumber);
  } catch (err) {
    showError(err);
    return;
  }

  diagnostics.clear();
  terminal?.dispose();
  terminal = undefined;
  artifactWatcher?.dispose();
  artifactWatcher = undefined;
  stopChecksPoll();
  checksTree.set(undefined);
  reviewTree.set(undefined);
  await closeWorktreeTabs(worktree);
}

/**
 * Closes editors showing the deleted checkout. A tab left open on a removed file
 * would keep offering to save it back into a worktree git no longer knows about.
 */
async function closeWorktreeTabs(worktree: string): Promise<void> {
  const prefix = worktree.endsWith(path.sep) ? worktree : worktree + path.sep;
  const doomed = vscode.window.tabGroups.all
    .flatMap((group) => group.tabs)
    .filter((tab) =>
      uris(tab).some(
        (uri) =>
          uri.scheme === BASE_SCHEME ||
          (uri.scheme === "file" && uri.fsPath.startsWith(prefix)),
      ),
    );
  if (doomed.length) {
    await vscode.window.tabGroups.close(doomed, true);
  }
}

/** Both sides of a diff tab count, so a diff closes with either one matching. */
function uris(tab: vscode.Tab): vscode.Uri[] {
  const input = tab.input;
  if (input instanceof vscode.TabInputText) {
    return [input.uri];
  }
  if (input instanceof vscode.TabInputTextDiff) {
    return [input.original, input.modified];
  }
  return [];
}

/**
 * Opens a terminal already in the worktree, so any harness the reviewer wants
 * runs against the right checkout without a manual cd.
 */
async function openTerminal(): Promise<void> {
  const state = reviewTree.current();
  if (!state) {
    return;
  }
  const name = `PR #${state.prepared.pr_number}`;
  if (terminal && terminal.name === name && terminal.exitStatus === undefined) {
    terminal.show(true);
    return;
  }
  terminal?.dispose();
  terminal = vscode.window.createTerminal({
    name,
    cwd: state.prepared.worktree,
  });
  terminal.show(true);
}

async function resumeChat(): Promise<void> {
  const state = reviewTree.current();
  if (!state) {
    vscode.window.showInformationMessage("Open a pull request first.");
    return;
  }
  // The recipe comes from the stored session, so a cached review opened without
  // running one in this window is still resumable. Composing the command here
  // would duplicate a decision the binary already owns.
  const command = state.resumeCommand ?? readSessionCommand(state.prepared);
  if (!command) {
    vscode.window.showInformationMessage(
      "No review session yet. Run a review first.",
    );
    return;
  }
  await openTerminal();
  // Not executed: the reviewer sees the command and presses Enter themselves.
  terminal?.sendText(command, false);
}

/** Reads the resume recipe the binary stored next to the review. */
function readSessionCommand(prepared: Prepared): string | undefined {
  try {
    const raw = fs.readFileSync(
      path.join(prepared.artifact_dir, "session.json"),
      "utf8",
    );
    return (JSON.parse(raw) as { resume_command?: string }).resume_command;
  } catch {
    return undefined;
  }
}

/** Shows the change as a side-by-side diff of base against head. */
async function openFileDiff(relPath: string, prNumber?: number): Promise<void> {
  const state = reviewTree.current();
  // Guarded here rather than by a manifest `enablement`, which VS Code applies
  // to every route into a command — including a tree row's own click handler,
  // where it silently does nothing.
  if (!state || typeof relPath !== "string" || !relPath) {
    return;
  }
  // A row clicked just before the tree repainted belongs to the pull request
  // that is gone, and its path would be joined to the wrong worktree.
  if (prNumber !== undefined && prNumber !== state.prepared.pr_number) {
    return;
  }
  const { worktree, base_sha: baseSHA } = state.prepared;
  const head = vscode.Uri.file(path.join(worktree, relPath));
  const base = baseUri(worktree, relPath, baseSHA);

  try {
    await vscode.commands.executeCommand(
      "vscode.diff",
      base,
      head,
      `${path.basename(relPath)} (${baseSHA.slice(0, 8)} ↔ head)`,
      // Active, not Beside: reviewing walks through many files, and letting each
      // one open a fresh editor group buries the review under split panes.
      //
      // preview is off deliberately. A preview diff is replaced only by another
      // editor of the same kind, so walking a reading order left the first file
      // pinned and every later click appearing to do nothing.
      { preview: false, viewColumn: vscode.ViewColumn.Active },
    );
  } catch (err) {
    // Falling back to the head side keeps the file readable, but staying silent
    // here is what turned a resolvable base into an unexplained dialog before.
    const message = err instanceof Error ? err.message : String(err);
    vscode.window.showWarningMessage(
      `pr-buddy: showing ${path.basename(relPath)} without its base side (${message})`,
    );
    await vscode.window.showTextDocument(head, {
      preview: false,
      viewColumn: vscode.ViewColumn.Active,
    });
  }
}

async function openFirstFile(prepared: Prepared): Promise<void> {
  // A reading order from a superseded review would open the wrong file first,
  // so it is only trusted while the review is current.
  const stored =
    prepared.review_status === "complete"
      ? readStoredReview(prepared.review_json)
      : undefined;
  // Shared with the panel deliberately: the file opened first must be the file
  // at the top of the list, or the reviewer starts somewhere the tree denies.
  const [first] = orderedFiles(prepared, stored);
  if (first) {
    await openFileDiff(first);
  }
}

async function openReviewMarkdown(): Promise<void> {
  const state = reviewTree.current();
  if (!state || !fs.existsSync(state.prepared.review_md)) {
    vscode.window.showInformationMessage("No review document yet.");
    return;
  }
  await vscode.commands.executeCommand(
    "markdown.showPreviewToSide",
    vscode.Uri.file(state.prepared.review_md),
  );
}

async function openOnGitHub(node: PullRequestNode): Promise<void> {
  if (node?.kind === "pr") {
    await vscode.env.openExternal(vscode.Uri.parse(node.pr.url));
  }
}

/**
 * Publishes findings as diagnostics so they appear on the changed lines and in
 * the Problems panel.
 */
function publishDiagnostics(
  prepared: Prepared,
  findings: Finding[] | undefined,
): void {
  diagnostics.clear();
  if (!findings?.length) {
    return;
  }
  const byFile = new Map<string, vscode.Diagnostic[]>();
  for (const f of findings) {
    const line = Math.max(0, (f.location.line ?? 1) - 1);
    const endLine = Math.max(
      line,
      (f.location.end_line ?? f.location.line ?? 1) - 1,
    );
    const diagnostic = new vscode.Diagnostic(
      new vscode.Range(line, 0, endLine, Number.MAX_SAFE_INTEGER),
      f.evidence ? `${f.message}\n\n${f.evidence}` : f.message,
      severityOf(f.severity),
    );
    diagnostic.source = "pr-buddy";
    diagnostic.code = f.rule;
    const abs = path.join(prepared.worktree, f.location.path);
    byFile.set(abs, [...(byFile.get(abs) ?? []), diagnostic]);
  }
  for (const [file, list] of byFile) {
    diagnostics.set(vscode.Uri.file(file), list);
  }
}

function severityOf(severity: Finding["severity"]): vscode.DiagnosticSeverity {
  switch (severity) {
    case "error":
      return vscode.DiagnosticSeverity.Error;
    case "warning":
      return vscode.DiagnosticSeverity.Warning;
    default:
      return vscode.DiagnosticSeverity.Information;
  }
}

function showError(err: unknown): void {
  const message =
    err instanceof PrBuddyError
      ? err.message
      : err instanceof Error
        ? err.message
        : String(err);
  vscode.window.showErrorMessage(`pr-buddy: ${message}`);
}
