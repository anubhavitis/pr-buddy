package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/anubhavitis/pr-buddy/internal/artifact"
	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
)

const goodOutput = `{
  "summary": "Adds retry backoff to the fetch client.",
  "findings": [
    {"severity":"error","rule":"unbounded-retry","message":"Retry loop has no ceiling",
     "path":"client/fetch.go","line":88,"evidence":"attempts is never compared to maxAttempts","confidence":0.9,
     "reading_group":"Retry logic"}
  ],
  "reading_guide": [
    {"name":"Retry logic","summary":"Start here","paths":["client/fetch.go"]}
  ]
}`

// stubReviewer stands in for the Claude CLI.
type stubReviewer struct {
	res     *ClaudeResult
	err     error
	calls   int
	lastDir string
	delay   time.Duration
}

func (s *stubReviewer) Review(ctx context.Context, dir, prompt string) (*ClaudeResult, error) {
	s.calls++
	s.lastDir = dir
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	res := s.res
	// The real CLI issues the session id itself; a stub that echoed back the
	// caller's would hide the fact that pr-buddy no longer supplies one.
	if res != nil && res.SessionID == "" {
		res.SessionID = fmt.Sprintf("cli-session-%d", s.calls)
	}
	return res, nil
}

func testProv() artifact.Provenance {
	return artifact.Provenance{
		Repo:          "acme/widgets",
		PRNumber:      42,
		BaseSHA:       "1111111111111111111111111111111111111111",
		HeadSHA:       "2222222222222222222222222222222222222222",
		RubricVersion: RubricVersion,
		Model:         "claude-opus-5",
		SchemaVersion: artifact.Version,
	}
}

func newRunner(rev Reviewer) *Runner {
	return &Runner{
		Reviewer: rev,
		Now:      func() time.Time { return time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC) },
	}
}

func TestRunProducesCompleteArtifactAndSession(t *testing.T) {
	dir := t.TempDir()
	rev := &stubReviewer{res: &ClaudeResult{Raw: goodOutput, SessionID: "session-a", Model: "claude-opus-5"}}
	res, err := newRunner(rev).Run(context.Background(), dir, "/wt", testProv())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FromCache {
		t.Error("first run should not be a cache hit")
	}
	if res.Review.Status != artifact.StatusComplete {
		t.Errorf("status = %q", res.Review.Status)
	}
	if len(res.Review.Findings) != 1 {
		t.Fatalf("got %d findings", len(res.Review.Findings))
	}
	if res.Review.Findings[0].ID == "" {
		t.Error("finding id not derived")
	}
	if res.Review.Summary == "" {
		t.Error("summary lost")
	}
	if len(res.Review.ReadingGuide) != 1 {
		t.Error("reading guide lost")
	}

	sess, err := artifact.ReadSession(dir)
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if sess.SessionID != "session-a" {
		t.Errorf("session id = %q", sess.SessionID)
	}
}

func TestRunReviewsInsideTheWorktree(t *testing.T) {
	rev := &stubReviewer{res: &ClaudeResult{Raw: goodOutput}}
	if _, err := newRunner(rev).Run(context.Background(), t.TempDir(), "/worktrees/acme-42", testProv()); err != nil {
		t.Fatal(err)
	}
	if rev.lastDir != "/worktrees/acme-42" {
		t.Fatalf("reviewed %q, not the worktree", rev.lastDir)
	}
}

func TestRunUsesCacheForUnchangedPR(t *testing.T) {
	dir := t.TempDir()
	rev := &stubReviewer{res: &ClaudeResult{Raw: goodOutput}}
	r := newRunner(rev)
	if _, err := r.Run(context.Background(), dir, "/wt", testProv()); err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(context.Background(), dir, "/wt", testProv())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if !res.FromCache {
		t.Error("expected cache hit")
	}
	if rev.calls != 1 {
		t.Errorf("model invoked %d times, want 1", rev.calls)
	}
}

