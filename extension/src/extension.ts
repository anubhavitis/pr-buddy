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

export function activate(context: vscode.ExtensionContext): void {
  prTree = new PullRequestTreeProvider();
  reviewTree = new ReviewTreeProvider();
  diagnostics = vscode.languages.createDiagnosticCollection("pr-buddy");

  context.subscriptions.push(
    vscode.window.createTreeView("prBuddy.pullRequests", { treeDataProvider: prTree }),
    vscode.window.createTreeView("prBuddy.review", { treeDataProvider: reviewTree }),
    diagnostics,
    vscode.commands.registerCommand("prBuddy.refresh", () => prTree.refresh()),
    vscode.commands.registerCommand("prBuddy.openPullRequest", openPullRequest),
    vscode.commands.registerCommand("prBuddy.runReview", () => review(false)),
    vscode.commands.registerCommand("prBuddy.rerunReview", () => review(true)),
    vscode.commands.registerCommand("prBuddy.openTerminal", openTerminal),
    vscode.commands.registerCommand("prBuddy.resumeChat", resumeChat),
    vscode.commands.registerCommand("prBuddy.openFileDiff", openFileDiff),
    vscode.commands.registerCommand("prBuddy.openFinding", openFinding),
    vscode.commands.registerCommand("prBuddy.openOnGitHub", openOnGitHub),
    vscode.commands.registerCommand("prBuddy.openReviewMarkdown", openReviewMarkdown),
  );
}

export function deactivate(): void {
  terminal?.dispose();
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

  const stored = readStoredReview(prepared.review_json);
  reviewTree.set({
    prepared,
    review: prepared.review_status === "complete" ? stored : undefined,
    running: false,
  });
  publishDiagnostics(prepared, prepared.review_status === "complete" ? stored?.findings : undefined);

  await openTerminal();
  await openFirstFile(prepared);

  const auto = vscode.workspace.getConfiguration("prBuddy").get<boolean>("autoReviewOnOpen", true);
  if (prepared.review_status === "complete") {
    vscode.window.setStatusBarMessage("pr-buddy: using cached review", 4000);
  } else if (auto) {
    void review(false);
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
        current.review = readStoredReview(result.review_json);
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
  terminal = vscode.window.createTerminal({ name, cwd: state.prepared.worktree });
  terminal.show(true);
}

async function resumeChat(): Promise<void> {
  const state = reviewTree.current();
  if (!state?.sessionId) {
    vscode.window.showInformationMessage("No review session yet. Run a review first.");
    return;
  }
  await openTerminal();
  terminal?.sendText(`claude --resume ${state.sessionId}`, false);
}

/** Shows the change as a side-by-side diff of base against head. */
async function openFileDiff(relPath: string): Promise<void> {
  const state = reviewTree.current();
  if (!state) {
    return;
  }
  const { worktree, base_sha: baseSHA } = state.prepared;
  const head = vscode.Uri.file(path.join(worktree, relPath));

  // git's own URI scheme renders the base side read-only, which is what a
  // diff against a fixed revision should be.
  const base = vscode.Uri.parse(
    `git:${path.join(worktree, relPath)}?${encodeURIComponent(JSON.stringify({ path: path.join(worktree, relPath), ref: baseSHA }))}`,
  );

  try {
    await vscode.commands.executeCommand(
      "vscode.diff",
      base,
      head,
      `${path.basename(relPath)} (${baseSHA.slice(0, 8)} ↔ head)`,
      { preview: true },
    );
  } catch {
    // A file added by the pull request has no base side to diff against.
    await vscode.window.showTextDocument(head, { preview: true });
  }
}

async function openFinding(finding: Finding): Promise<void> {
  const state = reviewTree.current();
  if (!state) {
    return;
  }
  const uri = vscode.Uri.file(path.join(state.prepared.worktree, finding.location.path));
  const line = Math.max(0, (finding.location.line ?? 1) - 1);
  const editor = await vscode.window.showTextDocument(uri, { preview: true });
  const range = new vscode.Range(line, 0, line, 0);
  editor.selection = new vscode.Selection(range.start, range.start);
  editor.revealRange(range, vscode.TextEditorRevealType.InCenter);
}

async function openFirstFile(prepared: Prepared): Promise<void> {
  const stored = readStoredReview(prepared.review_json);
  const first = stored?.reading_guide?.[0]?.paths?.[0] ?? prepared.changed_files?.[0];
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
function publishDiagnostics(prepared: Prepared, findings: Finding[] | undefined): void {
  diagnostics.clear();
  if (!findings?.length) {
    return;
  }
  const byFile = new Map<string, vscode.Diagnostic[]>();
  for (const f of findings) {
    const line = Math.max(0, (f.location.line ?? 1) - 1);
    const endLine = Math.max(line, (f.location.end_line ?? f.location.line ?? 1) - 1);
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
  const message = err instanceof PrBuddyError ? err.message : err instanceof Error ? err.message : String(err);
  vscode.window.showErrorMessage(`pr-buddy: ${message}`);
}
