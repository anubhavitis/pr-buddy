# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`pr-buddy` is a Chrome overlay on github.com Files tabs. It asks a local model
router for an understand-first file order plus per-file blurbs, then rewrites
GitHub's file tree and stacked diffs. The reviewer still writes every comment.

The old VS Code extension has been removed. The Go review engine (worktrees,
`internal/runner`, `review.json`) remains in the repo and is unused by this
surface.

It is an experiment. `plan.md` is the older governing document. The current
interface is `browser/` + `cmd/pr-buddy-host`.

The overlay has not been run against a real pull request yet.

## Commands

```sh
go test ./...                                   # all Go tests
go test ./internal/host                         # model router
go test -race -count=1 ./...                    # what to run before committing
go vet ./...                                    # only static check configured
go build -o pr-buddy-host ./cmd/pr-buddy-host
cd browser && npm test && npm run compile       # Chrome extension
```

There is no linter beyond `go vet`. Per the user's global instructions, build and
lint only when the user asks to commit.

## Architecture

The live surface:

```
browser/ (Chrome, github.com Files tab)
  → scrape title / files / head SHA
  → POST 127.0.0.1:17342/complete
      cmd/pr-buddy-host → claude | grok | mlx_lm.server (loopback only)
  → reorder file tree + stacked diffs, inject group + per-file blurbs
```

The unused engine, still in the tree:

```
cmd/pr-buddy  →  gh.ViewPR       (real base/head SHAs from GitHub)
              →  worktree.Ensure (detached checkout at head SHA)
              →  runner.Run      (cache decision → read-only Claude → artifact)
              →  render.Markdown (derived, one-way)
```

**`internal/exec`** — the single boundary through which anything shells out.
`Runner` is an interface; `Fake` records calls and returns canned responses. No test
shells out for real, and no code path can quietly execute something. When adding a
subprocess call, route it through here or the safety tests become meaningless.

**`internal/artifact`** — `review.json` is the source of truth. Markdown and any
future editor integration are derived from it, never the reverse.

- `Provenance.CacheKey()` digests repo, PR number, base SHA, head SHA, rubric
  version, model, and schema version. Cache validity is this comparison — never
  timestamps. Adding a field that should invalidate reviews means adding it here.
- `WriteReview` validates then writes atomically (temp file, fsync, rename).
  `WriteRaw` is for `pending`/`running`/`failed` only and *refuses* a complete
  status — that asymmetry is what stops an interrupted run from ever being served.
- `FindingID` excludes line numbers on purpose: an edit above a finding shifts its
  line without changing the issue, and an ID that churns cannot support future
  suppressions.

**`internal/gh`** — read-only with exactly one exception. A test asserts no
`pr comment`/`review`/`merge`/`close`/`edit` command is ever issued. Base and head
SHAs come from GitHub, never from whatever is checked out locally.

The exception is `checks.go`'s `RerunChecks`, the only call in pr-buddy that
changes anything on GitHub. It re-runs the failed jobs of a workflow the author
already configured, and cannot publish review content, comment, approve, or
merge. Nothing derived from the model reaches it: the run id is parsed from a
check GitHub itself reported. It is still a write — it spends the repository's CI
minutes and triggers whatever the workflow does on completion — so the extension
confirms with the reviewer before calling it. `TestChecksIssueOnlyTheOnePermittedWrite`
pins it as the *only* permitted write; adding a second one means changing that
test deliberately rather than slipping past it.

**`internal/worktree`** — worktrees are detached (nothing can be pushed from them),
named by a digest of the repo slug (so PR 42 in two repos cannot collide), and live
outside the source repository. `Ensure` is idempotent; it refreshes on head movement
but returns `ErrDirtyWorktree` rather than discarding changes it did not make. Fork
heads arrive via `refs/pull/N/head`, so no untrusted remote is ever added.

**`internal/runner`** — owns the cache decision, the model invocation, and status
transitions. `RubricVersion` and the text in `prompt.go` travel together: **changing
the prompt without bumping `RubricVersion` silently serves reviews produced under a
different rubric.** Failures are classified (`invocation`, `malformed_output`,
`timeout`, `interrupted`) and persisted with detail. Finding IDs are derived here,
never taken from the model.

**`internal/render`** — session identity must not appear in rendered output, since a
reviewer may paste it into a GitHub comment. A test enforces this.

## Safety model

PR code is untrusted, including internal PRs. This is the constraint the design is
built around, and it is easy to break by accident:

- Nothing from a PR is built, installed, generated, or executed. No hooks, no
  repository scripts, no package managers.
- The review is granted `Read`, `Grep`, `Glob` and nothing else. `--setting-sources
  user` and `--strict-mcp-config` are load-bearing: without them a PR could ship its
  own `.claude/settings.json` and grant `Bash` back to itself.
- `--permission-mode dontAsk` makes an unattended run fail rather than hang. Never
  add `--dangerously-skip-permissions` or `bypassPermissions`.
- Nothing copies `.env`, `.npmrc`, or credentials into a worktree.
- `internal/deps` copies the *reviewer's own* installed dependencies and built
  workspace output into a worktree, so imports resolve and an editor can
  navigate. This is a deliberate narrowing of an earlier blanket rule, and the
  distinction it rests on is: those directories are the reviewer's artifacts,
  never the pull request's. Nothing is installed, built, or executed to obtain
  them, no package manager is invoked, and no manifest from the pull request is
  honoured. The copy is copy-on-write (`cp -c`) rather than a symlink
  *precisely* so that anything writing in the worktree — a stray build, a test
  run — cannot reach back into the reviewer's checkout. Replacing it with a
  symlink would silently reintroduce that path.
- Findings stay local. GitHub comments are always authored by the human. The one
  thing pr-buddy sends to GitHub is a confirmed re-run of failed CI jobs; no
  review output ever leaves the machine.

Tests in `worktree` and `runner` assert these properties directly. If one starts
failing, the safety model regressed — do not adjust the test to match the code.

## Conventions

- TDD for lifecycle, caching, parsing, and status transitions: tests before
  implementation, per `plan.md`.
- Tests use `t.TempDir()` and `exec.Fake`; nothing touches a real repository or
  network.
- Comments explain *why* a constraint exists, not what the code does. The
  non-obvious invariants above are worth a comment; ordinary Go is not.
- Scope is deliberately narrow. `plan.md` lists what is deferred (squiggles,
  automatic comments, suppressions, delta re-review, local models). Phase 9 says one
  feature at a time, selected by pilot evidence rather than preference — proposing
  deferred features without that evidence works against the plan.
