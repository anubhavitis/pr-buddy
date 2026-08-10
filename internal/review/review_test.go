package review

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anubhavitis/pr-buddy/internal/artifact"
	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
	"github.com/anubhavitis/pr-buddy/internal/gh"
)

// fakeMeta stands in for GitHub. The whole reason Metadata is an interface is
// so the workflow can be exercised without an account or a network.
type fakeMeta struct {
	pr *gh.PR
	// current is what CurrentRepo reports for Cwd.
	current string
	// currentErr makes CurrentRepo fail, as it does outside any checkout.
	currentErr error
	viewCalls  []string
}

func (f *fakeMeta) ViewPR(_ context.Context, _, repo string, number int) (*gh.PR, error) {
	f.viewCalls = append(f.viewCalls, repo)
	pr := *f.pr
	pr.Repo = repo
	pr.Number = number
	return &pr, nil
}

func (f *fakeMeta) CurrentRepo(context.Context, string) (string, error) {
	if f.currentErr != nil {
		return "", f.currentErr
	}
	return f.current, nil
}

func testPR() *gh.PR {
	return &gh.PR{
		Number: 42, Repo: "acme/widgets", Title: "Add a thing",
		State: gh.StateOpen, BaseRef: "main",
		BaseSHA: strings.Repeat("b", 40), HeadSHA: strings.Repeat("h", 40),
	}
}

func newTestService(t *testing.T, meta *fakeMeta, r xexec.Runner) *Service {
	t.Helper()
	return &Service{Runner: r, Meta: meta, Root: t.TempDir(), Cwd: t.TempDir()}
}

// The defect this package exists to fix: the human entry point passed the
// current directory straight to the worktree manager while the JSON commands
// resolved a mirror, so `-repo` naming a repository other than the current one
// fetched from the wrong place. One workflow means one answer.
func TestSourceRepoIsResolvedTheSameWayForAnyRequestedRepo(t *testing.T) {
	fake := &xexec.Fake{}
	// The current directory is a checkout of a *different* repository.
	meta := &fakeMeta{pr: testPR(), current: "other/thing"}
	svc := newTestService(t, meta, fake)

	if _, err := svc.sourceRepoDir(context.Background(), "acme/widgets"); err != nil {
		t.Fatalf("sourceRepoDir: %v", err)
	}

	// It must clone a mirror rather than reuse the unrelated current checkout.
	var cloned bool
	for _, c := range fake.Calls {
		if c.Name == "git" && len(c.Args) > 1 && c.Args[0] == "clone" {
			cloned = true
			if c.Dir == svc.Cwd {
				t.Errorf("cloned into the current checkout %q", c.Dir)
			}
		}
	}
	if !cloned {
		t.Fatalf("expected a bare mirror clone for a repo that is not the current one; calls: %+v", fake.Calls)
	}
}

func TestSourceRepoUsesTheCurrentCheckoutWhenItIsTheRequestedRepo(t *testing.T) {
	fake := &xexec.Fake{}
	meta := &fakeMeta{pr: testPR(), current: "acme/widgets"}
	svc := newTestService(t, meta, fake)

	dir, err := svc.sourceRepoDir(context.Background(), "acme/widgets")
	if err != nil {
		t.Fatalf("sourceRepoDir: %v", err)
	}
	if dir != svc.Cwd {
		t.Errorf("source dir = %q, want the current checkout %q", dir, svc.Cwd)
	}
	for _, c := range fake.Calls {
		if c.Name == "git" && len(c.Args) > 0 && c.Args[0] == "clone" {
			t.Errorf("cloned a mirror despite already being in the repository: %+v", c)
		}
	}
}

func TestResolveRepoPrefersTheNamedRepository(t *testing.T) {
	meta := &fakeMeta{pr: testPR(), current: "other/thing"}
	svc := newTestService(t, meta, &xexec.Fake{})

	got, err := svc.ResolveRepo(context.Background(), "acme/widgets")
	if err != nil {
		t.Fatalf("ResolveRepo: %v", err)
	}
	if got != "acme/widgets" {
		t.Errorf("ResolveRepo = %q, want the named repository", got)
	}
}

func TestResolveRepoFallsBackToTheCurrentRepository(t *testing.T) {
	meta := &fakeMeta{pr: testPR(), current: "acme/widgets"}
	svc := newTestService(t, meta, &xexec.Fake{})

	got, err := svc.ResolveRepo(context.Background(), "")
	if err != nil {
		t.Fatalf("ResolveRepo: %v", err)
	}
	if got != "acme/widgets" {
		t.Errorf("ResolveRepo = %q, want the current repository", got)
	}
}

// Provenance is the cache identity. If it were assembled differently by
// different callers, two entry points could disagree about whether a stored
// review still applies.
func TestProvenanceCarriesEverythingCacheValidityDependsOn(t *testing.T) {
	svc := newTestService(t, &fakeMeta{pr: testPR()}, &xexec.Fake{})
	pr := testPR()

	prov := svc.provenance(pr, "some-model")

	if prov.Repo != pr.Repo || prov.PRNumber != pr.Number {
		t.Errorf("provenance lost repo identity: %+v", prov)
	}
	if prov.BaseSHA != pr.BaseSHA || prov.HeadSHA != pr.HeadSHA {
		t.Errorf("provenance lost revisions: %+v", prov)
	}
	if prov.Model != "some-model" {
		t.Errorf("model = %q, want the requested model", prov.Model)
	}
	if prov.RubricVersion == "" || prov.SchemaVersion == 0 {
		t.Errorf("provenance missing rubric or schema version: %+v", prov)
	}
}

