# pr-buddy

A personal, AI-assisted PR review workflow. For a given pull request it creates an
isolated git worktree at the PR's real head, runs a read-only Claude review against
it, caches the result as a versioned artifact, and opens the review beside the diff
in VS Code.

Status: **unverified.** The tool is built end to end, but it has not yet been run
against a real pull request. Nothing about the hypothesis has been tested.

## Usage

```sh
pr-buddy 1234                     # review PR 1234 in the current repository
pr-buddy -repo acme/widgets 1234  # review a PR elsewhere
pr-buddy -force 1234              # ignore a valid cached review
pr-buddy -open=false 1234         # print results without opening VS Code
```

Re-running for an unchanged PR reuses the cached review and invokes no model.

Subcommands emit JSON for programmatic callers such as the VS Code extension:
`list`, `prepare`, `review`, `remove`, `deps`, `progress`, `checks`.

## VS Code extension

`extension/` adds three panels:

- **Pull Requests** — pick an org and repo from the title bar, then a PR from a
  flat list showing `author → target branch`.
- **Review** — the PR's changed files in the order the review says to read them,
  each markable as read. Findings appear as diagnostics in the Problems panel and
  as prose in the rendered review, not as tree rows.
- **CI Checks** — check runs for the head commit, failures first, with a re-run
  action on failed workflow runs.

```sh
cd extension
npm install
npx @vscode/vsce package --no-dependencies
code --install-extension pr-buddy-0.1.0.vsix --force
```

Set `prBuddy.binaryPath` if `pr-buddy` is not on your `PATH`.

## Why

To test one hypothesis: that this reduces human review time by at least 25% without
reducing review quality. See [plan.md](plan.md) for the phased plan, exit criteria,
and the stop conditions that would end the project.

## Safety model

PR code is treated as untrusted, including internal PRs.

- No package installs, hooks, tasks, generators, or repository scripts ever run.
- No secrets are copied into review worktrees. Your own already-installed
  dependencies are copied in so imports resolve; nothing is installed or built to
  produce them, and no manifest from the PR is honoured.
- The review runs with read-only tools and no execution access.
- Findings stay local. GitHub comments are always written by you, deliberately.

The one exception to reading only: re-running a workflow's failed CI jobs, which
you confirm each time. It cannot comment, approve, or merge, and no review output
ever leaves your machine.

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
go test -race -count=1 ./...
go vet ./...
cd extension && npx tsc -p ./ --noEmit
```
