import { execFile } from "child_process";
import * as vscode from "vscode";

/**
 * Serves the base revision of a file as read-only text.
 *
 * VS Code's built-in `git:` scheme is not usable here: it resolves paths
 * against repositories the git extension has opened, and review worktrees live
 * outside the workspace, so it reports every base side as nonexistent. Reading
 * the blob ourselves keeps the diff working regardless of what the workspace
 * contains.
 */
export const BASE_SCHEME = "pr-buddy-base";

/** Encodes a worktree path and revision into a URI this provider can serve. */
export function baseUri(
  worktree: string,
  relPath: string,
  ref: string,
): vscode.Uri {
  // The path carries the file only so the editor picks the right language and
  // tab title; worktree and ref travel in the query, which Uri encodes for us.
  return vscode.Uri.from({
    scheme: BASE_SCHEME,
    path: relPath.startsWith("/") ? relPath : `/${relPath}`,
    query: JSON.stringify({ worktree, ref }),
  });
}

export class BaseContentProvider implements vscode.TextDocumentContentProvider {
  async provideTextDocumentContent(uri: vscode.Uri): Promise<string> {
    let worktree: string;
    let ref: string;
    try {
      ({ worktree, ref } = JSON.parse(uri.query));
    } catch {
      throw new Error(`pr-buddy: malformed base URI ${uri.toString()}`);
    }

    const relPath = uri.path.replace(/^\//, "");
    try {
      return await gitShow(worktree, `${ref}:${relPath}`);
    } catch (err) {
      // A file the pull request adds does not exist at base. An empty base side
      // is the correct diff for that, and far more useful than an error dialog.
      if (isMissingAtRevision(err)) {
        return "";
      }
      throw err;
    }
  }
}

function gitShow(cwd: string, spec: string): Promise<string> {
  return new Promise((resolve, reject) => {
    execFile(
      "git",
      ["show", spec],
      // Blobs are diffed, not streamed; a generous cap avoids truncating a
      // large file into a misleading diff.
      { cwd, maxBuffer: 64 * 1024 * 1024, encoding: "utf8" },
      (err, stdout, stderr) => {
        if (err) {
          reject(new Error(stderr?.trim() || err.message));
          return;
        }
        resolve(stdout);
      },
    );
  });
}

// The two messages git emits for a path absent at a revision, verified against
// git's actual output: a file the PR adds reports "exists on disk", and a path
// under a new directory reports "does not exist in".
function isMissingAtRevision(err: unknown): boolean {
  const message = err instanceof Error ? err.message : String(err);
  return (
    message.includes("exists on disk, but not in") ||
    message.includes("does not exist in")
  );
}
