package gh

import (
	"context"
	"strings"
	"testing"

	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
)

// Rows as the --jq template produces them, taken from a real response.
const checkRows = "comment\tcompleted\tsuccess\t2026-08-09T02:50:00Z\t2026-08-09T02:51:00Z\thttps://github.com/acme/widgets/actions/runs/31291159827/job/93188864110\n" +
	"ai-explore\tcompleted\tskipped\t2026-08-09T02:50:00Z\t2026-08-09T02:50:05Z\thttps://github.com/acme/widgets/actions/runs/31291159828/job/93188864111\n" +
	"QA Smoke / smoke\tcompleted\tfailure\t2026-08-09T02:54:16Z\t2026-08-09T03:03:37Z\thttps://github.com/acme/widgets/actions/runs/31291159829/job/93188864112\n" +
	"typecheck\tin_progress\t\t2026-08-09T03:00:00Z\t\thttps://github.com/acme/widgets/actions/runs/31291159830/job/93188864113\n"

func TestChecksForParsesRuns(t *testing.T) {
	f := xexec.NewFake()
	f.RespondOK("gh api --paginate repos/acme/widgets/commits/abc123/check-runs", checkRows)

	runs, err := New(f).ChecksFor(context.Background(), "acme/widgets", "abc123")
	if err != nil {
		t.Fatalf("ChecksFor: %v", err)
	}
	if len(runs) != 4 {
		t.Fatalf("expected 4 runs, got %d: %+v", len(runs), runs)
	}

	// A name containing a space and a slash must survive intact, which is why
	// the rows are tab-separated.
	if runs[2].Name != "QA Smoke / smoke" {
		t.Errorf("name = %q, want %q", runs[2].Name, "QA Smoke / smoke")
	}
	if !runs[2].Conclusion.Failed() {
		t.Errorf("failure conclusion should read as failed")
	}
	if runs[2].WorkflowRunID != 31291159829 {
		t.Errorf("workflow run id = %d, want 31291159829", runs[2].WorkflowRunID)
	}
	if runs[3].Conclusion != "" || !runs[3].Running() {
		t.Errorf("in-progress run should have no conclusion and report running: %+v", runs[3])
	}
}

// Skipped is the conclusion a path-filtered job reports. Reading it as a
// failure would paint healthy pull requests red, so it gets its own test.
func TestSkippedIsNotAFailure(t *testing.T) {
	for _, c := range []Conclusion{ConclusionSuccess, ConclusionSkipped, ConclusionNeutral, ""} {
		if c.Failed() {
			t.Errorf("conclusion %q must not read as failed", c)
		}
	}
	for _, c := range []Conclusion{ConclusionFailure, ConclusionTimedOut, ConclusionCancelled} {
		if !c.Failed() {
			t.Errorf("conclusion %q must read as failed", c)
		}
	}
}

// A pull request whose repository runs no CI is normal, not broken.
func TestChecksForHandlesNoChecks(t *testing.T) {
	f := xexec.NewFake()
	f.RespondOK("gh api --paginate", "")

	runs, err := New(f).ChecksFor(context.Background(), "acme/widgets", "abc123")
	if err != nil {
		t.Fatalf("ChecksFor: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("expected no runs, got %v", runs)
	}
}

// A check reported by something other than Actions has no workflow run behind
// it, so there is nothing to re-run and the id must not be invented.
func TestCheckWithoutWorkflowRunHasNoID(t *testing.T) {
	f := xexec.NewFake()
	f.RespondOK("gh api --paginate", "codecov\tcompleted\tsuccess\t\t\thttps://codecov.io/gh/acme/widgets/pull/42\n")

	runs, err := New(f).ChecksFor(context.Background(), "acme/widgets", "abc123")
	if err != nil {
		t.Fatalf("ChecksFor: %v", err)
	}
	if len(runs) != 1 || runs[0].WorkflowRunID != 0 {
		t.Fatalf("expected one run with no workflow id, got %+v", runs)
	}
}

func TestRerunChecksTargetsFailedJobs(t *testing.T) {
	f := xexec.NewFake()
	f.RespondOK("gh run rerun", "")

	if err := New(f).RerunChecks(context.Background(), "acme/widgets", 31291159829); err != nil {
		t.Fatalf("RerunChecks: %v", err)
	}

	line := strings.Join(f.CommandLines(), "\n")
	for _, want := range []string{"gh run rerun", "31291159829", "-R acme/widgets", "--failed"} {
		if !strings.Contains(line, want) {
			t.Errorf("command %q missing %q", line, want)
		}
	}
}

// Without a run id there is nothing to act on, and guessing one would re-run
// somebody else's workflow.
func TestRerunChecksRefusesWithoutRunID(t *testing.T) {
	f := xexec.NewFake()
	if err := New(f).RerunChecks(context.Background(), "acme/widgets", 0); err == nil {
		t.Fatal("expected an error for a missing run id")
	}
	if len(f.CommandLines()) != 0 {
		t.Fatalf("nothing should have been run: %v", f.CommandLines())
	}
}

// Re-running failed jobs is the one write this client may issue. Everything
// that publishes review content, changes the pull request, or speaks for the
// reviewer stays forbidden -- if this test starts failing, the safety model
// regressed and the code is wrong, not the test.
func TestChecksIssueOnlyTheOnePermittedWrite(t *testing.T) {
	f := xexec.NewFake()
	f.RespondOK("gh api --paginate", checkRows)
	f.RespondOK("gh run rerun", "")

	c := New(f)
	ctx := context.Background()
	_, _ = c.ChecksFor(ctx, "acme/widgets", "abc123")
	_ = c.RerunChecks(ctx, "acme/widgets", 31291159829)

	for _, line := range f.CommandLines() {
		for _, forbidden := range []string{
			"pr comment", "pr review", "pr merge", "pr close", "pr edit",
			"-X PATCH", "-X DELETE", "pr ready", "release", "workflow disable",
		} {
			if strings.Contains(line, forbidden) {
				t.Fatalf("checks issued a forbidden write: %q", line)
			}
		}
		// POST is permitted only as the re-run, never as anything else.
		if strings.Contains(line, "-X POST") {
			t.Fatalf("checks issued a raw POST: %q", line)
		}
	}
}

// Reading checks must stay read-only even though its file also holds a write.
func TestChecksForIssuesNoWrites(t *testing.T) {
	f := xexec.NewFake()
	f.RespondOK("gh api --paginate", checkRows)

	_, _ = New(f).ChecksFor(context.Background(), "acme/widgets", "abc123")

	for _, line := range f.CommandLines() {
		for _, forbidden := range []string{"run rerun", "-X POST", "-X PATCH", "-X DELETE", "pr "} {
			if strings.Contains(line, forbidden) {
				t.Fatalf("reading checks issued a write: %q", line)
			}
		}
	}
}
