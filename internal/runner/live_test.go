package runner

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anubhavitis/pr-buddy/internal/artifact"
	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
)

// TestLiveReviewAgainstRealCLI exercises the one seam every other test mocks:
// the actual `claude` binary. It is skipped unless PR_BUDDY_LIVE=1, so the
// normal suite still shells out to nothing.
//
// It exists because the session and model defects this file's fixes address
// were both invisible to a fake -- they were disagreements with the real CLI's
// behaviour, not with our model of it.
func TestLiveReviewAgainstRealCLI(t *testing.T) {
	if os.Getenv("PR_BUDDY_LIVE") != "1" {
		t.Skip("set PR_BUDDY_LIVE=1 to run against the real claude binary")
	}

	worktree := t.TempDir()
	if err := os.WriteFile(worktree+"/math.go",
		[]byte("package main\n\n// Div divides a by b.\nfunc Div(a, b int) int {\n\treturn a / b\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifactDir := t.TempDir()

	prov := artifact.Provenance{
		Repo: "local/live", PRNumber: 1,
		BaseSHA: "a000000000000000000000000000000000000000",
		HeadSHA: "b000000000000000000000000000000000000000",
		RubricVersion: RubricVersion, Model: "claude-opus-5",
		SchemaVersion: artifact.Version,
	}
	r := &Runner{
		Reviewer: &Claude{Runner: xexec.Real{}, Model: "claude-opus-5"},
		Timeout:  10 * time.Minute,
	}

	res, err := r.Run(context.Background(), artifactDir, worktree, prov)
	if err != nil {
		t.Fatalf("live review failed: %v", err)
	}
	if res.Review.Status != artifact.StatusComplete {
		t.Fatalf("status = %q", res.Review.Status)
	}
	if res.StaleReason != "" {
		t.Errorf("first live run reported stale reason %q", res.StaleReason)
	}

	sess, err := artifact.ReadSession(artifactDir)
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if sess.SessionID == "" {
		t.Error("no session id recorded; the review is unresumable")
	}
	if sess.Model == "" {
		t.Error("no model recorded; session.json is useless for diagnosis")
	}
	if !strings.Contains(sess.ResumeCommand, sess.SessionID) {
		t.Errorf("resume command %q does not name the session", sess.ResumeCommand)
	}
	t.Logf("session id=%s model=%q resume=%q findings=%d",
		sess.SessionID, sess.Model, sess.ResumeCommand, len(res.Review.Findings))

	// The recorded id must actually be resumable -- the defect that motivated
	// dropping --session-id was an id that could never be reused.
	out, err := xexec.Real{}.Run(context.Background(), worktree, "claude",
		"--print", "--output-format", "json", "--resume", sess.SessionID,
		"Reply with the single word RESUMED.")
	if err != nil {
		t.Fatalf("recorded session id is not resumable: %v", err)
	}
	if !strings.Contains(out, "RESUMED") {
		t.Errorf("resume did not reattach to the conversation; got: %s", truncate(out, 300))
	}
}