func TestRunReReviewsOnEveryInvalidatingChange(t *testing.T) {
	cases := map[string]func(*artifact.Provenance){
		"head moved":    func(p *artifact.Provenance) { p.HeadSHA = "3333333333333333333333333333333333333333" },
		"base moved":    func(p *artifact.Provenance) { p.BaseSHA = "4444444444444444444444444444444444444444" },
		"rubric bumped": func(p *artifact.Provenance) { p.RubricVersion = "code-review@2" },
		"model changed": func(p *artifact.Provenance) { p.Model = "claude-sonnet-5" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			rev := &stubReviewer{res: &ClaudeResult{Raw: goodOutput}}
			r := newRunner(rev)
			if _, err := r.Run(context.Background(), dir, "/wt", testProv()); err != nil {
				t.Fatal(err)
			}
			p := testProv()
			mutate(&p)
			res, err := r.Run(context.Background(), dir, "/wt", p)
			if err != nil {
				t.Fatal(err)
			}
			if res.FromCache {
				t.Fatalf("%s should have invalidated the cache", name)
			}
			if res.StaleReason == "" {
				t.Error("stale reason not reported")
			}
			if rev.calls != 2 {
				t.Errorf("model invoked %d times, want 2", rev.calls)
			}
		})
	}
}

func TestRunRecordsFailureAndKeepsItUnusable(t *testing.T) {
	dir := t.TempDir()
	rev := &stubReviewer{err: errors.New("boom")}
	if _, err := newRunner(rev).Run(context.Background(), dir, "/wt", testProv()); err == nil {
		t.Fatal("expected error")
	}
	stored, err := artifact.ReadReview(dir)
	if err != nil {
		t.Fatalf("ReadReview: %v", err)
	}
	if stored.Status != artifact.StatusFailed {
		t.Errorf("status = %q, want failed", stored.Status)
	}
	if stored.Failure == nil || stored.Failure.Kind != "invocation" {
		t.Errorf("failure detail = %+v", stored.Failure)
	}
	if stored.Usable(testProv()) {
		t.Fatal("failed review must never be served from cache")
	}
}

func TestRunRetriesAfterFailure(t *testing.T) {
	dir := t.TempDir()
	rev := &stubReviewer{err: errors.New("transient")}
	r := newRunner(rev)
	if _, err := r.Run(context.Background(), dir, "/wt", testProv()); err == nil {
		t.Fatal("expected first run to fail")
	}
	rev.err = nil
	rev.res = &ClaudeResult{Raw: goodOutput}
	res, err := r.Run(context.Background(), dir, "/wt", testProv())
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if res.FromCache {
		t.Fatal("retry must not reuse the failed artifact")
	}
	if res.Review.Status != artifact.StatusComplete {
		t.Errorf("status = %q", res.Review.Status)
	}
}

func TestRunClassifiesMalformedOutput(t *testing.T) {
	dir := t.TempDir()
	rev := &stubReviewer{res: &ClaudeResult{Raw: "I could not produce JSON, sorry."}}
	if _, err := newRunner(rev).Run(context.Background(), dir, "/wt", testProv()); err == nil {
		t.Fatal("expected malformed output error")
	}
	stored, _ := artifact.ReadReview(dir)
	if stored.Failure == nil || stored.Failure.Kind != "malformed_output" {
		t.Fatalf("failure = %+v", stored.Failure)
	}
	if stored.Failure.Raw == "" {
		t.Error("raw output not preserved for diagnosis")
	}
}

func TestRunClassifiesTimeout(t *testing.T) {
	dir := t.TempDir()
	rev := &stubReviewer{delay: 200 * time.Millisecond, res: &ClaudeResult{Raw: goodOutput}}
	r := newRunner(rev)
	r.Timeout = 10 * time.Millisecond
	if _, err := r.Run(context.Background(), dir, "/wt", testProv()); err == nil {
		t.Fatal("expected timeout")
	}
	stored, _ := artifact.ReadReview(dir)
	if stored.Failure == nil || stored.Failure.Kind != "timeout" {
		t.Fatalf("failure = %+v", stored.Failure)
	}
}

