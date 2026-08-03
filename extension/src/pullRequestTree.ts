import * as vscode from "vscode";
import { listOrgs, listPullRequests, listRepos, Org, PullRequest, Repo } from "./prBuddy";

/**
 * The org → repo → pull request tree.
 *
 * Each level is fetched only when expanded. Enumerating every pull request in
 * every repository up front would take minutes on a real account, so children
 * are resolved lazily and cached until an explicit refresh.
 */

export type Node = OrgNode | RepoNode | PullRequestNode | MessageNode;

export interface OrgNode {
  kind: "org";
  org: Org;
}

export interface RepoNode {
  kind: "repo";
  repo: Repo;
}

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
}

export class PullRequestTreeProvider implements vscode.TreeDataProvider<Node> {
  private readonly changed = new vscode.EventEmitter<Node | undefined>();
  readonly onDidChangeTreeData = this.changed.event;

  private readonly repoCache = new Map<string, Repo[]>();
  private readonly prCache = new Map<string, PullRequest[]>();

  refresh(): void {
    this.repoCache.clear();
    this.prCache.clear();
    this.changed.fire(undefined);
  }

  getTreeItem(node: Node): vscode.TreeItem {
    switch (node.kind) {
      case "org":
        return orgItem(node);
      case "repo":
        return repoItem(node);
      case "pr":
        return prItem(node);
      case "message":
        return messageItem(node);
    }
  }

  async getChildren(node?: Node): Promise<Node[]> {
    try {
      if (!node) {
        const orgs = await listOrgs();
        return orgs.length
          ? orgs.map((org) => ({ kind: "org", org }) as OrgNode)
          : [{ kind: "message", text: "No organizations found" }];
      }

      if (node.kind === "org") {
        const repos = await this.reposFor(node.org.login);
        return repos.length
          ? repos.map((repo) => ({ kind: "repo", repo }) as RepoNode)
          : [{ kind: "message", text: "No repositories" }];
      }

      if (node.kind === "repo") {
        const prs = await this.pullRequestsFor(node.repo.name_with_owner);
        return prs.length
          ? prs.map((pr) => ({ kind: "pr", repo: node.repo.name_with_owner, pr }) as PullRequestNode)
          : [{ kind: "message", text: "No open pull requests" }];
      }

      return [];
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      return [{ kind: "message", text: "Could not load", tooltip: message }];
    }
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
}

function orgItem(node: OrgNode): vscode.TreeItem {
  const item = new vscode.TreeItem(node.org.login, vscode.TreeItemCollapsibleState.Collapsed);
  item.iconPath = new vscode.ThemeIcon(node.org.is_viewer ? "account" : "organization");
  item.contextValue = "org";
  return item;
}

function repoItem(node: RepoNode): vscode.TreeItem {
  const item = new vscode.TreeItem(node.repo.name, vscode.TreeItemCollapsibleState.Collapsed);
  item.iconPath = new vscode.ThemeIcon(node.repo.private ? "lock" : "repo");
  item.tooltip = node.repo.name_with_owner;
  item.contextValue = "repo";
  return item;
}

function prItem(node: PullRequestNode): vscode.TreeItem {
  const { pr } = node;
  const item = new vscode.TreeItem(`#${pr.number} ${pr.title}`, vscode.TreeItemCollapsibleState.None);
  item.description = `${pr.author} · ${pr.changed_files}f +${pr.additions}/-${pr.deletions}`;
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
  const item = new vscode.TreeItem(node.text, vscode.TreeItemCollapsibleState.None);
  item.iconPath = new vscode.ThemeIcon("info");
  item.tooltip = node.tooltip;
  item.contextValue = "message";
  return item;
}
