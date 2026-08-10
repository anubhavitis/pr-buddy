# pr-buddy

A personal, AI-assisted PR review workflow. For a given pull request it creates an
isolated git worktree at the PR's real head, runs a read-only Claude review against
it, caches the result as a versioned artifact, and opens the review beside the diff
in VS Code.

**You use it in VS Code.** The `pr-buddy` binary is the engine underneath — the
extension drives it for you. Running it from a terminal is supported but optional.

Status: **unverified.** The tool is built end to end, but it has not yet been run
against a real pull request. Nothing about the hypothesis has been tested.

## Prerequisites

| | Why |
|---|---|
| **VS Code** | The interface. Everything below is what it drives. |
| **Go 1.24+** | To build the `pr-buddy` binary. |
| **`git`** | Creates the isolated worktree at the PR head. |
| **`gh`**, authenticated | Reads PR metadata and CI checks. Run `gh auth login` first. |
| **`claude` CLI**, authenticated | Performs the review. |
| **Node 18+ / `npm`** | To build the extension only. Not needed at runtime. |

Check you are ready:

```sh
git --version && gh auth status && claude --version && go version
```

## Install

> **Not on the Marketplace yet — you build it yourself.**
>
> There is no `code --install-extension pr-buddy` from a registry, no Homebrew
> formula, and no release binary to download. Installing means cloning this repo
> and running the two builds below. Updating means pulling and running them
> again.
>
> Publishing it properly is planned, but it is deliberately behind the pilot:
> the tool has not yet been run against a real pull request, so there is nothing
> worth distributing until the hypothesis in [plan.md](plan.md) survives contact
> with one.

Build the engine, then the extension:

```sh
# 1. the binary
go build -o pr-buddy ./cmd/pr-buddy
sudo mv pr-buddy /usr/local/bin/     # or anywhere on your PATH

# 2. the extension
cd extension
npm install
npx @vscode/vsce package --no-dependencies
code --install-extension pr-buddy-0.1.0.vsix --force
```

If you keep the binary somewhere off your `PATH`, set `prBuddy.binaryPath` in VS
Code settings to its full path.

## Using it (VS Code)

Three panels:

- **Pull Requests** — pick an org and repo from the title bar, then a PR from a
  flat list showing `author → target branch`.
- **Review** — the PR's changed files in the order the review says to read them,
  each markable as read. Findings appear as diagnostics in the Problems panel and
  as prose in the rendered review, not as tree rows.
- **CI Checks** — check runs for the head commit, failures first, with a re-run
  action on failed workflow runs.

Pick a PR and the rest happens on its own: the checkout appears in seconds, the
review lands when it is ready, and re-opening an unchanged PR reuses the cached
result instead of paying for another one.

## Terminal usage (optional)

You never need this — the extension covers the same ground. It is here for
debugging, scripting, or working without an editor.

```sh
pr-buddy 1234                     # review PR 1234 in the current repository
pr-buddy -repo acme/widgets 1234  # review a PR elsewhere
pr-buddy -force 1234              # ignore a valid cached review
pr-buddy -open=false 1234         # print results without opening VS Code
```

Re-running for an unchanged PR reuses the cached review and invokes no model.

Subcommands emit JSON on stdout for programmatic callers — this is the interface
the extension itself uses: `list`, `prepare`, `review`, `remove`, `deps`,
`progress`, `checks`.

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

## Architecture

The extension is the interface; the binary is the engine. Both parse input and
format output, and neither decides how a review happens — that lives in
`internal/review`, so the two front ends can never drift apart.

```
     ┌─────────────────────────┐        ┌─────────────────────────┐
     │   VS Code extension     │ spawns │      CLI binary         │
     │   how you actually      │───────►│      the engine ·       │
     │   use it                │  JSON  │  also usable directly   │
     └─────────────────────────┘ stdout └───────────┬─────────────┘
                                                    │
       front ends: parse input, format output.      │
       neither decides how a review happens.        │
                                                    ▼
     ┌───────────────────────────────────────────────────────────────┐
     │                      internal/review                          │
     │                                                               │
     │    resolve repo → read PR → check out → cache? → review       │
     │                                                               │
     │    the one workflow both front ends call                      │
     └────┬─────────────┬─────────────┬──────────────┬───────────────┘
         │              │              │              │
         ▼              ▼              ▼              ▼
     ┌──────────┐  ┌──────────┐  ┌──────────┐    ┌───────────────┐
     │    gh    │  │ worktree │  │  runner  │    │   artifact    │
     │          │  │          │  │          │    │               │
     │ reads PR │  │ detached │  │  cache   │    │  review.json  │
     │ metadata │  │ checkout │  │ decision │───►│               │
     │ base +   │  │ at the   │  │    +     │    │ SOURCE OF     │
     │ head SHA │  │ head SHA │  │ model    │    │ TRUTH         │
     │          │  │          │  │ call     │    │               │
     └────┬─────┘  └────┬─────┘  └────┬─────┘    └───────┬───────┘
          │             │             │                  │
          │             │ after       │                  │
          │             ▼ checkout    │                  ▼
          │        ┌──────────┐       │            ┌───────────┐
          │        │   deps   │       │            │  render   │
          │        │          │       │            │           │
          │        │ copies   │       │            │ review.md │
          │        │ YOUR own │       │            └───────────┘
          │        │ installed│       │
          │        │ deps in  │       │
          │        └────┬─────┘       │
          │             │             │
          └─────────────┴──────┬──────┘
                               ▼
     ╔═══════════════════════════════════════════════════════════════╗
     ║                       internal/exec                           ║
     ║      the ONLY way anything in pr-buddy runs a subprocess      ║
     ║      no shell · swappable Fake in every test                  ║
     ╚═══════════════════════════╤═══════════════════════════════════╝
                                 ▼
                     ┌────────────────────────┐
                     │  git  ·  gh  ·  claude │
                     └────────────────────────┘
```

