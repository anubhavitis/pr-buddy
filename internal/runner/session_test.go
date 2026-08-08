package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/anubhavitis/pr-buddy/internal/artifact"
	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
)

// A first-ever run has no cache to be stale against. Reporting one made the CLI
// announce "re-reviewing: no previous review" on a brand new pull request.
func TestRunReportsNoStaleReasonOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	rev := &stubReviewer{res: &ClaudeResult{Raw: goodOutput, SessionID: "s-1"}}
	res, err := newRunner(rev).Run(context.Background(), dir, "/wt", testProv())
	if err != nil {
		t.Fatal(err)
	}
	if res.StaleReason != "" {
		t.Errorf("first run reported stale reason %q; nothing was stale", res.StaleReason)
	}
}

// The session id must be the one the CLI returned, since that is the only id
// `claude --resume` will accept. pr-buddy must not invent one.
func TestRunRecordsTheSessionIDTheCLIReturned(t *testing.T) {
	dir := t.TempDir()
	rev := &stubReviewer{res: &ClaudeResult{Raw: goodOutput, SessionID: "cli-issued-id"}}
	if _, err := newRunner(rev).Run(context.Background(), dir, "/wt", testProv()); err != nil {
		t.Fatal(err)
	}
	sess, err := artifact.ReadSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	if sess.SessionID != "cli-issued-id" {
		t.Errorf("session id = %q, want the id the CLI returned", sess.SessionID)
	}
}

// session.json is the record of which model produced a review. An empty model
// makes it useless for diagnosis.
func TestRunRecordsTheModel(t *testing.T) {
	dir := t.TempDir()
	rev := &stubReviewer{res: &ClaudeResult{Raw: goodOutput, SessionID: "s-1", Model: "claude-opus-5"}}
	if _, err := newRunner(rev).Run(context.Background(), dir, "/wt", testProv()); err != nil {
		t.Fatal(err)
	}
	sess, err := artifact.ReadSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Model != "claude-opus-5" {
		t.Errorf("session model = %q, want the model that served the review", sess.Model)
	}
}

// The resume command is composed once, in Go, so the CLI and the extension
// cannot disagree about how to reattach to a session.
func TestRunRecordsAResumeCommand(t *testing.T) {
	dir := t.TempDir()
	rev := &stubReviewer{res: &ClaudeResult{Raw: goodOutput, SessionID: "cli-issued-id"}}
	if _, err := newRunner(rev).Run(context.Background(), dir, "/wt", testProv()); err != nil {
		t.Fatal(err)
	}
	sess, err := artifact.ReadSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sess.ResumeCommand, "cli-issued-id") {
		t.Errorf("resume command %q does not name the session", sess.ResumeCommand)
	}
	if !strings.Contains(sess.ResumeCommand, "--resume") {
		t.Errorf("resume command %q does not resume", sess.ResumeCommand)
	}
	// Even though the follow-up chat is deliberately unrestricted, it must never
	// be a permission bypass.
	for _, forbidden := range []string{"--dangerously-skip-permissions", "bypassPermissions"} {
		if strings.Contains(sess.ResumeCommand, forbidden) {
			t.Fatalf("resume command weakens permissions with %q", forbidden)
		}
	}
}

// The real CLI reports the model under `modelUsage`, keyed by model name; there
// is no top-level `model` field. Parsing the field that does not exist left the
// session record empty.
func TestClaudeReadsModelFromModelUsage(t *testing.T) {
	payload := `{"type":"result","subtype":"success","is_error":false,
		"result":"{}","session_id":"s-1",
		"modelUsage":{"claude-opus-5[1m]":{"inputTokens":2,"outputTokens":4,"canonicalModel":"claude-opus-5"}}}`
	f := xexec.NewFake().RespondOK("claude", payload)
	res, err := (&Claude{Runner: f}).Review(context.Background(), "/wt", "p")
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "claude-opus-5" {
		t.Errorf("model = %q, want the canonical model from modelUsage", res.Model)
	}
}

// With no modelUsage to read, the configured model is the honest answer.
func TestClaudeFallsBackToConfiguredModel(t *testing.T) {
	f := xexec.NewFake().RespondOK("claude", `{"result":"{}","session_id":"s-1"}`)
	res, err := (&Claude{Runner: f, Model: "claude-sonnet-5"}).Review(context.Background(), "/wt", "p")
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "claude-sonnet-5" {
		t.Errorf("model = %q, want the configured model", res.Model)
	}
}

// Asserting a caller-chosen id makes the id single-use: the CLI rejects a second
// invocation under the same one, which is what broke resume.
func TestClaudeDoesNotAssertACallerChosenSessionID(t *testing.T) {
	f := xexec.NewFake().RespondOK("claude", `{"result":"{}","session_id":"s-1"}`)
	if _, err := (&Claude{Runner: f}).Review(context.Background(), "/wt", "p"); err != nil {
		t.Fatal(err)
	}
	if line := f.CommandLines()[0]; strings.Contains(line, "--session-id") {
		t.Errorf("invocation asserts a session id, making it single-use\ngot: %s", line)
	}
}
