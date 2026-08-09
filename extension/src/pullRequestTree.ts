import * as vscode from "vscode";
import {
  listOrgs,
  listPullRequests,
  listRepos,
  Org,
  PullRequest,
  Repo,
} from "./prBuddy";

/**
 * The open pull requests of one selected repository.
 *
 * Organization and repository are chosen from the title bar rather than
 * expanded to. Browsing a three-level tree meant paying a network round trip to
 * reach a list the reviewer already knew they wanted, and every level of nesting
 * pushed the pull requests — the only rows that are ever clicked — further down.
 *
 * The selection is remembered across reloads: a reviewer works out of one
 * repository for days at a time, and re-picking it every window is the cost this
 * view exists to remove.
 */

export type Node = PullRequestNode | MessageNode;

export interface PullRequestNode {
  kind: "pr";
  repo: string;
  pr: PullRequest;
}

/** Renders an empty result or an error as an inert row rather than a popup. */
export interface MessageNode {
  kind: "message";
  text: string;
  tooltip?: string;
  icon?: string;
  /** Makes the row the way into the picker when there is nothing else to click. */
  command?: string;
}

const ORG_KEY = "prBuddy.selectedOrg";
const REPO_KEY = "prBuddy.selectedRepo";

export class PullRequestTreeProvider implements vscode.TreeDataProvider<Node> {
  private readonly changed = new vscode.EventEmitter<Node | undefined>();
  readonly onDidChangeTreeData = this.changed.event;

  private readonly repoCache = new Map<string, Repo[]>();
  private readonly prCache = new Map<string, PullRequest[]>();
  private orgs?: Org[];

  constructor(private readonly memento: vscode.Memento) {}

  org(): string | undefined {
    return this.memento.get<string>(ORG_KEY);
  }

  /** The selected repository as owner/name. */
  repo(): string | undefined {
    return this.memento.get<string>(REPO_KEY);
  }

  refresh(): void {
    this.orgs = undefined;
    this.repoCache.clear();
    this.prCache.clear();
    this.changed.fire(undefined);
  }

  /**
   * Picks an organization, then immediately its repository — choosing an org
   * alone leaves the view empty, which reads as a failure rather than a step.
   */
  async selectOrg(): Promise<void> {
    const orgs = await this.withError(() => this.allOrgs(), "organizations");
    if (!orgs?.length) {
      return;
    }
    const picked = await vscode.window.showQuickPick(
      orgs.map((o) => ({
        label: o.login,
        description: o.is_viewer ? "your account" : undefined,
        picked: o.login === this.org(),
      })),
      { title: "Select organization", matchOnDescription: true },
    );
    if (!picked) {
      return;
    }
    if (picked.label !== this.org()) {
      // The old repository belongs to the old org, and keeping it would show
      // pull requests from an organization the title bar no longer names.
      await this.memento.update(REPO_KEY, undefined);
    }
    await this.memento.update(ORG_KEY, picked.label);
    this.changed.fire(undefined);
    await this.selectRepo();
  }

  async selectRepo(): Promise<void> {
    const org = this.org();
    if (!org) {
      await this.selectOrg();
      return;
    }
    const repos = await this.withError(
      () => this.reposFor(org),
      `repositories in ${org}`,
    );
    if (!repos?.length) {
      return;
    }
    const picked = await vscode.window.showQuickPick(
      repos.map((r) => ({
        label: r.name,
        // Repos arrive newest-first; the age makes that ordering legible rather
        // than arbitrary, and is usually how the wanted one is recognised.
        description: [relativeAge(r.pushed_at), r.private ? "private" : ""]
          .filter(Boolean)
          .join(" · "),
        repo: r,
      })),
      { title: `Select repository in ${org}`, matchOnDescription: true },
    );
    if (picked) {
      await this.memento.update(REPO_KEY, picked.repo.name_with_owner);
      this.changed.fire(undefined);
    }
  }

  getTreeItem(node: Node): vscode.TreeItem {
    return node.kind === "pr" ? prItem(node) : messageItem(node);
  }

