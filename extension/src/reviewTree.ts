import * as fs from "fs";
import * as path from "path";
import * as vscode from "vscode";
import { Finding, Prepared, ReadingGroup, StoredReview } from "./prBuddy";

/**
 * The review panel: reading order first, findings second.
 *
 * The reading guide is the point of the tool — files are listed in the order
 * the review says to read them, not alphabetically. Falls back to GitHub's
 * changed-file order while a review is still running.
 */

export type ReviewNode =
  SummaryNode | GroupNode | FileNode | FindingNode | MessageNode;

interface SummaryNode {
  kind: "summary";
  label: string;
  detail?: string;
  /** The pull request title, which the label deliberately omits. */
  tooltip?: string;
}

interface GroupNode {
  kind: "group";
  group: ReadingGroup;
  index: number;
}

interface FileNode {
  kind: "file";
  relPath: string;
  findings: Finding[];
}

interface FindingNode {
  kind: "finding";
  finding: Finding;
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

  /**
   * Whether the reader has folded the reading groups away. VS Code re-reads
   * `collapsibleState` on every refresh, so a hardcoded `Expanded` would undo
   * the collapse the moment a review reloads — the flag is what makes it stick.
   */
  private collapsed = false;

  current(): ReviewState | undefined {
    return this.state;
  }

  set(state: ReviewState | undefined): void {
    if (state?.prepared.pr_number !== this.state?.prepared.pr_number) {
      this.collapsed = false;
    }
    this.state = state;
    this.changed.fire(undefined);
  }

  /** Folds the reading groups; a new pull request starts expanded again. */
  collapse(): void {
    this.collapsed = true;
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
        return summaryItem(node);
      case "group":
        return groupItem(node, this.collapsed);
      case "file":
        return fileItem(node, this.state);
      case "finding":
        return findingItem(node, this.state);
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

    if (!node) {
      return this.roots(state);
    }
    if (node.kind === "group") {
      return node.group.paths.map((p) => ({
        kind: "file",
        relPath: p,
        findings: findingsFor(state.review, p),
      }));
    }
    if (node.kind === "file") {
      return node.findings.map((f) => ({ kind: "finding", finding: f }));
    }
    return [];
  }

