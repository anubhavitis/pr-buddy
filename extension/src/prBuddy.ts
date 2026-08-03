import { execFile } from "child_process";
import * as vscode from "vscode";

/**
 * Typed wrapper around the pr-buddy binary.
 *
 * The extension owns no review logic of its own: worktree lifecycle, caching,
 * and the read-only model invocation all live in the Go binary, which is where
 * they are tested. This file only shells out and parses JSON.
 */

export interface Org {
  login: string;
  is_viewer: boolean;
}

export interface Repo {
  name: string;
  name_with_owner: string;
  private: boolean;
}

export interface PullRequest {
  number: number;
  title: string;
  author: string;
  state: string;
  is_draft: boolean;
  base_ref: string;
  changed_files: number;
  additions: number;
  deletions: number;
  updated_at: string;
  url: string;
}

export interface Prepared {
  repo: string;
  pr_number: number;
  title: string;
  state: string;
  base_ref: string;
  base_sha: string;
  head_sha: string;
  is_fork: boolean;
  worktree: string;
  artifact_dir: string;
  review_json: string;
  review_md: string;
  created: boolean;
  refreshed: boolean;
  changed_files: string[] | null;
  review_status: string;
  stale_reason?: string;
}

export interface Finding {
  id: string;
  severity: "error" | "warning" | "info";
  rule: string;
  message: string;
  location: { path: string; line?: number; end_line?: number };
  evidence?: string;
  confidence: number;
  reading_group?: string;
}

export interface ReadingGroup {
  name: string;
  summary?: string;
  paths: string[];
}

export interface ReviewResult {
  repo: string;
  pr_number: number;
  status: string;
  from_cache: boolean;
  stale_reason?: string;
  worktree: string;
  review_json: string;
  review_md: string;
  session_id?: string;
  counts: Record<string, number>;
  findings: Finding[];
}

/** The stored artifact, read directly for its reading guide and summary. */
export interface StoredReview {
  status: string;
  summary?: string;
  findings: Finding[];
  reading_guide?: ReadingGroup[];
  provenance: { repo: string; pr_number: number; head_sha: string };
}

export class PrBuddyError extends Error {
  constructor(message: string, readonly stderr: string) {
    super(message);
  }
}

function binary(): string {
  return vscode.workspace.getConfiguration("prBuddy").get<string>("binaryPath", "pr-buddy");
}

function model(): string {
  return vscode.workspace.getConfiguration("prBuddy").get<string>("model", "claude-opus-5");
}

/**
 * Runs the binary and parses its stdout as JSON.
 *
 * Reviews take minutes, so timeoutMs is generous by default and the token
 * allows a caller to abandon a run that is no longer wanted.
 */
function run<T>(args: string[], token?: vscode.CancellationToken, timeoutMs = 120_000): Promise<T> {
  return new Promise((resolve, reject) => {
    const child = execFile(
      binary(),
      args,
      { timeout: timeoutMs, maxBuffer: 32 * 1024 * 1024 },
      (err, stdout, stderr) => {
        if (err) {
          const hint =
            (err as NodeJS.ErrnoException).code === "ENOENT"
              ? `pr-buddy binary not found at "${binary()}". Set prBuddy.binaryPath.`
              : stderr.trim() || err.message;
          reject(new PrBuddyError(hint, stderr));
          return;
        }
        try {
          resolve(JSON.parse(stdout) as T);
        } catch {
          reject(new PrBuddyError("pr-buddy returned output that is not JSON", stdout.slice(0, 2000)));
        }
      },
    );
    token?.onCancellationRequested(() => child.kill());
  });
}

export async function listOrgs(token?: vscode.CancellationToken): Promise<Org[]> {
  const out = await run<{ orgs: Org[] }>(["list"], token, 30_000);
  return out.orgs ?? [];
}

export async function listRepos(org: string, token?: vscode.CancellationToken): Promise<Repo[]> {
  const limit = vscode.workspace.getConfiguration("prBuddy").get<number>("repoLimit", 100);
  const out = await run<{ repos: Repo[] }>(["list", "-org", org, "-limit", String(limit)], token, 60_000);
  return out.repos ?? [];
}

export async function listPullRequests(
  repo: string,
  token?: vscode.CancellationToken,
): Promise<PullRequest[]> {
  const limit = vscode.workspace.getConfiguration("prBuddy").get<number>("prLimit", 50);
  const out = await run<{ pull_requests: PullRequest[] }>(
    ["list", "-repo", repo, "-limit", String(limit)],
    token,
    60_000,
  );
  return out.pull_requests ?? [];
}

/** Checks the pull request out without reviewing it. */
export async function prepare(
  repo: string,
  prNumber: number,
  token?: vscode.CancellationToken,
): Promise<Prepared> {
  return run<Prepared>(
    ["prepare", "-repo", repo, "-model", model(), String(prNumber)],
    token,
    300_000,
  );
}

/** Runs a review, reusing a valid cached result unless force is set. */
export async function review(
  repo: string,
  prNumber: number,
  force: boolean,
  token?: vscode.CancellationToken,
): Promise<ReviewResult> {
  const args = ["review", "-repo", repo, "-model", model()];
  if (force) {
    args.push("-force");
  }
  args.push(String(prNumber));
  return run<ReviewResult>(args, token, 20 * 60_000);
}