**Why it is shaped this way**

| Question | Answer |
|---|---|
| Why does the extension shell out instead of calling Go directly? | It gets one JSON contract instead of a language binding. The review logic stays in one place, and the same binary keeps working from a terminal. |
| Why is `internal/exec` in the middle of everything? | It is the single place a subprocess can start. A test asserts nothing bypasses it, which is what makes the safety guarantees testable rather than aspirational. |
| Why is `review.json` called the source of truth? | Markdown, the tree view, and the Problems panel are all derived from it. Nothing writes back. One file to inspect when something looks wrong. |
| Why a separate worktree per PR? | The review needs the PR's real head without disturbing your checkout. It is detached, so nothing can be pushed from it, and it lives outside your repo. |
| Why is `deps` separate from `worktree`? | Checkout takes seconds; copying dependencies can take a minute. The diff is readable before deps finish. They are also different trust decisions — one handles PR code, the other only ever touches your own files. |
| Why does the runner own the cache decision? | Cache validity and model invocation are the same decision: reuse, or spend a call. Splitting them lets them disagree. |
| What stops an interrupted run from being served later? | A complete review is written through a path that validates it; in-flight and failed states go through a separate one that *refuses* to write `complete`. |

**What happens when you open a PR**

```
  you pick PR 1234 in VS Code
            │
            ▼
  ┌───────────────────────────────────────────────────────────────┐
  │ 1. gh        which repo? what are the real base/head SHAs?    │
  │              ──► acme/widgets, base b3f…, head a91…           │
  ├───────────────────────────────────────────────────────────────┤
  │ 2. worktree  check out a91… detached, outside your repo       │
  │              ──► ready in seconds; the diff is now readable   │
  ├───────────────────────────────────────────────────────────────┤
  │ 3. artifact  is there a valid cached review for this exact    │
  │              repo + PR + base + head + rubric + model?        │
  └───────────────────────────────┬───────────────────────────────┘
                                  │
                  ┌───────────────┴───────────────┐
                  ▼                               ▼
        ╔═══════════════════╗           ╔═══════════════════════╗
        ║   YES — cache hit ║           ║  NO — miss or stale   ║
        ╠═══════════════════╣           ╠═══════════════════════╣
        ║ serve the stored  ║           ║ mark running          ║
        ║ review as-is      ║           ║   ↓                   ║
        ║                   ║           ║ claude reviews the    ║
        ║ no model call     ║           ║ checkout, read-only   ║
        ║ nothing is spent  ║           ║ (Read/Grep/Glob)      ║
        ║                   ║           ║   ↓                   ║
        ║                   ║           ║ write review.json     ║
        ║                   ║           ║ then session.json     ║
        ╚═════════┬═════════╝           ╚═══════════┬═══════════╝
                  │                                 │
                  └───────────────┬─────────────────┘
                                  ▼
                    review appears beside the diff
                    findings land in the Problems panel

  a stale cache always says why: "pr head moved", "rubric changed", …
```

## Design

`review.json` is the source of truth. Human-readable output and any future editor
integration are derived from it, never the reverse.

Cache validity is deterministic: a review is reusable only if it is `complete`,
valid, and its provenance digest — repo, PR number, base SHA, head SHA, rubric
version, model, schema version — matches the current one. Artifact writes are
atomic, so an interrupted run can never look complete.

Finding identity excludes line numbers, so a finding survives unrelated edits above
it and can be tracked or suppressed across runs.

## Development

```sh
go test -race -count=1 ./...
go vet ./...
cd extension && npx tsc -p ./ --noEmit
```
