package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
	"github.com/anubhavitis/pr-buddy/internal/gh"
)

const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func testPR() *gh.PR {
	return &gh.PR{
		Number:  42,
		Repo:    "acme/widgets",
		State:   gh.StateOpen,
		BaseRef: "main",
		BaseSHA: "1111111111111111111111111111111111111111",
		HeadRef: "fix-backoff",
		HeadSHA: headA,
	}
}

// fixture wires a Manager to a fake runner with sane defaults.
type fixture struct {
	t   *testing.T
	f   *xexec.Fake
	m   *Manager
	src string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := xexec.NewFake()
	f.RespondOK("git fetch", "")
	f.RespondOK("git worktree list", "")
	f.RespondOK("git worktree add", "")
	f.RespondOK("git status --porcelain", "")
	f.RespondOK("git rev-parse HEAD", headA+"\n")
	return &fixture{t: t, f: f, m: New(f, t.TempDir()), src: t.TempDir()}
}

// registerWorktree makes the fake report an existing worktree at path and
// creates the directory, since existence is confirmed on disk too.
func (fx *fixture) registerWorktree(path string) {
	fx.t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		fx.t.Fatal(err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		fx.t.Fatal(err)
	}
	fx.f.RespondOK("git worktree list", "worktree "+abs+"\nHEAD "+headA+"\ndetached\n\n")
}

func TestDirNameAvoidsCollisionsAcrossRepos(t *testing.T) {
	a := DirName("acme/widgets", 42)
	b := DirName("other/widgets", 42)
	if a == b {
		t.Fatalf("same directory name for different repos: %q", a)
	}
	if DirName("acme/widgets", 42) == DirName("acme/widgets", 43) {
		t.Fatal("same directory name for different PR numbers")
	}
	if DirName("acme/widgets", 42) != a {
		t.Fatal("directory name is not deterministic")
	}
}

func TestDirNameIsFilesystemSafe(t *testing.T) {
	for _, repo := range []string{"acme/widgets", "a b/c:d", "../../etc", "UPPER/Case"} {
		got := DirName(repo, 1)
		if strings.ContainsAny(got, "/\\: ") {
			t.Errorf("unsafe directory name for %q: %q", repo, got)
		}
		if strings.Contains(got, "..") {
			t.Errorf("directory name for %q can traverse: %q", repo, got)
		}
	}
}

func TestEnsureCreatesWorktreeAtPRHead(t *testing.T) {
	fx := newFixture(t)
	wt, err := fx.m.Ensure(context.Background(), fx.src, testPR())
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !wt.Created {
		t.Error("expected Created")
	}
	if wt.HeadSHA != headA {
		t.Errorf("head = %q, want %q", wt.HeadSHA, headA)
	}
	if !fx.f.Ran("git worktree add --detach " + wt.Path + " " + headA) {
		t.Fatalf("worktree not created at PR head: %v", fx.f.CommandLines())
	}
}

// The checked-out revision must be the SHA GitHub reported, never a branch name
// that could have moved.
func TestEnsureChecksOutExactSHANotBranch(t *testing.T) {
	fx := newFixture(t)
	pr := testPR()
	if _, err := fx.m.Ensure(context.Background(), fx.src, pr); err != nil {
		t.Fatal(err)
	}
	for _, line := range fx.f.CommandLines() {
		if strings.HasPrefix(line, "git worktree add") && strings.Contains(line, pr.HeadRef) {
			t.Fatalf("worktree created from branch name rather than SHA: %q", line)
		}
	}
}

func TestEnsureIsIdempotentForUnchangedPR(t *testing.T) {
	fx := newFixture(t)
	pr := testPR()
	first, err := fx.m.Ensure(context.Background(), fx.src, pr)
	if err != nil {
		t.Fatal(err)
	}

	fx.registerWorktree(first.Path)
	fx.f.Reset()

	second, err := fx.m.Ensure(context.Background(), fx.src, pr)
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if second.Created || second.Refreshed {
		t.Error("unchanged PR should reuse the worktree untouched")
	}
	if second.Path != first.Path {
		t.Errorf("path changed between calls: %q vs %q", first.Path, second.Path)
	}
	if fx.f.Ran("git worktree add") || fx.f.Ran("git checkout") {
		t.Fatalf("unchanged PR mutated the worktree: %v", fx.f.CommandLines())
	}
}

func TestEnsureRefreshesWhenHeadMoves(t *testing.T) {
	fx := newFixture(t)
	pr := testPR()
	first, err := fx.m.Ensure(context.Background(), fx.src, pr)
	if err != nil {
		t.Fatal(err)
	}
	fx.registerWorktree(first.Path)
	fx.f.Reset()

	pr.HeadSHA = headB
	wt, err := fx.m.Ensure(context.Background(), fx.src, pr)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !wt.Refreshed {
		t.Error("expected Refreshed")
	}
	if wt.HeadSHA != headB {
		t.Errorf("head = %q, want %q", wt.HeadSHA, headB)
	}
	if !fx.f.Ran("git checkout --detach " + headB) {
		t.Fatalf("worktree not moved to new head: %v", fx.f.CommandLines())
	}
}

