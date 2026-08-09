import * as vscode from "vscode";
import { CheckRun } from "./prBuddy";

/**
 * The CI checks panel: one row per check run on the pull request head.
 *
 * Read-only apart from a re-run, which is offered only on the workflow runs
 * that actually failed. A check that is merely skipped is not a failure — a job
 * that opts out on a path filter reports skipped, and treating that as broken
 * would mark healthy pull requests as failing.
 */

export type CheckNode = CheckItemNode | CheckMessageNode;

interface CheckItemNode {
  kind: "check";
  check: CheckRun;
}

interface CheckMessageNode {
  kind: "message";
  text: string;
  icon?: string;
  tooltip?: string;
}

export interface ChecksState {
  repo: string;
  prNumber: number;
  checks: CheckRun[];
  /** Set while a fetch is in flight and nothing has arrived yet. */
  loading?: boolean;
  error?: string;
}

export function failed(check: CheckRun): boolean {
  return (
    check.conclusion === "failure" ||
    check.conclusion === "timed_out" ||
    check.conclusion === "cancelled" ||
    check.conclusion === "action_required" ||
    check.conclusion === "stale"
  );
}

export function running(check: CheckRun): boolean {
  return check.status !== "completed";
}

export class ChecksTreeProvider implements vscode.TreeDataProvider<CheckNode> {
  private readonly changed = new vscode.EventEmitter<CheckNode | undefined>();
  readonly onDidChangeTreeData = this.changed.event;

  private state?: ChecksState;

  current(): ChecksState | undefined {
    return this.state;
  }

  set(state: ChecksState | undefined): void {
    this.state = state;
    this.changed.fire(undefined);
  }

  /** Whether anything is still running, which is what polling keys off. */
  anyRunning(): boolean {
    return (this.state?.checks ?? []).some(running);
  }

  getTreeItem(node: CheckNode): vscode.TreeItem {
    return node.kind === "check" ? checkItem(node.check) : messageItem(node);
  }

  getChildren(node?: CheckNode): CheckNode[] {
    if (node) {
      return [];
    }
    const state = this.state;
    if (!state) {
      return [{ kind: "message", text: "Open a pull request", icon: "info" }];
    }
    if (state.error) {
      return [
        {
          kind: "message",
          text: "Could not load checks",
          tooltip: state.error,
          icon: "warning",
        },
      ];
    }
    if (state.loading && !state.checks.length) {
      return [{ kind: "message", text: "Loading…", icon: "loading~spin" }];
    }
    if (!state.checks.length) {
      // A repository with no workflows is normal, and must not read as broken.
      return [{ kind: "message", text: "No checks on this commit", icon: "circle-slash" }];
    }
    // Failures first: the reason to open this panel is at the top, and the
    // passing rows below it are context rather than the point.
    const order = (c: CheckRun) => (failed(c) ? 0 : running(c) ? 1 : 2);
    return [...state.checks]
      .sort((a, b) => order(a) - order(b))
      .map((check) => ({ kind: "check", check }) as CheckItemNode);
  }
}

function checkItem(check: CheckRun): vscode.TreeItem {
  const item = new vscode.TreeItem(
    check.name,
    vscode.TreeItemCollapsibleState.None,
  );
  item.description = duration(check);
  item.iconPath = icon(check);
  item.tooltip = new vscode.MarkdownString(
    [
      `**${check.name}**`,
      "",
      `${check.status}${check.conclusion ? ` · ${check.conclusion}` : ""}`,
      check.started_at ? `\nstarted ${check.started_at}` : "",
    ].join("\n"),
  );
  // Only a failed Actions run can be re-run: a passing job has nothing to
  // repeat, and a check reported by another app has no workflow behind it.
  item.contextValue =
    failed(check) && check.workflow_run_id ? "checkFailed" : "check";
  if (check.url) {
    item.command = {
      command: "prBuddy.openCheck",
      title: "Open Check on GitHub",
      arguments: [check],
    };
  }
  return item;
}

function icon(check: CheckRun): vscode.ThemeIcon {
  if (running(check)) {
    return new vscode.ThemeIcon("loading~spin");
  }
  if (failed(check)) {
    return new vscode.ThemeIcon(
      "error",
      new vscode.ThemeColor("problemsErrorIcon.foreground"),
    );
  }
  if (check.conclusion === "skipped" || check.conclusion === "neutral") {
    return new vscode.ThemeIcon("circle-slash");
  }
  return new vscode.ThemeIcon("check", new vscode.ThemeColor("charts.green"));
}

/**
 * How long the check took, or how long it has been going.
 *
 * Sub-minute granularity because CI jobs are measured in seconds and minutes;
 * the day-granularity age used for repositories would render every one of them
 * as "today".
 */
function duration(check: CheckRun): string {
  const start = Date.parse(check.started_at);
  if (Number.isNaN(start)) {
    return check.conclusion || check.status;
  }
  const end = check.completed_at ? Date.parse(check.completed_at) : Date.now();
  if (Number.isNaN(end)) {
    return check.conclusion || check.status;
  }
  const seconds = Math.max(0, Math.round((end - start) / 1000));
  const spent = seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
  return running(check) ? `${spent}…` : spent;
}

function messageItem(node: CheckMessageNode): vscode.TreeItem {
  const item = new vscode.TreeItem(
    node.text,
    vscode.TreeItemCollapsibleState.None,
  );
  item.iconPath = new vscode.ThemeIcon(node.icon ?? "info");
  item.tooltip = node.tooltip ?? node.text;
  return item;
}