func TestRunClassifiesInterruption(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	rev := &stubReviewer{delay: time.Second, res: &ClaudeResult{Raw: goodOutput}}
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	if _, err := newRunner(rev).Run(ctx, dir, "/wt", testProv()); err == nil {
		t.Fatal("expected interruption")
	}
	stored, _ := artifact.ReadReview(dir)
	if stored.Failure == nil || stored.Failure.Kind != "interrupted" {
		t.Fatalf("failure = %+v", stored.Failure)
	}
	if stored.Usable(testProv()) {
		t.Fatal("interrupted review must not be usable")
	}
}

// An interrupted run leaves a running artifact; it must never be served.
func TestRunningArtifactIsNeverUsable(t *testing.T) {
	dir := t.TempDir()
	p := testProv()
	if err := artifact.WriteRaw(dir, &artifact.Review{
		Status:     artifact.StatusRunning,
		Provenance: p,
		StartedAt:  time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := artifact.ReadReview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Usable(p) {
		t.Fatal("running artifact served as cache")
	}

	rev := &stubReviewer{res: &ClaudeResult{Raw: goodOutput}}
	res, err := newRunner(rev).Run(context.Background(), dir, "/wt", p)
	if err != nil {
		t.Fatal(err)
	}
	if res.FromCache {
		t.Fatal("running artifact treated as a cache hit")
	}
}

// A review with no session id is unresumable, so it must not be recorded as a
// finished one. This stub returns nothing rather than standing in for the CLI,
// which always issues an id.
type idlessReviewer struct{}

func (idlessReviewer) Review(ctx context.Context, dir, prompt string) (*ClaudeResult, error) {
	return &ClaudeResult{Raw: goodOutput, SessionID: ""}, nil
}

func TestRunRejectsMissingSessionID(t *testing.T) {
	dir := t.TempDir()
	if _, err := newRunner(idlessReviewer{}).Run(context.Background(), dir, "/wt", testProv()); err == nil {
		t.Fatal("expected error when no session id is available")
	}
}

func TestParseReviewDerivesStableIDs(t *testing.T) {
	f1, _, _, err := ParseReview(goodOutput)
	if err != nil {
		t.Fatal(err)
	}
	f2, _, _, err := ParseReview(goodOutput)
	if err != nil {
		t.Fatal(err)
	}
	if f1[0].ID != f2[0].ID {
		t.Fatal("finding id is not stable across parses")
	}
	if f1[0].ID != artifact.FindingID("client/fetch.go", "unbounded-retry", "Retry loop has no ceiling") {
		t.Fatal("finding id not derived from path, rule, and message")
	}
}

func TestParseReviewAcceptsFencedJSON(t *testing.T) {
	raw := "Here is the review:\n```json\n" + goodOutput + "\n```\nHope that helps."
	f, _, summary, err := ParseReview(raw)
	if err != nil {
		t.Fatalf("ParseReview: %v", err)
	}
	if len(f) != 1 || summary == "" {
		t.Fatal("fenced JSON not extracted")
	}
}

func TestParseReviewRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"no json":          "there is no object here",
		"bad severity":     `{"findings":[{"severity":"critical","path":"a.go","message":"m"}]}`,
		"missing path":     `{"findings":[{"severity":"error","message":"m"}]}`,
		"truncated object": `{"findings":[{"severity":"error"`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := ParseReview(raw); err == nil {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
}

func TestParseReviewClampsConfidence(t *testing.T) {
	raw := `{"findings":[
	  {"severity":"info","path":"a.go","message":"m","confidence":5},
	  {"severity":"info","path":"b.go","message":"m","confidence":-2}]}`
	f, _, _, err := ParseReview(raw)
	if err != nil {
		t.Fatal(err)
	}
	if f[0].Confidence != 1 || f[1].Confidence != 0 {
		t.Fatalf("confidence not clamped: %v, %v", f[0].Confidence, f[1].Confidence)
	}
}

func TestParseReviewAcceptsEmptyFindings(t *testing.T) {
	f, _, summary, err := ParseReview(`{"summary":"Clean change.","findings":[]}`)
	if err != nil {
		t.Fatalf("ParseReview: %v", err)
	}
	if len(f) != 0 || summary != "Clean change." {
		t.Fatal("empty findings not handled")
	}
}

// The rubric text and its version travel together; a changed rubric must
// invalidate cached reviews.
func TestPromptIsVersionedAndDescribesTheContract(t *testing.T) {
	p := testProv()
	got := Prompt(p)
	for _, want := range []string{"#42", "acme/widgets", "read-only", "findings", "reading_guide", "severity"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if RubricVersion == "" {
		t.Error("rubric version must be set")
	}
}

// The central Phase 6 guarantee: the invocation grants no capability to write
// or execute, and the reviewed repository cannot grant one back.
func TestClaudeInvocationIsReadOnly(t *testing.T) {
	f := xexec.NewFake().RespondOK("claude", `{"result":"{}","session_id":"s-1","model":"claude-opus-5"}`)
	c := &Claude{Runner: f, Model: "claude-opus-5"}
	if _, err := c.Review(context.Background(), "/wt", "review please"); err != nil {
		t.Fatalf("Review: %v", err)
	}
	line := f.CommandLines()[0]

	for _, want := range []string{
		"--allowed-tools Read,Grep,Glob",
		"--setting-sources user",
		"--strict-mcp-config",
		"--permission-mode dontAsk",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("invocation missing %q\ngot: %s", want, line)
		}
	}
	for _, denied := range []string{"Bash", "Write", "Edit", "Task", "WebFetch"} {
		if !strings.Contains(line, denied) {
			t.Errorf("denied tool %q not named in invocation", denied)
		}
	}
	for _, forbidden := range []string{"--dangerously-skip-permissions", "--allow-dangerously-skip-permissions", "bypassPermissions", "acceptEdits"} {
		if strings.Contains(line, forbidden) {
			t.Fatalf("invocation weakens permissions with %q", forbidden)
		}
	}
}

func TestClaudeRunsInsideWorktree(t *testing.T) {
	f := xexec.NewFake().RespondOK("claude", `{"result":"{}","session_id":"s-1"}`)
	c := &Claude{Runner: f}
	if _, err := c.Review(context.Background(), "/worktrees/acme-42", "p"); err != nil {
		t.Fatal(err)
	}
	if f.Calls[0].Dir != "/worktrees/acme-42" {
		t.Fatalf("ran in %q", f.Calls[0].Dir)
	}
}

func TestClaudeReportsMalformedOutput(t *testing.T) {
	f := xexec.NewFake().RespondOK("claude", "not json")
	var me *MalformedError
	_, err := (&Claude{Runner: f}).Review(context.Background(), "/wt", "p")
	if !errors.As(err, &me) {
		t.Fatalf("got %v, want MalformedError", err)
	}
	if me.Raw != "not json" {
		t.Error("raw output not preserved")
	}
}

func TestClaudeReportsErrorPayload(t *testing.T) {
	f := xexec.NewFake().RespondOK("claude", `{"is_error":true,"error":"rate limited","session_id":"s-1"}`)
	if _, err := (&Claude{Runner: f}).Review(context.Background(), "/wt", "p"); err == nil {
		t.Fatal("expected error payload to surface")
	}
}

func TestClaudeRequiresSessionID(t *testing.T) {
	f := xexec.NewFake().RespondOK("claude", `{"result":"{}"}`)
	if _, err := (&Claude{Runner: f}).Review(context.Background(), "/wt", "p"); err == nil {
		t.Fatal("expected error when no session id is returned")
	}
}

func TestClaudePropagatesInvocationFailure(t *testing.T) {
	f := xexec.NewFake().Respond("claude", xexec.Response{Stderr: "command not found", Err: xexec.ErrExit})
	if _, err := (&Claude{Runner: f}).Review(context.Background(), "/wt", "p"); err == nil {
		t.Fatal("expected invocation failure")
	}
}
