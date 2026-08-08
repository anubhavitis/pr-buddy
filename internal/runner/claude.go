package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
)

// readOnlyTools is the complete set of capabilities a review is granted.
// Reading and searching only: no writing, no execution, no network.
var readOnlyTools = []string{"Read", "Grep", "Glob"}

// deniedTools is stated explicitly as well. Allow-listing already excludes
// these, but naming them means a future edit that widens the allow list does
// not silently regain the ability to execute pull request code.
var deniedTools = []string{
	"Bash", "Write", "Edit", "MultiEdit", "NotebookEdit",
	"Task", "Agent", "WebFetch", "WebSearch",
}

// ClaudeResult is what one review invocation produced.
type ClaudeResult struct {
	// Raw is the model's textual result, expected to contain the review JSON.
	Raw string
	// SessionID identifies the conversation for later resumption.
	SessionID string
	// Model is what actually served the request.
	Model string
}

// Claude invokes the Claude Code CLI in a strictly read-only configuration.
type Claude struct {
	Runner xexec.Runner
	// Bin is the CLI to invoke; defaults to "claude".
	Bin string
	// Model pins the model so that a change invalidates cached reviews.
	Model string
}

// claudeJSON mirrors the subset of `claude --output-format json` we consume.
//
// There is no top-level `model` field. The CLI reports what served the request
// under `modelUsage`, keyed by model name, with the unsuffixed name under
// `canonicalModel` -- verified against real output.
type claudeJSON struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	IsError   bool   `json:"is_error"`
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
	Error     string `json:"error"`

	ModelUsage map[string]struct {
		CanonicalModel string `json:"canonicalModel"`
	} `json:"modelUsage"`
}

// model reports which model served the request, preferring the canonical name
// over the keyed name so that "claude-opus-5[1m]" is recorded as
// "claude-opus-5". Empty when the CLI reported no usage.
func (p claudeJSON) model() string {
	// Sorted so that a multi-model response yields a stable answer rather than
	// whichever key the map happened to yield first.
	names := make([]string, 0, len(p.ModelUsage))
	for name := range p.ModelUsage {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if c := p.ModelUsage[name].CanonicalModel; c != "" {
			return c
		}
		return name
	}
	return ""
}

// Review runs the review prompt against dir and returns the model's output.
//
// dir is a worktree containing untrusted pull request code. Every flag below
// exists to ensure that code is read and never executed, and that nothing
// inside it can widen these permissions.
func (c *Claude) Review(ctx context.Context, dir, prompt string) (*ClaudeResult, error) {
	bin := c.Bin
	if bin == "" {
		bin = "claude"
	}

	args := []string{
		"--print",
		"--output-format", "json",
		// Deny before allow-listing; both are sent so neither alone is load
		// bearing.
		"--disallowed-tools", strings.Join(deniedTools, ","),
		"--allowed-tools", strings.Join(readOnlyTools, ","),
		// A reviewed repository must not be able to grant capability back
		// through its own committed settings, hooks, or MCP servers.
		"--setting-sources", "user",
		"--strict-mcp-config",
		// Never prompt: an unattended review must fail rather than wait, and
		// must never be answered by a permission bypass.
		"--permission-mode", "dontAsk",
	}
	// Deliberately no --session-id: asserting a caller-chosen id makes that id
	// single-use (the CLI rejects a second invocation under the same one), which
	// is precisely what leaves a review unresumable. The id the CLI returns is
	// the one `claude --resume` accepts, so that is what gets recorded.
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	args = append(args, prompt)

	out, err := c.Runner.Run(ctx, dir, bin, args...)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("claude invocation failed: %w", err)
	}

	var payload claudeJSON
	if jsonErr := json.Unmarshal([]byte(out), &payload); jsonErr != nil {
		return nil, &MalformedError{Raw: out, Err: jsonErr}
	}
	if payload.IsError {
		msg := payload.Error
		if msg == "" {
			msg = payload.Result
		}
		return nil, fmt.Errorf("claude reported an error: %s", strings.TrimSpace(msg))
	}
	if payload.SessionID == "" {
		return nil, errors.New("claude returned no session id; review would not be resumable")
	}

	model := payload.model()
	if model == "" {
		model = c.Model
	}
	return &ClaudeResult{Raw: payload.Result, SessionID: payload.SessionID, Model: model}, nil
}

// ResumeCommand is the command a human runs to continue a review conversation.
//
// Verified against the real CLI: `--resume` reattaches to a session created by
// `--print` and replays its history, so the returned id stays usable after the
// review process exits.
//
// The follow-up session is deliberately *not* restricted to the review's
// read-only tool set: this is an interactive chat a human drives, and the user
// chose full capability for it. That is a widening of the review's safety model
// -- the worktree still holds untrusted pull request code -- so it is a choice
// recorded here rather than an accident. It must never become a permission
// bypass; no --dangerously-skip-permissions is ever emitted.
func ResumeCommand(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	return "claude --resume " + sessionID
}

// MalformedError reports output that could not be parsed. The raw text is kept
// so a failed review can be diagnosed rather than merely retried.
type MalformedError struct {
	Raw string
	Err error
}

func (e *MalformedError) Error() string {
	return fmt.Sprintf("malformed claude output: %v", e.Err)
}

func (e *MalformedError) Unwrap() error { return e.Err }
