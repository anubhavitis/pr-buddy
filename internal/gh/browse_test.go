package gh

import (
	"context"
	"strings"
	"testing"

	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
)

func TestOrgsIncludesViewerFirst(t *testing.T) {
	f := xexec.NewFake()
	f.RespondOK("gh api user --jq", "anubhavitis\n")
	f.RespondOK("gh api user/orgs", "Outcome-xyz\nsome-other-org\n")

	orgs, err := New(f).Orgs(context.Background())
	if err != nil {
		t.Fatalf("Orgs: %v", err)
	}
	if len(orgs) != 3 {
		t.Fatalf("got %d orgs, want 3", len(orgs))
	}
	if orgs[0].Login != "anubhavitis" || !orgs[0].IsViewer {
		t.Errorf("viewer not first: %+v", orgs[0])
	}
	if orgs[1].Login != "Outcome-xyz" {
		t.Errorf("org list = %+v", orgs)
	}
}

// GitHub reports no activity timestamp for an organization, and deriving one
// would cost a request per org. Alphabetical is the cheap predictable order;
// the viewer stays pinned first regardless.
func TestOrgsSortsAlphabeticallyAfterViewer(t *testing.T) {
	f := xexec.NewFake()
	f.RespondOK("gh api user --jq", "anubhavitis\n")
	f.RespondOK("gh api user/orgs", "zulu\nOutcome-xyz\nalpha\n")

	orgs, err := New(f).Orgs(context.Background())
	if err != nil {
		t.Fatalf("Orgs: %v", err)
	}
	got := []string{orgs[0].Login, orgs[1].Login, orgs[2].Login, orgs[3].Login}
	want := []string{"anubhavitis", "alpha", "Outcome-xyz", "zulu"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("orgs = %v, want %v (viewer first, then case-insensitive A-Z)", got, want)
		}
	}
}

// Org membership is not readable under every token scope; the viewer's own
// account must still be usable.
func TestOrgsDegradesWhenMembershipUnreadable(t *testing.T) {
	f := xexec.NewFake()
	f.RespondOK("gh api user --jq", "anubhavitis\n")
	f.Respond("gh api user/orgs", xexec.Response{Stderr: "403 Forbidden", Err: xexec.ErrExit})

	orgs, err := New(f).Orgs(context.Background())
	if err != nil {
		t.Fatalf("Orgs should degrade, not fail: %v", err)
	}
	if len(orgs) != 1 || !orgs[0].IsViewer {
		t.Fatalf("expected just the viewer, got %+v", orgs)
	}
}

func TestOrgsFailsWithoutAuthenticatedUser(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh api user --jq", "")
	if _, err := New(f).Orgs(context.Background()); err == nil {
		t.Fatal("expected an error when no user is authenticated")
	}
}