// A force-push moves the head to an unrelated SHA; it must be handled the same
// as any other head movement.
func TestEnsureHandlesForcePush(t *testing.T) {
	fx := newFixture(t)
	pr := testPR()
	first, _ := fx.m.Ensure(context.Background(), fx.src, pr)
	fx.registerWorktree(first.Path)
	fx.f.Reset()

	pr.HeadSHA = "cccccccccccccccccccccccccccccccccccccccc"
	wt, err := fx.m.Ensure(context.Background(), fx.src, pr)
	if err != nil {
		t.Fatalf("force-pushed PR: %v", err)
	}
	if !wt.Refreshed || wt.HeadSHA != pr.HeadSHA {
		t.Fatal("force-pushed head not adopted")
	}
}

// Base movement changes the cache key but must not disturb the checkout, which
// tracks the head only.
func TestEnsureIgnoresBaseMovementForCheckout(t *testing.T) {
	fx := newFixture(t)
	pr := testPR()
	first, _ := fx.m.Ensure(context.Background(), fx.src, pr)
	fx.registerWorktree(first.Path)
	fx.f.Reset()

	pr.BaseSHA = "9999999999999999999999999999999999999999"
	wt, err := fx.m.Ensure(context.Background(), fx.src, pr)
	if err != nil {
		t.Fatal(err)
	}
	if wt.Refreshed {
		t.Error("base movement alone must not re-checkout")
	}
}

func TestEnsureRefusesToDiscardReviewerChanges(t *testing.T) {
	fx := newFixture(t)
	pr := testPR()
	first, _ := fx.m.Ensure(context.Background(), fx.src, pr)
	fx.registerWorktree(first.Path)
	fx.f.RespondOK("git status --porcelain", " M notes.md\n")
	fx.f.Reset()

	pr.HeadSHA = headB
	_, err := fx.m.Ensure(context.Background(), fx.src, pr)
	if !errors.Is(err, ErrDirtyWorktree) {
		t.Fatalf("got %v, want ErrDirtyWorktree", err)
	}
	if fx.f.Ran("git checkout") {
		t.Fatal("dirty worktree was checked out anyway")
	}
}

// Fork heads are fetched through the pull request ref, so no untrusted remote
// is ever added.
func TestEnsureFetchesForkViaPullRef(t *testing.T) {
	fx := newFixture(t)
	pr := testPR()
	pr.IsFork = true
	pr.HeadRepo = "outsider/widgets"
	if _, err := fx.m.Ensure(context.Background(), fx.src, pr); err != nil {
		t.Fatal(err)
	}
	if !fx.f.Ran("git fetch --no-tags --no-recurse-submodules origin refs/pull/42/head") {
		t.Fatalf("fork head not fetched via pull ref: %v", fx.f.CommandLines())
	}
	if fx.f.Ran("git remote add") {
		t.Fatal("added a remote for an untrusted fork")
	}
}

func TestEnsureWorksForNonMainBaseBranch(t *testing.T) {
	fx := newFixture(t)
	pr := testPR()
	pr.BaseRef = "develop"
	if _, err := fx.m.Ensure(context.Background(), fx.src, pr); err != nil {
		t.Fatal(err)
	}
	if !fx.f.Ran("origin develop") {
		t.Fatalf("non-main base not fetched: %v", fx.f.CommandLines())
	}
}

// Closed and merged PRs still have a head revision and remain reviewable.
func TestEnsureAllowsClosedAndMergedPRs(t *testing.T) {
	for _, st := range []gh.State{gh.StateClosed, gh.StateMerged} {
		fx := newFixture(t)
		pr := testPR()
		pr.State = st
		if _, err := fx.m.Ensure(context.Background(), fx.src, pr); err != nil {
			t.Fatalf("state %s: %v", st, err)
		}
	}
}

func TestEnsureRejectsPRWithoutHead(t *testing.T) {
	fx := newFixture(t)
	pr := testPR()
	pr.HeadSHA = ""
	if _, err := fx.m.Ensure(context.Background(), fx.src, pr); err == nil {
		t.Fatal("expected error for PR without head revision")
	}
}

func TestEnsureRejectsNilPR(t *testing.T) {
	fx := newFixture(t)
	if _, err := fx.m.Ensure(context.Background(), fx.src, nil); err == nil {
		t.Fatal("expected error for nil PR")
	}
}

// An interrupted creation can leave a directory with no registered worktree.
// Deleting it blindly could destroy user data, so refuse instead.
func TestEnsureRefusesUnregisteredDirectory(t *testing.T) {
	fx := newFixture(t)
	pr := testPR()
	path := fx.m.Path(pr.Repo, pr.Number)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "leftover.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := fx.m.Ensure(context.Background(), fx.src, pr)
	if err == nil {
		t.Fatal("expected refusal for unregistered directory")
	}
	if _, statErr := os.Stat(filepath.Join(path, "leftover.txt")); statErr != nil {
		t.Fatal("refusing path deleted user data")
	}
}

