import * as fs from "fs";
import * as path from "path";
import * as vscode from "vscode";
import { Prepared, StoredReview } from "./prBuddy";

/**
 * The review panel: a flat checklist of the pull request's changed files.
 *
 * Reading order survives, grouping does not. The reading guide names a file once
 * per group it belongs to, and a tree gives no signal that two rows are the same
 * file — the repetition, not the order, is what made the panel unreadable. So
 * groups collapse into ordering plus a tooltip, and each file appears exactly
 * once at its earliest mention.
 *
 * Findings are deliberately absent. They reach the reviewer as diagnostics, where
 * they sit on the lines they describe and the Problems panel filters them by
 * file, and as prose in the rendered review. A tree row can do neither well.
 */

export type ReviewNode = SummaryNode | FileNode | MessageNode;

interface SummaryNode {
  kind: "summary";
  label: string;
  detail?: string;
  /** The pull request title, which the label deliberately omits. */
  tooltip?: string;
}

interface FileNode {
  kind: "file";
  relPath: string;
  /** The reading group that placed this file here, when one did. */
  groupName?: string;
}

interface MessageNode {
  kind: "message";
  text: string;
  icon?: string;
  /** Full text, when the row shows a shortened form of it. */
  tooltip?: string;
}

/** What the panel is currently showing. */
export interface ReviewState {
  prepared: Prepared;
  review?: StoredReview;
  /** Set while a review is in flight, so the panel can say so. */
  running: boolean;
  sessionId?: string;
  /** How to reattach to the review conversation, as composed by the binary. */
  resumeCommand?: string;
  /**
   * Why the stored review was not adopted. Set when a cached review exists but
   * describes a different head, so the panel can explain the absence rather
   * than silently showing "not reviewed".
   */
  staleReason?: string;
  /** The worktree has the dependency tree, so imports resolve. */
  depsReady?: boolean;
  /**
   * The copied dependencies were resolved from a different lockfile than this
   * pull request's, so types may not be the ones it would build against.
   */
  depsLockfileDiffers?: boolean;
  /** Why dependency setup failed, when it did. */
  depsError?: string;
  /**
   * Repository-relative paths the reviewer has marked read, already filtered to
   * those still valid at the current head.
   */
  reviewed?: Set<string>;
}

export class ReviewTreeProvider implements vscode.TreeDataProvider<ReviewNode> {
  private readonly changed = new vscode.EventEmitter<ReviewNode | undefined>();
  readonly onDidChangeTreeData = this.changed.event;

  private state?: ReviewState;

  current(): ReviewState | undefined {
    return this.state;
  }

  set(state: ReviewState | undefined): void {
    this.state = state;
    this.changed.fire(undefined);
  }

  /**
   * Re-reads the stored artifact, picking up a review that finished out of band
   * — a terminal-initiated run, or one whose progress notification was
   * cancelled after the work had already completed.
   *
   * The head-SHA check is load-bearing: without it this becomes a second route
   * to adopting a review of different code, which is the bug the caller-side
   * status check exists to prevent.
   */
  reload(): void {
    if (!this.state) {
      return;
    }
    const stored = readStoredReview(this.state.prepared.review_json);
    if (
      stored?.status === "complete" &&
      stored.provenance?.head_sha === this.state.prepared.head_sha
    ) {
      this.state.review = stored;
      this.state.staleReason = undefined;
      this.changed.fire(undefined);
    }
  }

  getTreeItem(node: ReviewNode): vscode.TreeItem {
    switch (node.kind) {
      case "summary":
        return summaryItem(node, this.state);
      case "file":
        return fileItem(node, this.state);
      case "message":
        return messageItem(node);
    }
  }

  getChildren(node?: ReviewNode): ReviewNode[] {
    const state = this.state;
    if (!state) {
      return [
        {
          kind: "message",
          text: "Select a pull request to begin",
          icon: "git-pull-request",
        },
      ];
    }
    return node ? [] : this.roots(state);
  }