// Repositories are ranked by activity, not name: the reviewer is looking for
// what moved recently, and an alphabetical list buries it.
func TestReposSortsByMostRecentPush(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh repo list", `[
	  {"name":"apple","nameWithOwner":"acme/apple","isPrivate":true,"pushedAt":"2023-01-05T00:00:00Z"},
	  {"name":"zebra","nameWithOwner":"acme/zebra","isPrivate":false,"pushedAt":"2026-08-01T00:00:00Z"},
	  {"name":"mango","nameWithOwner":"acme/mango","isPrivate":false,"pushedAt":"2026-02-14T00:00:00Z"}]`)

	repos, err := New(f).Repos(context.Background(), "acme", 0)
	if err != nil {
		t.Fatalf("Repos: %v", err)
	}
	if len(repos) != 3 {
		t.Fatalf("got %d repos", len(repos))
	}
	got := []string{repos[0].Name, repos[1].Name, repos[2].Name}
	want := []string{"zebra", "mango", "apple"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("repos = %v, want %v (most recently pushed first)", got, want)
		}
	}
	if repos[2].Name != "apple" || !repos[2].Private {
		t.Error("private flag lost")
	}
	if repos[0].PushedAt != "2026-08-01T00:00:00Z" {
		t.Errorf("pushedAt not surfaced: %+v", repos[0])
	}
}

// A repository that has never been pushed to reports an empty timestamp; it
// must sort last rather than jumping ahead of real activity.
func TestReposSortsUnpushedLast(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh repo list", `[
	  {"name":"empty","nameWithOwner":"acme/empty","pushedAt":""},
	  {"name":"live","nameWithOwner":"acme/live","pushedAt":"2024-03-01T00:00:00Z"}]`)

	repos, err := New(f).Repos(context.Background(), "acme", 0)
	if err != nil {
		t.Fatalf("Repos: %v", err)
	}
	if repos[0].Name != "live" || repos[1].Name != "empty" {
		t.Fatalf("unpushed repo not last: %+v", repos)
	}
}

// Equal timestamps must not produce a list that reshuffles between calls.
func TestReposBreaksTiesByName(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh repo list", `[
	  {"name":"beta","nameWithOwner":"acme/beta","pushedAt":"2025-01-01T00:00:00Z"},
	  {"name":"alpha","nameWithOwner":"acme/alpha","pushedAt":"2025-01-01T00:00:00Z"}]`)

	repos, err := New(f).Repos(context.Background(), "acme", 0)
	if err != nil {
		t.Fatalf("Repos: %v", err)
	}
	if repos[0].Name != "alpha" || repos[1].Name != "beta" {
		t.Fatalf("ties not broken by name: %+v", repos)
	}
}

func TestReposRequestsPushedAt(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh repo list", "[]")
	if _, err := New(f).Repos(context.Background(), "acme", 0); err != nil {
		t.Fatal(err)
	}
	if !f.Ran("pushedAt") {
		t.Fatalf("pushedAt not requested from gh: %v", f.CommandLines())
	}
}

func TestReposExcludesArchived(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh repo list", "[]")
	if _, err := New(f).Repos(context.Background(), "acme", 0); err != nil {
		t.Fatal(err)
	}
	if !f.Ran("--no-archived") {
		t.Fatalf("archived repositories not excluded: %v", f.CommandLines())
	}
}

func TestPRsParsesListing(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh pr list", `[
	  {"number":2214,"title":"fix(deposit): evict stale wallet","state":"OPEN","isDraft":false,
	   "author":{"login":"anubhavitis"},"baseRefName":"dev","changedFiles":2,
	   "additions":83,"deletions":1,"updatedAt":"2026-08-01T10:00:00Z",
	   "url":"https://github.com/acme/widgets/pull/2214"}]`)

	prs, err := New(f).PRs(context.Background(), "acme/widgets", 0)
	if err != nil {
		t.Fatalf("PRs: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("got %d prs", len(prs))
	}
	p := prs[0]
	if p.Number != 2214 || p.Author != "anubhavitis" || p.BaseRef != "dev" {
		t.Errorf("listing fields lost: %+v", p)
	}
	if p.ChangedFiles != 2 || p.Additions != 83 {
		t.Errorf("size fields lost: %+v", p)
	}
	if p.State != StateOpen {
		t.Errorf("state = %q", p.State)
	}
}

func TestPRsHandlesMissingAuthor(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh pr list", `[{"number":1,"title":"t","state":"OPEN"}]`)
	prs, err := New(f).PRs(context.Background(), "acme/widgets", 0)
	if err != nil {
		t.Fatal(err)
	}
	if prs[0].Author != "" {
		t.Errorf("author = %q, want empty", prs[0].Author)
	}
}

func TestPRsRequestsOnlyOpen(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh pr list", "[]")
	if _, err := New(f).PRs(context.Background(), "acme/widgets", 0); err != nil {
		t.Fatal(err)
	}
	if !f.Ran("--state open") {
		t.Fatalf("did not restrict to open PRs: %v", f.CommandLines())
	}
}

func TestChangedFilesReturnsPaths(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh api --paginate", "a/one.ts\nb/two.go\n")
	files, err := New(f).ChangedFiles(context.Background(), "acme/widgets", 42)
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if len(files) != 2 || files[0] != "a/one.ts" {
		t.Fatalf("files = %v", files)
	}
}

func TestChangedFilesHandlesEmpty(t *testing.T) {
	f := xexec.NewFake().RespondOK("gh api --paginate", "")
	files, err := New(f).ChangedFiles(context.Background(), "acme/widgets", 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("expected no files, got %v", files)
	}
}

// Browsing must stay as read-only as the rest of the client.
func TestBrowseIssuesNoWriteCommands(t *testing.T) {
	f := xexec.NewFake()
	f.RespondOK("gh api user --jq", "anubhavitis\n")
	f.RespondOK("gh api user/orgs", "acme\n")
	f.RespondOK("gh repo list", "[]")
	f.RespondOK("gh pr list", "[]")
	f.RespondOK("gh api --paginate", "")

	c := New(f)
	ctx := context.Background()
	_, _ = c.Orgs(ctx)
	_, _ = c.Repos(ctx, "acme", 0)
	_, _ = c.PRs(ctx, "acme/widgets", 0)
	_, _ = c.ChangedFiles(ctx, "acme/widgets", 42)

	for _, line := range f.CommandLines() {
		for _, forbidden := range []string{"pr comment", "pr review", "pr merge", "pr close", "pr edit", "-X POST", "-X PATCH", "-X DELETE"} {
			if strings.Contains(line, forbidden) {
				t.Fatalf("browse issued a write command: %q", line)
			}
		}
	}
}