// A worktree registered in git but missing on disk is treated as absent so it
// can be recreated.
func TestEnsureRecreatesWhenDirectoryVanished(t *testing.T) {
	fx := newFixture(t)
	pr := testPR()
	path := fx.m.Path(pr.Repo, pr.Number)
	abs, _ := filepath.Abs(path)
	fx.f.RespondOK("git worktree list", "worktree "+abs+"\nHEAD "+headA+"\n\n")

	wt, err := fx.m.Ensure(context.Background(), fx.src, pr)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !wt.Created {
		t.Fatal("expected worktree to be recreated")
	}
}

func TestEnsurePropagatesFetchFailure(t *testing.T) {
	fx := newFixture(t)
	fx.f.Respond("git fetch --no-tags --no-recurse-submodules origin refs/pull",
		xexec.Response{Stderr: "couldn't find remote ref", Err: xexec.ErrExit})
	if _, err := fx.m.Ensure(context.Background(), fx.src, testPR()); err == nil {
		t.Fatal("expected fetch failure to propagate")
	}
	if fx.f.Ran("git worktree add") {
		t.Fatal("created a worktree despite failed fetch")
	}
}

func TestEnsureCancelledContext(t *testing.T) {
	fx := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fx.m.Ensure(ctx, fx.src, testPR()); err == nil {
		t.Fatal("expected cancellation error")
	}
}

// The central safety property: preparing a worktree must never execute code
// from the pull request.
func TestEnsureNeverRunsUntrustedCode(t *testing.T) {
	fx := newFixture(t)
	if _, err := fx.m.Ensure(context.Background(), fx.src, testPR()); err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"npm", "yarn", "pnpm", "bun", "go generate", "go build", "go test",
		"make", "sh ", "bash", "python", "node ", "cargo", "gradle", "mvn",
		"husky", "pre-commit",
	}
	for _, line := range fx.f.CommandLines() {
		if !strings.HasPrefix(line, "git ") {
			t.Fatalf("non-git command during worktree preparation: %q", line)
		}
		for _, bad := range forbidden {
			if strings.Contains(line, bad) {
				t.Fatalf("forbidden command during worktree preparation: %q", line)
			}
		}
	}
}

// Nothing may copy local secrets or dependency directories into a worktree.
func TestEnsureCopiesNothingIntoWorktree(t *testing.T) {
	fx := newFixture(t)
	wt, err := fx.m.Ensure(context.Background(), fx.src, testPR())
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range fx.f.CommandLines() {
		for _, bad := range []string{"cp ", "rsync", "ln -s", ".env", ".npmrc", "node_modules"} {
			if strings.Contains(line, bad) {
				t.Fatalf("worktree preparation touched %q: %s", bad, line)
			}
		}
	}
	// Nothing beyond git's own checkout should exist in the worktree root.
	if entries, err := os.ReadDir(wt.Path); err == nil {
		for _, e := range entries {
			if e.Name() == ".env" || e.Name() == ".npmrc" || e.Name() == "node_modules" {
				t.Fatalf("secret or dependency directory present: %s", e.Name())
			}
		}
	}
}

func TestWorktreeLivesOutsideSourceRepo(t *testing.T) {
	fx := newFixture(t)
	wt, err := fx.m.Ensure(context.Background(), fx.src, testPR())
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(wt.Path, fx.src) {
		t.Fatalf("worktree %q is inside the source repository %q", wt.Path, fx.src)
	}
}

func TestRemoveRefusesDirtyWorktree(t *testing.T) {
	fx := newFixture(t)
	pr := testPR()
	first, _ := fx.m.Ensure(context.Background(), fx.src, pr)
	fx.registerWorktree(first.Path)
	fx.f.RespondOK("git status --porcelain", "?? scratch.md\n")

	err := fx.m.Remove(context.Background(), fx.src, pr.Repo, pr.Number)
	if !errors.Is(err, ErrDirtyWorktree) {
		t.Fatalf("got %v, want ErrDirtyWorktree", err)
	}
	if fx.f.Ran("git worktree remove") {
		t.Fatal("removed a dirty worktree")
	}
}

func TestRemoveCleansRegisteredWorktree(t *testing.T) {
	fx := newFixture(t)
	pr := testPR()
	first, _ := fx.m.Ensure(context.Background(), fx.src, pr)
	fx.registerWorktree(first.Path)

	if err := fx.m.Remove(context.Background(), fx.src, pr.Repo, pr.Number); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !fx.f.Ran("git worktree remove") {
		t.Fatalf("worktree not removed: %v", fx.f.CommandLines())
	}
}

func TestRemoveIsNoOpWhenAbsent(t *testing.T) {
	fx := newFixture(t)
	if err := fx.m.Remove(context.Background(), fx.src, "acme/widgets", 42); err != nil {
		t.Fatalf("Remove on absent worktree: %v", err)
	}
}
