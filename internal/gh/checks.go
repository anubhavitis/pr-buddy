package gh

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// CI check runs for a pull request head.
//
// This file holds the one exception to the client's read-only rule. Everything
// else here reports what GitHub already decided; RerunChecks asks it to do
// something. The two live in the same file so the exception is visible next to
// the reads it accompanies rather than lost among them, and the guard test
// pins it as the only write the client is allowed to issue.

// Conclusion is how a finished check run turned out. Empty while it is still
// running.
//
// The vocabulary is GitHub's, not ours, and the distinction that matters is
// that "skipped" and "neutral" are not failures: a workflow that opts out of a
// job on a path filter reports skipped, and treating that as broken would paint
// a healthy pull request red.
type Conclusion string

const (
	ConclusionSuccess   Conclusion = "success"
	ConclusionFailure   Conclusion = "failure"
	ConclusionCancelled Conclusion = "cancelled"
	ConclusionSkipped   Conclusion = "skipped"
	ConclusionNeutral   Conclusion = "neutral"
	ConclusionTimedOut  Conclusion = "timed_out"
)

// Failed reports whether a conclusion should be read as the check having gone
// wrong, as opposed to having passed, been deliberately skipped, or not having
// finished.
func (c Conclusion) Failed() bool {
	switch c {
	case ConclusionFailure, ConclusionTimedOut, ConclusionCancelled:
		return true
	}
	// action_required and stale are rare and mean "a human must look", which is
	// closer to failure than to success for a reviewer reading this panel.
	return c == "action_required" || c == "stale"
}

// CheckRun is one CI job reported against a commit.
type CheckRun struct {
	Name string `json:"name"`
	// Status is queued, in_progress, or completed.
	Status string `json:"status"`
	// Conclusion is empty until Status is completed.
	Conclusion Conclusion `json:"conclusion"`
	StartedAt  string     `json:"started_at"`
	// CompletedAt is empty while the run is still going.
	CompletedAt string `json:"completed_at"`
	// URL is the job's page on GitHub, which is also where the log lives.
	URL string `json:"url"`
	// WorkflowRunID identifies the workflow run this job belongs to, which is
	// what a re-run acts on. Zero when it could not be determined.
	WorkflowRunID int64 `json:"workflow_run_id"`
}

// Running reports whether the check has not finished yet.
func (c CheckRun) Running() bool { return c.Status != "completed" }

// ChecksFor lists the CI check runs reported against a commit.
//
// Empty is a legitimate answer, not an error: plenty of pull requests run no
// CI at all, and a repository with no workflows must not look broken.
func (c *Client) ChecksFor(ctx context.Context, repo, sha string) ([]CheckRun, error) {
	// --jq flattens the response to one line per run. The endpoint wraps its
	// array in an object, and --paginate concatenates whole objects per page,
	// so unmarshalling the combined output directly would fail on a second page.
	//
	// Tab-separated because a check run's name routinely contains spaces
	// ("QA Smoke / smoke") but never a tab.
	out, err := c.Runner.Run(ctx, "", "gh", "api", "--paginate",
		fmt.Sprintf("repos/%s/commits/%s/check-runs", repo, sha),
		"--jq", `.check_runs[] | [.name, .status, (.conclusion // ""), (.started_at // ""), (.completed_at // ""), (.html_url // "")] | @tsv`)
	if err != nil {
		return nil, fmt.Errorf("listing checks for %s@%s: %w", repo, shortSHA(sha), err)
	}

	var runs []CheckRun
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 6 {
			// A malformed row is skipped rather than failing the whole list: one
			// unparseable check must not hide the others.
			continue
		}
		runs = append(runs, CheckRun{
			Name:          f[0],
			Status:        f[1],
			Conclusion:    Conclusion(f[2]),
			StartedAt:     f[3],
			CompletedAt:   f[4],
			URL:           f[5],
			WorkflowRunID: workflowRunID(f[5]),
		})
	}
	return runs, nil
}

// RerunChecks re-runs the failed jobs of a workflow run.
//
// This is the only call in pr-buddy that changes anything on GitHub, and it is
// deliberately the narrowest one that does the job: it re-runs jobs the author
// already configured, on their own workflow, and cannot publish review content,
// comment, approve, or merge. Nothing derived from the model reaches it -- the
// run id comes from a check GitHub itself reported.
//
// It is still a write. It spends the repository's CI minutes and can set off
// anything the workflow does on completion, so callers must confirm with the
// reviewer first rather than firing it from a click.
func (c *Client) RerunChecks(ctx context.Context, repo string, runID int64) error {
	if runID <= 0 {
		return fmt.Errorf("re-running checks for %s: no workflow run to re-run", repo)
	}
	// -R rather than a directory: the worktree is a detached checkout with no
	// push remote, so repo context has to be named explicitly.
	if _, err := c.Runner.Run(ctx, "", "gh", "run", "rerun",
		strconv.FormatInt(runID, 10), "-R", repo, "--failed"); err != nil {
		return fmt.Errorf("re-running failed jobs of %s run %d: %w", repo, runID, err)
	}
	return nil
}

// workflowRunID pulls the workflow run out of a check run's job URL, which has
// the form .../actions/runs/<run>/job/<job>. Zero when the URL is not one --
// checks reported by apps other than Actions have no run to re-run.
func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func workflowRunID(jobURL string) int64 {
	_, after, found := strings.Cut(jobURL, "/actions/runs/")
	if !found {
		return 0
	}
	digits, _, _ := strings.Cut(after, "/")
	id, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0
	}
	return id
}