  private roots(state: ReviewState): ReviewNode[] {
    const { prepared, review, running, staleReason } = state;
    const nodes: ReviewNode[] = [];

    const files = orderedFiles(prepared, review);
    const reviewed = state.reviewed;
    // Counted against what is on screen, not against the stored record: a mark
    // can outlive the pull request's interest in the file it names, and a
    // denominator the reviewer cannot see rows for is one they cannot check.
    const done = reviewed ? files.filter((f) => reviewed.has(f)).length : 0;
    nodes.push({
      kind: "summary",
      // The number alone: the panel is narrow, and a title repeated on every
      // row crowds out the detail that actually changes.
      label: `PR-${prepared.pr_number}`,
      tooltip: prepared.title,
      detail: files.length ? `${done}/${files.length} read` : undefined,
    });

    // A superseded review is deliberately not shown, so say why rather than
    // leaving the panel looking simply unreviewed.
    if (staleReason && !running && !review) {
      nodes.push({
        kind: "message",
        text: `Cached review is out of date: ${staleReason}. Re-review to refresh.`,
        icon: "warning",
      });
    }

    // Only worth saying when it changes what the reviewer should trust: that
    // resolved types may not match the pull request, or that navigation is
    // degraded because the copy failed.
    if (state.depsError) {
      nodes.push({
        kind: "message",
        text: `Dependencies unavailable: ${state.depsError}. Imports will not resolve.`,
        icon: "warning",
      });
    } else if (state.depsReady && state.depsLockfileDiffers) {
      nodes.push({
        kind: "message",
        text: "Dependencies copied from your checkout, whose lockfile differs from this pull request's — resolved types may not match what it builds against.",
        icon: "info",
      });
    }

    if (running) {
      nodes.push({
        kind: "message",
        text: "Reviewing…",
        icon: "loading~spin",
      });
    }

    // The file list is the panel now. An empty one has to explain itself, or a
    // failed changed-files call reads as a pull request that changed nothing.
    if (!files.length) {
      if (!running) {
        nodes.push({
          kind: "message",
          text: "No changed files listed. Re-open the pull request to retry.",
          icon: "warning",
        });
      }
      return nodes;
    }

    // Read files sink to the bottom, where the run of green checks marks the
    // boundary without spending a row on a heading.
    const unread = files.filter((f) => !reviewed?.has(f));
    const read = files.filter((f) => reviewed?.has(f));
    for (const relPath of [...unread, ...read]) {
      nodes.push({
        kind: "file",
        relPath,
        groupName: groupNameOf(review, relPath),
      });
    }
    return nodes;
  }
}

/**
 * The pull request's changed files in the order the review says to read them.
 *
 * Guide paths are filtered against the changed set on purpose: the model reads
 * files for context that the pull request never touched, and offering those a
 * reviewed-mark would record progress against code nobody is reviewing.
 */
export function orderedFiles(
  prepared: Prepared,
  review: StoredReview | undefined,
): string[] {
  const changed = prepared.changed_files ?? [];
  const remaining = new Set(changed);
  const ordered: string[] = [];
  for (const group of review?.reading_guide ?? []) {
    for (const p of group.paths ?? []) {
      // First mention wins. The guide is ordered by what must be understood
      // first, so a later group repeating a file is not asking for it later.
      if (remaining.delete(p)) {
        ordered.push(p);
      }
    }
  }
  return [...ordered, ...changed.filter((p) => remaining.has(p))];
}

export function readStoredReview(
  reviewJsonPath: string,
): StoredReview | undefined {
  try {
    return JSON.parse(fs.readFileSync(reviewJsonPath, "utf8")) as StoredReview;
  } catch {
    return undefined;
  }
}

/** The first reading group naming this file, matching orderedFiles' choice. */
function groupNameOf(
  review: StoredReview | undefined,
  relPath: string,
): string | undefined {
  return (review?.reading_guide ?? []).find((g) =>
    (g.paths ?? []).includes(relPath),
  )?.name;
}

function summaryItem(node: SummaryNode, state?: ReviewState): vscode.TreeItem {
  const item = new vscode.TreeItem(
    node.label,
    vscode.TreeItemCollapsibleState.None,
  );
  item.description = node.detail;
  item.tooltip = node.tooltip;
  item.iconPath = new vscode.ThemeIcon("git-pull-request");
  item.contextValue = "reviewSummary";
  if (state?.review) {
    // The written review is where findings live now, so the row summarising it
    // is the way in.
    item.command = {
      command: "prBuddy.openReviewMarkdown",
      title: "Open Review Document",
    };
  }
  return item;
}

function fileItem(node: FileNode, state?: ReviewState): vscode.TreeItem {
  const item = new vscode.TreeItem(
    path.basename(node.relPath),
    vscode.TreeItemCollapsibleState.None,
  );
  const reviewed = state?.reviewed?.has(node.relPath) ?? false;
  const dir = path.dirname(node.relPath);
  item.description = reviewed ? `✓ ${dir}` : dir;
  item.tooltip = [
    node.relPath,
    node.groupName ? `\n${node.groupName}` : "",
    reviewed
      ? "\nMarked reviewed. The mark clears by itself if the author changes this file."
      : "",
  ].join("");
  item.resourceUri = state
    ? vscode.Uri.file(path.join(state.prepared.worktree, node.relPath))
    : undefined;
  // Left unset while unread so the icon theme resolves the file type; an
  // explicit ThemeIcon.File would suppress that and flatten every row to the
  // same generic page.
  item.iconPath = reviewed
    ? new vscode.ThemeIcon("check", new vscode.ThemeColor("charts.green"))
    : undefined;
  // Distinct context values let the menu offer mark or unmark, never both.
  item.contextValue = reviewed ? "reviewFileDone" : "reviewFile";
  if (state) {
    // The pull request travels with the path: a row rendered for one pull
    // request can still be clicked in the instant before the tree repaints
    // after a switch, and the handler has no other way to notice.
    item.command = {
      command: "prBuddy.openFileDiff",
      title: "Open Diff",
      arguments: [node.relPath, state.prepared.pr_number],
    };
  }
  return item;
}

function messageItem(node: MessageNode): vscode.TreeItem {
  const item = new vscode.TreeItem(
    node.text,
    vscode.TreeItemCollapsibleState.None,
  );
  item.iconPath = new vscode.ThemeIcon(node.icon ?? "info");
  item.tooltip = node.tooltip ?? node.text;
  return item;
}