func TestProvenanceDefaultsTheModelSoCacheKeysAreNeverAmbiguous(t *testing.T) {
	svc := newTestService(t, &fakeMeta{pr: testPR()}, &xexec.Fake{})

	prov := svc.provenance(testPR(), "")

	if prov.Model != DefaultModel {
		t.Errorf("model = %q, want %q", prov.Model, DefaultModel)
	}
}

// Storage paths must separate repositories that share a pull request number,
// or PR 42 in two repositories would overwrite each other's reviews.
func TestArtifactDirSeparatesRepositoriesSharingAPullRequestNumber(t *testing.T) {
	svc := newTestService(t, &fakeMeta{pr: testPR()}, &xexec.Fake{})

	a := svc.ArtifactDir("acme/widgets", 42)
	b := svc.ArtifactDir("other/widgets", 42)

	if a == b {
		t.Fatalf("two repositories share an artifact dir: %q", a)
	}
	for _, dir := range []string{a, b} {
		if !strings.HasPrefix(dir, svc.Root) {
			t.Errorf("artifact dir %q escapes the review root %q", dir, svc.Root)
		}
	}
}

func TestStoredStatusReportsAbsentWhenNothingHasBeenReviewed(t *testing.T) {
	dir := t.TempDir()
	prov := artifact.Provenance{
		Repo: "acme/widgets", PRNumber: 42,
		BaseSHA: "b", HeadSHA: "h",
		RubricVersion: "r", Model: "m", SchemaVersion: artifact.Version,
	}

	status, reason := storedStatus(dir, prov)

	if status != "absent" {
		t.Errorf("status = %q, want absent", status)
	}
	if reason != "" {
		t.Errorf("stale reason = %q, want empty", reason)
	}
}

func TestStoredStatusReportsStaleWhenTheHeadMoved(t *testing.T) {
	dir := t.TempDir()
	prov := artifact.Provenance{
		Repo: "acme/widgets", PRNumber: 42,
		BaseSHA: strings.Repeat("b", 40), HeadSHA: strings.Repeat("h", 40),
		RubricVersion: "r", Model: "m", SchemaVersion: artifact.Version,
	}
	stored := &artifact.Review{
		Status: artifact.StatusComplete, Provenance: prov,
		EndedAt: time.Unix(1700000000, 0).UTC(),
	}
	if err := artifact.WriteReview(dir, stored); err != nil {
		t.Fatalf("WriteReview: %v", err)
	}

	moved := prov
	moved.HeadSHA = strings.Repeat("c", 40)

	status, reason := storedStatus(dir, moved)

	if status != "stale" {
		t.Fatalf("status = %q, want stale", status)
	}
	if reason == "" {
		t.Error("a stale review must explain why")
	}
}

func TestStoredStatusReportsCompleteForACurrentReview(t *testing.T) {
	dir := t.TempDir()
	prov := artifact.Provenance{
		Repo: "acme/widgets", PRNumber: 42,
		BaseSHA: strings.Repeat("b", 40), HeadSHA: strings.Repeat("h", 40),
		RubricVersion: "r", Model: "m", SchemaVersion: artifact.Version,
	}
	if err := artifact.WriteReview(dir, &artifact.Review{
		Status: artifact.StatusComplete, Provenance: prov, EndedAt: time.Unix(1700000000, 0).UTC(),
	}); err != nil {
		t.Fatalf("WriteReview: %v", err)
	}

	status, reason := storedStatus(dir, prov)

	if status != string(artifact.StatusComplete) {
		t.Errorf("status = %q, want complete", status)
	}
	if reason != "" {
		t.Errorf("a current review must not be stale: %q", reason)
	}
}

// A discarded review must take its session with it: a resume command pointing
// at a conversation whose artifact is gone is worse than none.
func TestDiscardCachedRemovesTheReviewAndItsSessionTogether(t *testing.T) {
	dir := t.TempDir()
	prov := artifact.Provenance{
		Repo: "acme/widgets", PRNumber: 42,
		BaseSHA: strings.Repeat("b", 40), HeadSHA: strings.Repeat("h", 40),
		RubricVersion: "r", Model: "m", SchemaVersion: artifact.Version,
	}
	if err := artifact.WriteReview(dir, &artifact.Review{
		Status: artifact.StatusComplete, Provenance: prov, EndedAt: time.Unix(1700000000, 0).UTC(),
	}); err != nil {
		t.Fatalf("WriteReview: %v", err)
	}
	if err := artifact.WriteSession(dir, &artifact.Session{
		SessionID: "abc", CacheKey: prov.CacheKey(),
	}); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}

	DiscardCached(dir)

	if _, err := artifact.ReadReview(dir); err == nil {
		t.Error("review survived a discard")
	}
	if _, err := artifact.ReadSession(dir); err == nil {
		t.Error("session survived a discard, and would advertise a dead resume command")
	}
}

func TestPreparedReportsArtifactPathsInsideItsArtifactDir(t *testing.T) {
	p := &Prepared{ArtifactDir: filepath.Join("root", ".reviews", "acme-widgets-pr-42")}

	if got, want := p.ReviewJSON(), filepath.Join(p.ArtifactDir, "review.json"); got != want {
		t.Errorf("ReviewJSON = %q, want %q", got, want)
	}
	if got, want := p.ReviewMD(), filepath.Join(p.ArtifactDir, "review.md"); got != want {
		t.Errorf("ReviewMD = %q, want %q", got, want)
	}
}
