<p align="center">
  <img src="browser/icons/icon128.png" width="96" height="96" alt="pr-buddy">
</p>

<h1 align="center">pr-buddy</h1>

<p align="center">
  Understand-first file order for GitHub pull requests.<br>
  You still write every comment.
</p>

GitHub lists changed files alphabetically. pr-buddy is a Chrome overlay on the Files tab that asks a **local** model for a reading order — contracts and types first, then implementation, then wiring, then tests — and rewrites the file list and stacked diffs to match.

No findings. No draft comments. The review stays yours.

<p align="center">
  <img src="docs/flow.svg" width="720" alt="Open a Files tab, a local model orders the files, you write the review">
</p>

## Use (Mac, Apple Silicon)

**Need:** Chrome, an M-series Mac, and at least one backend — [`claude`](https://docs.anthropic.com/en/docs/claude-code) CLI, [`grok`](https://docs.x.ai/docs) CLI, or [`mlx_lm.server`](https://github.com/ml-explore/mlx-lm) on loopback.

No Go or npm.

1. Download `pr-buddy-host-darwin-arm64` and `pr-buddy-extension.zip` from the [latest Release](https://github.com/anubhavitis/pr-buddy/releases/latest)
2. Start the host and leave it running (loopback only, `127.0.0.1:17342`):

```sh
chmod +x pr-buddy-host-darwin-arm64
./pr-buddy-host-darwin-arm64
```

If macOS says the binary is damaged or unverified: right-click it → **Open**.
3. Unzip `pr-buddy-extension.zip`
4. Chrome → `chrome://extensions` → **Developer mode** → **Load unpacked** → select `pr-buddy-extension/`
5. Open a pull request **Files** tab (`/files` or `/changes`)
6. Click the **pr-buddy** pill (next to Ready to merge) and pick `claude`, `grok`, or `mlx`

For MLX, set the URL (default `http://127.0.0.1:8080/v1`) and the model id.

Click the extension icon: it should say `host: running`. If it says offline, the host is not running.

## What you see

| Surface | What it does |
|---|---|
| **Status pill** | Model picker and **Refresh order** |
| **Left file list** | Numbered paths in review order |
| **Each stacked diff** | Collapsed **Summary** under the file header |

Same head SHA + same backend is cached (`owner/repo#n:sha:backend`). **Refresh order** ignores the cache.

## Safety

- Host binds loopback only. Non-loopback MLX URLs are rejected.
- Claude and Grok run print-only, no tools, `--permission-mode dontAsk`.
- Nothing from the PR is built, installed, or executed.
- Comments stay in the GitHub UI, written by you.

## Development

**Need:** [Go 1.24+](https://go.dev/dl/), Node 18+.

```sh
./install-browser.sh          # local host + unpacked browser/
./pr-buddy-host               # leave running
```

Load unpacked → `browser/`. After changing `browser/src`, reload the extension, then hard-reload the tab.

```sh
go test ./...
cd browser && npm test && npm run compile
./package-mac.sh              # dist/ host + extension zip
```

Tag `v*` and push to cut a GitHub Release (`darwin/arm64` only).
