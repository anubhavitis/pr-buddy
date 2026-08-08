import * as fs from "fs";
import * as path from "path";
import * as vscode from "vscode";
import {
  Finding,
  Prepared,
  PrBuddyError,
  prepare,
  review as runReview,
} from "./prBuddy";
import { BASE_SCHEME, BaseContentProvider, baseUri } from "./baseContent";
import { PullRequestNode, PullRequestTreeProvider } from "./pullRequestTree";
import { readStoredReview, ReviewTreeProvider } from "./reviewTree";

/**
 * Opening a pull request checks it out and shows the diff immediately, then
 * reviews in the background. Waiting minutes before any code is readable would
 * defeat the point.
 */

let prTree: PullRequestTreeProvider;
let reviewTree: ReviewTreeProvider;
let terminal: vscode.Terminal | undefined;
let diagnostics: vscode.DiagnosticCollection;
/** Watches the current review artifact so an out-of-band run is picked up. */
let artifactWatcher: vscode.FileSystemWatcher | undefined;

export function activate(context: vscode.ExtensionContext): void {
  prTree = new PullRequestTreeProvider();
  reviewTree = new ReviewTreeProvider();
  diagnostics = vscode.languages.createDiagnosticCollection("pr-buddy");

  context.subscriptions.push(
    vscode.window.createTreeView("prBuddy.pullRequests", {
      treeDataProvider: prTree,
    }),
    vscode.window.createTreeView("prBuddy.review", {
      treeDataProvider: reviewTree,
    }),
    vscode.workspace.registerTextDocumentContentProvider(
      BASE_SCHEME,
      new BaseContentProvider(),
    ),
    diagnostics,
    vscode.commands.registerCommand("prBuddy.refresh", () => prTree.refresh()),
    vscode.commands.registerCommand("prBuddy.reloadReview", () =>
      reviewTree.reload(),
    ),
    vscode.commands.registerCommand("prBuddy.openPullRequest", openPullRequest),
    vscode.commands.registerCommand("prBuddy.runReview", () => review(false)),
    vscode.commands.registerCommand("prBuddy.rerunReview", () => review(true)),
    vscode.commands.registerCommand("prBuddy.openTerminal", openTerminal),
    vscode.commands.registerCommand("prBuddy.resumeChat", resumeChat),
    vscode.commands.registerCommand("prBuddy.openFileDiff", openFileDiff),
    vscode.commands.registerCommand("prBuddy.openFinding", openFinding),
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
async function openFileDiff(relPath: string): Promise<void> {
  const state = reviewTree.current();
  if (!state) {
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
      { preview: true, viewColumn: vscode.ViewColumn.Active },
    );
  } catch (err) {
    // Falling back to the head side keeps the file readable, but staying silent
    // here is what turned a resolvable base into an unexplained dialog before.
    const message = err instanceof Error ? err.message : String(err);
    vscode.window.showWarningMessage(
      `pr-buddy: showing ${path.basename(relPath)} without its base side (${message})`,
    );
    await vscode.window.showTextDocument(head, {
      preview: true,
      viewColumn: vscode.ViewColumn.Active,
    });
  }
}

async function openFinding(finding: Finding): Promise<void> {
  const state = reviewTree.current();
  if (!state) {
    return;
  }
  const uri = vscode.Uri.file(
    path.join(state.prepared.worktree, finding.location.path),
  );
  const line = Math.max(0, (finding.location.line ?? 1) - 1);
  const editor = await vscode.window.showTextDocument(uri, {
    preview: true,
    viewColumn: vscode.ViewColumn.Active,
  });
  const range = new vscode.Range(line, 0, line, 0);
  editor.selection = new vscode.Selection(range.start, range.start);
  editor.revealRange(range, vscode.TextEditorRevealType.InCenter);
}

async function openFirstFile(prepared: Prepared): Promise<void> {
  // A reading order from a superseded review would open the wrong file first,
  // so it is only trusted while the review is current.
  const stored =
    prepared.review_status === "complete"
      ? readStoredReview(prepared.review_json)
      : undefined;
  const first =
    stored?.reading_guide?.[0]?.paths?.[0] ?? prepared.changed_files?.[0];
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
