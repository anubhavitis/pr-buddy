package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
type claudeJSON struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	IsError   bool   `json:"is_error"`
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
	Error     string `json:"error"`
}

// Review runs the review prompt against dir and returns the model's output.
//
// dir is a worktree containing untrusted pull request code. Every flag below
// exists to ensure that code is read and never executed, and that nothing
// inside it can widen these permissions.
func (c *Claude) Review(ctx context.Context, dir, sessionID, prompt string) (*ClaudeResult, error) {
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
	if sessionID != "" {
		args = append(args, "--session-id", sessionID)
	}
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

	model := payload.Model
	if model == "" {
		model = c.Model
	}
	return &ClaudeResult{Raw: payload.Result, SessionID: payload.SessionID, Model: model}, nil
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
