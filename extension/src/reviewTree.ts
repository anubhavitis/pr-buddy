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

export type ReviewNode = SummaryNode | GroupNode | FileNode | FindingNode | MessageNode;

interface SummaryNode {
  kind: "summary";
  label: string;
  detail?: string;
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
}

/** What the panel is currently showing. */
export interface ReviewState {
  prepared: Prepared;
  review?: StoredReview;
  /** Set while a review is in flight, so the panel can say so. */
  running: boolean;
  sessionId?: string;
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

  /** Re-reads the stored artifact, picking up a review that has just finished. */
  reload(): void {
    if (!this.state) {
      return;
    }
    const stored = readStoredReview(this.state.prepared.review_json);
    if (stored) {
      this.state.review = stored;
    }
    this.changed.fire(undefined);
  }

  getTreeItem(node: ReviewNode): vscode.TreeItem {
    switch (node.kind) {
      case "summary":
        return summaryItem(node);
      case "group":
        return groupItem(node);
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
      return [{ kind: "message", text: "Select a pull request to begin", icon: "git-pull-request" }];
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
    const { prepared, review, running } = state;
    const nodes: ReviewNode[] = [];

    const counts = countBySeverity(review?.findings ?? []);
    nodes.push({
      kind: "summary",
      label: `#${prepared.pr_number} ${prepared.title}`,
      detail: running
        ? "reviewing…"
        : review
          ? `${counts.error} error · ${counts.warning} warning · ${counts.info} info`
          : "not reviewed",
    });

    if (review?.summary) {
      nodes.push({ kind: "message", text: review.summary, icon: "note" });
    }

    const guide = review?.reading_guide ?? [];
    if (guide.length) {
      guide.forEach((group, index) => nodes.push({ kind: "group", group, index }));
      return nodes;
    }

    // No reading order yet: show GitHub's changed files so the worktree is
    // usable while the review runs.
    const files = prepared.changed_files ?? [];
    if (running) {
      nodes.push({ kind: "message", text: "Reading order appears when the review finishes", icon: "loading~spin" });
    }
    for (const relPath of files) {
      nodes.push({ kind: "file", relPath, findings: findingsFor(review, relPath) });
    }
    return nodes;
  }
}

export function readStoredReview(reviewJsonPath: string): StoredReview | undefined {
  try {
    return JSON.parse(fs.readFileSync(reviewJsonPath, "utf8")) as StoredReview;
  } catch {
    return undefined;
  }
}

function findingsFor(review: StoredReview | undefined, relPath: string): Finding[] {
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
  const item = new vscode.TreeItem(node.label, vscode.TreeItemCollapsibleState.None);
  item.description = node.detail;
  item.iconPath = new vscode.ThemeIcon("git-pull-request");
  item.contextValue = "reviewSummary";
  return item;
}

function groupItem(node: GroupNode): vscode.TreeItem {
  const item = new vscode.TreeItem(
    `${node.index + 1}. ${node.group.name}`,
    vscode.TreeItemCollapsibleState.Expanded,
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
    node.findings.length ? vscode.TreeItemCollapsibleState.Collapsed : vscode.TreeItemCollapsibleState.None,
  );
  item.description = path.dirname(node.relPath);
  item.tooltip = node.relPath;
  item.resourceUri = state ? vscode.Uri.file(path.join(state.prepared.worktree, node.relPath)) : undefined;
  item.iconPath = worst ? severityIcon(worst) : vscode.ThemeIcon.File;
  item.contextValue = "reviewFile";
  if (state) {
    item.command = {
      command: "prBuddy.openFileDiff",
      title: "Open Diff",
      arguments: [node.relPath],
    };
  }
  return item;
}

function findingItem(node: FindingNode, state?: ReviewState): vscode.TreeItem {
  const { finding } = node;
  const item = new vscode.TreeItem(finding.message, vscode.TreeItemCollapsibleState.None);
  item.description = finding.location.line ? `:${finding.location.line}` : finding.rule;
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
  const item = new vscode.TreeItem(node.text, vscode.TreeItemCollapsibleState.None);
  item.iconPath = new vscode.ThemeIcon(node.icon ?? "info");
  item.tooltip = node.text;
  return item;
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
      return new vscode.ThemeIcon("error", new vscode.ThemeColor("problemsErrorIcon.foreground"));
    case "warning":
      return new vscode.ThemeIcon("warning", new vscode.ThemeColor("problemsWarningIcon.foreground"));
    default:
      return new vscode.ThemeIcon("info", new vscode.ThemeColor("problemsInfoIcon.foreground"));
  }
}
