# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`pr-buddy` prepares a pull request for human review: it checks the PR head out into
an isolated worktree, runs a read-only Claude review against it, caches the result
as a versioned artifact, and opens the review beside the diff in VS Code.

It is an experiment with a hypothesis, not a product. `plan.md` is the governing
document — it defines nine phases, exit criteria, and **stop conditions that can end
the project**. Read it before proposing work. Phases 4–7 are implemented; Phases 1–3
are unrun evidence gates that belong to the user.

The tool has never been run against a real pull request. All tests mock the process
boundary.

## Commands

```sh
go test ./...                                   # all tests
go test ./internal/runner                       # one package
go test ./internal/runner -run TestRunUsesCache # one test
go test -race -count=1 ./...                    # what to run before committing
go vet ./...                                    # only static check configured
go build -o pr-buddy ./cmd/pr-buddy
```

There is no linter beyond `go vet`. Per the user's global instructions, build and
lint only when the user asks to commit.

## Architecture

Data flows one way, and each seam exists for a reason:

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

**`internal/gh`** — read-only. A test asserts no `pr comment`/`review`/`merge`/
`close`/`edit` command is ever issued. Base and head SHAs come from GitHub, never
from whatever is checked out locally.

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
- Findings stay local. GitHub comments are always authored by the human.

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