  async getChildren(node?: Node): Promise<Node[]> {
    if (node) {
      return [];
    }
    const repo = this.repo();
    if (!repo) {
      return [
        {
          kind: "message",
          text: this.org()
            ? "Select a repository"
            : "Select an organization to begin",
          icon: "search",
          command: this.org() ? "prBuddy.selectRepo" : "prBuddy.selectOrg",
        },
      ];
    }
    try {
      const prs = await this.pullRequestsFor(repo);
      return prs.length
        ? prs.map((pr) => ({ kind: "pr", repo, pr }) as PullRequestNode)
        : [{ kind: "message", text: "No open pull requests", icon: "info" }];
    } catch (err) {
      return [
        {
          kind: "message",
          text: "Could not load pull requests",
          tooltip: err instanceof Error ? err.message : String(err),
          icon: "warning",
        },
      ];
    }
  }

  private async allOrgs(): Promise<Org[]> {
    this.orgs ??= await listOrgs();
    return this.orgs;
  }

  private async reposFor(org: string): Promise<Repo[]> {
    const cached = this.repoCache.get(org);
    if (cached) {
      return cached;
    }
    const repos = await listRepos(org);
    this.repoCache.set(org, repos);
    return repos;
  }

  private async pullRequestsFor(repo: string): Promise<PullRequest[]> {
    const cached = this.prCache.get(repo);
    if (cached) {
      return cached;
    }
    const prs = await listPullRequests(repo);
    this.prCache.set(repo, prs);
    return prs;
  }

  /**
   * Fetches for a picker, reporting failure as a dialog.
   *
   * A picker has no row to render a message into, so unlike the tree it has
   * nowhere to fail quietly.
   */
  private async withError<T>(
    fetch: () => Promise<T[]>,
    what: string,
  ): Promise<T[] | undefined> {
    try {
      const items = await vscode.window.withProgress(
        { location: { viewId: "prBuddy.pullRequests" } },
        fetch,
      );
      if (!items.length) {
        vscode.window.showInformationMessage(`pr-buddy: no ${what} found.`);
      }
      return items;
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      vscode.window.showErrorMessage(
        `pr-buddy: could not list ${what} — ${message}`,
      );
      return undefined;
    }
  }
}

/** Compact age like "3d" or "2mo" for a RFC 3339 timestamp; empty when unknown. */
function relativeAge(timestamp: string): string {
  if (!timestamp) {
    return "";
  }
  const then = Date.parse(timestamp);
  if (Number.isNaN(then)) {
    return "";
  }
  const days = Math.floor((Date.now() - then) / 86_400_000);
  if (days <= 0) {
    return "today";
  }
  if (days < 30) {
    return `${days}d`;
  }
  if (days < 365) {
    return `${Math.floor(days / 30)}mo`;
  }
  return `${Math.floor(days / 365)}y`;
}

function prItem(node: PullRequestNode): vscode.TreeItem {
  const { pr } = node;
  // The number alone. A list of long titles truncates to near-identical
  // prefixes, so the text that survives is the least distinguishing part; the
  // title stays a hover away.
  const item = new vscode.TreeItem(
    `PR-${pr.number}`,
    vscode.TreeItemCollapsibleState.None,
  );
  // Who wrote it and where it lands. Size counts were the obvious thing to put
  // here and the wrong one: they say how much work the review is, not whether
  // it is work worth doing now, and a branch that merges somewhere unexpected
  // is the single thing a reviewer most needs to notice before reading a line.
  item.description = `${pr.author} → ${pr.base_ref}`;
  item.tooltip = new vscode.MarkdownString(
    [
      `**#${pr.number} ${pr.title}**`,
      "",
      `${pr.author} → \`${pr.base_ref}\``,
      `${pr.changed_files} files, +${pr.additions}/-${pr.deletions}`,
      pr.is_draft ? "\n_Draft_" : "",
    ].join("\n"),
  );
  item.iconPath = new vscode.ThemeIcon(
    pr.is_draft ? "git-pull-request-draft" : "git-pull-request",
    pr.is_draft ? undefined : new vscode.ThemeColor("charts.green"),
  );
  item.contextValue = "pullRequest";
  item.command = {
    command: "prBuddy.openPullRequest",
    title: "Open Pull Request",
    arguments: [node],
  };
  return item;
}

function messageItem(node: MessageNode): vscode.TreeItem {
  const item = new vscode.TreeItem(
    node.text,
    vscode.TreeItemCollapsibleState.None,
  );
  item.iconPath = new vscode.ThemeIcon(node.icon ?? "info");
  item.tooltip = node.tooltip;
  item.contextValue = "message";
  if (node.command) {
    item.command = { command: node.command, title: node.text };
  }
  return item;
}
