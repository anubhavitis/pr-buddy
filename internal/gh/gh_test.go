package gh

import (
	"context"
	"errors"
	"strings"
	"testing"

	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
)

const samePR = `{
  "number": 42,
  "title": "Fix retry backoff",
  "state": "OPEN",
  "baseRefName": "main",
  "baseRefOid": "1111111111111111111111111111111111111111",
  "headRefName": "fix-backoff",
  "headRefOid": "2222222222222222222222222222222222222222",
  "isCrossRepository": false,
  "headRepository": {"name": "widgets", "owner": {"login": "acme"}},
  "headRepositoryOwner": {"login": "acme"}
}`

const forkPR = `{
  "number": 7,
  "title": "Community patch",
  "state": "OPEN",
  "baseRefName": "develop",
  "baseRefOid": "3333333333333333333333333333333333333333",
  "headRefName": "patch-1",
  "headRefOid": "4444444444444444444444444444444444444444",
  "isCrossRepository": true,
  "headRepository": {"name": "widgets", "owner": {"login": "outsider"}},
  "headRepositoryOwner": {"login": "outsider"}
}`

func TestViewPRReadsBaseAndHeadFromGitHub(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh pr view 42", samePR)
	pr, err := New(f).ViewPR(context.Background(), "/repo", "acme/widgets", 42)
	if err != nil {
		t.Fatalf("ViewPR: %v", err)
	}
	if pr.BaseSHA != "1111111111111111111111111111111111111111" {
		t.Errorf("base sha = %q", pr.BaseSHA)
	}
	if pr.HeadSHA != "2222222222222222222222222222222222222222" {
		t.Errorf("head sha = %q", pr.HeadSHA)
	}
	if pr.BaseRef != "main" || pr.HeadRef != "fix-backoff" {
		t.Errorf("refs = %q / %q", pr.BaseRef, pr.HeadRef)
	}
	if pr.State != StateOpen {
		t.Errorf("state = %q", pr.State)
	}
	if pr.IsFork {
		t.Error("same-repo PR reported as fork")
	}
}

// The plan requires reviewing the revision GitHub reports, so the repo must be
// passed explicitly rather than inferred from whatever is checked out.
func TestViewPRPassesRepoExplicitly(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh pr view", samePR)
	if _, err := New(f).ViewPR(context.Background(), "/repo", "acme/widgets", 42); err != nil {
		t.Fatal(err)
	}
	if !f.Ran("--repo acme/widgets") {
		t.Fatalf("expected explicit --repo, got %v", f.CommandLines())
	}
}

func TestViewPRHandlesForks(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh pr view 7", forkPR)
	pr, err := New(f).ViewPR(context.Background(), "/repo", "acme/widgets", 7)
	if err != nil {
		t.Fatalf("ViewPR: %v", err)
	}
	if !pr.IsFork {
		t.Error("cross-repository PR not marked as fork")
	}
	if pr.HeadRepo != "outsider/widgets" {
		t.Errorf("head repo = %q, want outsider/widgets", pr.HeadRepo)
	}
	if pr.BaseRef != "develop" {
		t.Errorf("non-main base branch lost: %q", pr.BaseRef)
	}
}

func TestViewPRReportsClosedAndMergedStates(t *testing.T) {
	cases := map[string]State{
		`{"number":1,"state":"CLOSED","baseRefOid":"a","headRefOid":"b"}`: StateClosed,
		`{"number":1,"state":"MERGED","baseRefOid":"a","headRefOid":"b"}`: StateMerged,
	}
	for body, want := range cases {
		f := xexec.NewFake().RespondOK("gh pr view", body)
		pr, err := New(f).ViewPR(context.Background(), "/repo", "acme/widgets", 1)
		if err != nil {
			t.Fatalf("ViewPR: %v", err)
		}
		if pr.State != want {
			t.Errorf("state = %q, want %q", pr.State, want)
		}
	}
}

func TestViewPRNotFound(t *testing.T) {
	f := xexec.NewFake().Respond("gh pr view", xexec.Response{
		Stderr: "GraphQL: Could not resolve to a PullRequest with the number of 999.",
		Err:    xexec.ErrExit,
	})
	_, err := New(f).ViewPR(context.Background(), "/repo", "acme/widgets", 999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// A PR without revisions must be a hard error: reviewing an unknown revision is
// worse than not reviewing.
func TestViewPRRejectsMissingRevisions(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh pr view", `{"number":42,"state":"OPEN"}`)
	if _, err := New(f).ViewPR(context.Background(), "/repo", "acme/widgets", 42); err == nil {
		t.Fatal("expected error when gh returns no base/head revision")
	}
}

func TestViewPRRejectsMalformedJSON(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh pr view", "not json at all")
	if _, err := New(f).ViewPR(context.Background(), "/repo", "acme/widgets", 42); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestCurrentRepo(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh repo view", `{"nameWithOwner":"acme/widgets"}`)
	repo, err := New(f).CurrentRepo(context.Background(), "/repo")
	if err != nil {
		t.Fatalf("CurrentRepo: %v", err)
	}
	if repo != "acme/widgets" {
		t.Fatalf("repo = %q", repo)
	}
}

func TestCurrentRepoRejectsEmpty(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh repo view", `{"nameWithOwner":""}`)
	if _, err := New(f).CurrentRepo(context.Background(), "/repo"); err == nil {
		t.Fatal("expected error for empty repository name")
	}
}

// Nothing in this package may mutate the pull request.
func TestClientNeverWritesToGitHub(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh", samePR)
	c := New(f)
	ctx := context.Background()
	_, _ = c.ViewPR(ctx, "/repo", "acme/widgets", 42)
	f.Respond("gh repo view", xexec.Response{Stdout: `{"nameWithOwner":"acme/widgets"}`})
	_, _ = c.CurrentRepo(ctx, "/repo")

	for _, line := range f.CommandLines() {
		for _, forbidden := range []string{"pr comment", "pr review", "pr merge", "pr close", "pr edit"} {
			if strings.Contains(line, forbidden) {
				t.Fatalf("read-only client issued a write command: %q", line)
			}
		}
	}
}