  private roots(state: ReviewState): ReviewNode[] {
    const { prepared, review, running, staleReason } = state;
    const nodes: ReviewNode[] = [];

    const counts = countBySeverity(review?.findings ?? []);
    const total = prepared.changed_files?.length ?? 0;
    const done = state.reviewed?.size ?? 0;
    // How far through the files the reviewer is answers the question the panel
    // is open for; finding counts do not.
    const read = total && done ? ` · ${done}/${total} read` : "";
    nodes.push({
      kind: "summary",
      // The number alone: the panel is narrow, and a title repeated on every
      // row crowds out the detail that actually changes.
      label: `PR-${prepared.pr_number}`,
      tooltip: prepared.title,
      detail: running
        ? "reviewing…"
        : review
          ? `${counts.error} error · ${counts.warning} warning · ${counts.info} info${read}`
          : staleReason
            ? `out of date${read}`
            : `not reviewed${read}`,
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

    if (review?.summary) {
      // A tree row is one line: the rest of a paragraph is invisible anyway,
      // and letting it run only pushes the reading order further down.
      nodes.push({
        kind: "message",
        text: firstSentence(review.summary),
        tooltip: review.summary,
        icon: "note",
      });
    }

    const guide = review?.reading_guide ?? [];
    if (guide.length) {
      guide.forEach((group, index) =>
        nodes.push({ kind: "group", group, index }),
      );
      return nodes;
    }

    // No reading order yet: show GitHub's changed files so the worktree is
    // usable while the review runs.
    const files = prepared.changed_files ?? [];
    if (running) {
      nodes.push({
        kind: "message",
        text: "Reading order appears when the review finishes",
        icon: "loading~spin",
      });
    }
    for (const relPath of files) {
      nodes.push({
        kind: "file",
        relPath,
        findings: findingsFor(review, relPath),
      });
    }
    return nodes;
  }
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

function findingsFor(
  review: StoredReview | undefined,
  relPath: string,
): Finding[] {
  return (review?.findings ?? []).filter((f) => f.location.path === relPath);
}

function countBySeverity(findings: Finding[]): Record<string, number> {
  const counts: Record<string, number> = { error: 0, warning: 0, info: 0 };
  for (const f of findings) {
    counts[f.severity] = (counts[f.severity] ?? 0) + 1;
  }
  return counts;
}

function summaryItem(node: SummaryNode): vscode.TreeItem {
  const item = new vscode.TreeItem(
    node.label,
    vscode.TreeItemCollapsibleState.None,
  );
  item.description = node.detail;
  item.tooltip = node.tooltip;
  item.iconPath = new vscode.ThemeIcon("git-pull-request");
  item.contextValue = "reviewSummary";
  return item;
}

function groupItem(node: GroupNode, collapsed: boolean): vscode.TreeItem {
  const item = new vscode.TreeItem(
    `${node.index + 1}. ${node.group.name}`,
    collapsed
      ? vscode.TreeItemCollapsibleState.Collapsed
      : vscode.TreeItemCollapsibleState.Expanded,
  );
  item.description = node.group.summary;
  item.tooltip = node.group.summary;
  item.iconPath = new vscode.ThemeIcon("book");
  item.contextValue = "readingGroup";
  return item;
}

function fileItem(node: FileNode, state?: ReviewState): vscode.TreeItem {
  const worst = worstSeverity(node.findings);
  const item = new vscode.TreeItem(
    path.basename(node.relPath),
    node.findings.length
      ? vscode.TreeItemCollapsibleState.Collapsed
      : vscode.TreeItemCollapsibleState.None,
  );
  const reviewed = state?.reviewed?.has(node.relPath) ?? false;
  item.description = reviewed
    ? `✓ ${path.dirname(node.relPath)}`
    : path.dirname(node.relPath);
  item.tooltip = reviewed
    ? `${node.relPath}\n\nMarked reviewed. The mark clears by itself if the author changes this file.`
    : node.relPath;
  item.resourceUri = state
    ? vscode.Uri.file(path.join(state.prepared.worktree, node.relPath))
    : undefined;
  // A finding outranks the check: a file with an unresolved error is worth
  // seeing as such even once the reviewer has read it.
  item.iconPath = worst
    ? severityIcon(worst)
    : reviewed
      ? new vscode.ThemeIcon("check", new vscode.ThemeColor("charts.green"))
      : vscode.ThemeIcon.File;
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

function findingItem(node: FindingNode, state?: ReviewState): vscode.TreeItem {
  const { finding } = node;
  const item = new vscode.TreeItem(
    finding.message,
    vscode.TreeItemCollapsibleState.None,
  );
  item.description = finding.location.line
    ? `:${finding.location.line}`
    : finding.rule;
  item.iconPath = severityIcon(finding.severity);
  item.tooltip = new vscode.MarkdownString(
    [
      `**${finding.severity}** · \`${finding.rule}\` · confidence ${Math.round(finding.confidence * 100)}%`,
      "",
      finding.message,
      finding.evidence ? `\n${finding.evidence}` : "",
    ].join("\n"),
  );
  item.contextValue = "finding";
  if (state) {
    item.command = {
      command: "prBuddy.openFinding",
      title: "Go To Finding",
      arguments: [finding],
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

/**
 * The first sentence of a summary, bounded so it still fits a tree row.
 *
 * Sentence-first rather than a hard character cut: a summary's opening sentence
 * says what the change does, which is the part worth seeing without hovering.
 */
function firstSentence(text: string): string {
  const trimmed = text.trim().replace(/\s+/g, " ");
  const end = trimmed.search(/[.!?](\s|$)/);
  const sentence = end > 0 ? trimmed.slice(0, end + 1) : trimmed;
  return sentence.length > 100 ? `${sentence.slice(0, 99)}…` : sentence;
}

function worstSeverity(findings: Finding[]): Finding["severity"] | undefined {
  if (findings.some((f) => f.severity === "error")) {
    return "error";
  }
  if (findings.some((f) => f.severity === "warning")) {
    return "warning";
  }
  return findings.length ? "info" : undefined;
}

function severityIcon(severity: Finding["severity"]): vscode.ThemeIcon {
  switch (severity) {
    case "error":
      return new vscode.ThemeIcon(
        "error",
        new vscode.ThemeColor("problemsErrorIcon.foreground"),
      );
    case "warning":
      return new vscode.ThemeIcon(
        "warning",
        new vscode.ThemeColor("problemsWarningIcon.foreground"),
      );
    default:
      return new vscode.ThemeIcon(
        "info",
        new vscode.ThemeColor("problemsInfoIcon.foreground"),
      );
  }
}
