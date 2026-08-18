# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`pr-buddy` is a Chrome overlay on github.com Files tabs. It asks a local model
router for an understand-first file order plus per-file blurbs, then rewrites
GitHub's file tree and stacked diffs. The reviewer still writes every comment.

The interface is `browser/` + `cmd/pr-buddy-host`.

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

```
browser/ (Chrome, github.com Files tab)
  → scrape title / files / head SHA
  → POST 127.0.0.1:17342/complete
      cmd/pr-buddy-host → claude | grok | mlx_lm.server (loopback only)
  → reorder file tree + stacked diffs, inject group + per-file blurbs
```

**`internal/exec`** — the single boundary through which anything shells out.
`Runner` is an interface; `Fake` records calls and returns canned responses. No test
shells out for real, and no code path can quietly execute something. When adding a
subprocess call, route it through here or the safety tests become meaningless.

**`internal/host`** — loopback model router. `Completer` dispatches to claude,
grok, or mlx_lm.server. `Handler` exposes `/health` and `/complete`.

## Safety model

PR code is untrusted, including internal PRs.

- Nothing from a PR is built, installed, generated, or executed. The overlay
  scrapes GitHub's DOM; it never checks out the pull request.
- Host binds loopback only. Non-loopback MLX URLs are rejected.
- Claude and Grok run print-only (`--tools ""`, `--permission-mode dontAsk`).
  Never add `--dangerously-skip-permissions` or `bypassPermissions`.
- Comments stay in the GitHub UI, written by the human.

## Conventions

- TDD for parsing, caching, and host routing: tests before implementation.
- Tests use `exec.Fake`; nothing shells out for real.
- Comments explain *why* a constraint exists, not what the code does.
- Scope is deliberately narrow. The reviewer writes every comment.
