# pr-buddy

A personal, AI-assisted PR review workflow. For a given pull request it creates an
isolated git worktree at the PR's real head, runs a read-only Claude review against
it, caches the result as a versioned artifact, and opens the review beside the diff
in VS Code.

Status: **in development.** The artifact layer exists; the worktree lifecycle,
review runner, and editor seam do not yet.

## Why

To test one hypothesis: that this reduces human review time by at least 25% without
reducing review quality. See [plan.md](plan.md) for the phased plan, exit criteria,
and the stop conditions that would end the project.

## Safety model

PR code is treated as untrusted, including internal PRs.

- No package installs, hooks, tasks, generators, or repository scripts ever run.
- No secrets or dependency directories are copied into review worktrees.
- The review runs without write or execution access.
- Findings stay local. GitHub comments are always written by you, deliberately.

## Design

`review.json` is the source of truth. Human-readable output and any future editor
integration are derived from it, never the reverse.

Cache validity is deterministic: a review is reusable only if it is `complete`,
valid, and its provenance digest — repo, PR number, base SHA, head SHA, rubric
version, model, schema version — matches the current one. Artifact writes are
atomic, so an interrupted run can never look complete.

Finding identity excludes line numbers, so a finding survives unrelated edits above
it and can be tracked or suppressed across runs.

## Requirements

Go 1.24+, `git`, `gh` (authenticated), and the `claude` CLI.

## Development

```sh
go test ./...
go vet ./...
```
