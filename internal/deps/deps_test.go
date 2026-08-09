package deps

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A dependency directory is cloned, never installed: nothing from the pull
// request may be built, and no package manager may run against its manifests.
func TestPrepareNeverInstalls(t *testing.T) {
	src, wt := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "node_modules", "react", "index.js"), "x")
	write(t, filepath.Join(src, "pnpm-lock.yaml"), "lock")
	write(t, filepath.Join(wt, "pnpm-lock.yaml"), "lock")

	f := xexec.NewFake()
	if _, err := New(f, src).Prepare(context.Background(), wt); err != nil {
		t.Fatal(err)
	}
	for _, line := range f.CommandLines() {
		for _, forbidden := range []string{
			"npm", "pnpm", "yarn", "bun", "npx",
			"install", "run ", "exec", "postinstall",
		} {
			if strings.Contains(line, forbidden) {
				t.Fatalf("dependency setup ran a package manager: %q", line)
			}
		}
	}
}

// Copy-on-write is what makes this affordable and safe. A plain recursive copy
// would cost gigabytes; a symlink would let anything writing in the worktree
// reach into the reviewer's own checkout.
func TestPrepareClonesCopyOnWriteAndNeverSymlinks(t *testing.T) {
	src, wt := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "node_modules", "react", "index.js"), "x")

	f := xexec.NewFake()
	if _, err := New(f, src).Prepare(context.Background(), wt); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.CommandLines(), "\n")
	if !strings.Contains(joined, "cp -c") {
		t.Errorf("clone is not copy-on-write:\n%s", joined)
	}
	if strings.Contains(joined, "ln -s") || strings.Contains(joined, "ln ") {
		t.Errorf("dependencies were symlinked, exposing the source checkout:\n%s", joined)
	}
}

// Cloning into an existing directory would merge two dependency trees. A
// worktree that already has one is left alone.
func TestPrepareSkipsWhenWorktreeAlreadyHasDeps(t *testing.T) {
	src, wt := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "node_modules", "react", "index.js"), "x")
	write(t, filepath.Join(wt, "node_modules", "react", "index.js"), "x")

	f := xexec.NewFake()
	res, err := New(f, src).Prepare(context.Background(), wt)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.CommandLines()) != 0 {
		t.Errorf("cloned over an existing dependency tree: %v", f.CommandLines())
	}
	if !res.AlreadyPresent {
		t.Error("existing dependencies not reported")
	}
}

// A worktree that lost only part of its dependency tree must be able to recover
// it. Judging the whole tree by the root left such a worktree permanently
// broken: the root's presence reported the work as already done.
func TestPrepareRestoresAPartialTree(t *testing.T) {
	src, wt := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "node_modules", "react", "index.js"), "x")
	write(t, filepath.Join(src, "apps", "web", "node_modules", "next", "index.js"), "x")
	// The worktree kept its root dependencies but lost the nested ones.
	write(t, filepath.Join(wt, "node_modules", "react", "index.js"), "x")
	if err := os.MkdirAll(filepath.Join(wt, "apps", "web"), 0o755); err != nil {
		t.Fatal(err)
	}

	f := xexec.NewFake()
	res, err := New(f, src).Prepare(context.Background(), wt)
	if err != nil {
		t.Fatal(err)
	}
	if res.AlreadyPresent {
		t.Error("a partial tree reported as already present")
	}
	joined := strings.Join(f.CommandLines(), "\n")
	if !strings.Contains(joined, filepath.Join("apps", "web", "node_modules")) {
		t.Errorf("missing nested dependencies not restored:\n%s", joined)
	}
	// The directory that survived must not be cloned over.
	if strings.Contains(joined, filepath.Join(src, "node_modules")+" ") {
		t.Errorf("cloned over dependencies that were already present:\n%s", joined)
	}
}

// Nothing to clone is an ordinary outcome, not a failure: plenty of repositories
// have no dependency directory at all.
func TestPrepareIsANoOpWithoutSourceDeps(t *testing.T) {
	src, wt := t.TempDir(), t.TempDir()

	f := xexec.NewFake()
	res, err := New(f, src).Prepare(context.Background(), wt)
	if err != nil {
		t.Fatalf("missing source dependencies must not be an error: %v", err)
	}
	if res.Cloned {
		t.Error("reported a clone that did not happen")
	}
	if len(f.CommandLines()) != 0 {
		t.Errorf("ran commands with nothing to clone: %v", f.CommandLines())
	}
}

// The clone comes from the reviewer's own install, which was resolved from the
// reviewer's lockfile. When the pull request's lockfile differs, the types on
// screen may not be the ones the pull request would actually build against, and
// that has to be visible rather than silently wrong.
func TestPrepareReportsLockfileMismatch(t *testing.T) {
	src, wt := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "node_modules", "react", "index.js"), "x")
	write(t, filepath.Join(src, "pnpm-lock.yaml"), "version: 1")
	write(t, filepath.Join(wt, "pnpm-lock.yaml"), "version: 2")

	res, err := New(xexec.NewFake(), src).Prepare(context.Background(), wt)
	if err != nil {
		t.Fatal(err)
	}
	if !res.LockfileDiffers {
		t.Error("differing lockfiles not reported")
	}
}

func TestPrepareReportsMatchingLockfile(t *testing.T) {
	src, wt := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "node_modules", "react", "index.js"), "x")
	write(t, filepath.Join(src, "pnpm-lock.yaml"), "same")
	write(t, filepath.Join(wt, "pnpm-lock.yaml"), "same")

	res, err := New(xexec.NewFake(), src).Prepare(context.Background(), wt)
	if err != nil {
		t.Fatal(err)
	}
	if res.LockfileDiffers {
		t.Error("identical lockfiles reported as differing")
	}
}

// Workspace packages keep their own dependency directories, and resolution
// fails without them.
func TestPrepareClonesNestedWorkspaceDeps(t *testing.T) {
	src, wt := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "node_modules", "react", "index.js"), "x")
	write(t, filepath.Join(src, "apps", "web", "node_modules", "next", "index.js"), "x")
	write(t, filepath.Join(src, "packages", "uikit", "node_modules", "clsx", "index.js"), "x")
	// Only directories the worktree actually has may be targeted.
	if err := os.MkdirAll(filepath.Join(wt, "apps", "web"), 0o755); err != nil {
		t.Fatal(err)
	}

	f := xexec.NewFake()
	if _, err := New(f, src).Prepare(context.Background(), wt); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.CommandLines(), "\n")
	if !strings.Contains(joined, filepath.Join("apps", "web", "node_modules")) {
		t.Errorf("nested workspace dependencies not cloned:\n%s", joined)
	}
	if strings.Contains(joined, filepath.Join("packages", "uikit", "node_modules")) {
		t.Errorf("cloned into a package the worktree does not have:\n%s", joined)
	}
}

// Workspace packages commonly point `types` and `exports` at build output that
// is gitignored, so a fresh checkout never has it and every import of such a
// package fails to resolve. The reviewer's own build output is cloned for the
// same reason the dependency tree is: it is theirs, not the pull request's, and
// nothing is built to obtain it.
func TestPrepareClonesWorkspaceBuildOutput(t *testing.T) {
	src, wt := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "node_modules", "react", "index.js"), "x")
	write(t, filepath.Join(src, "packages", "precision", "dist", "index.d.ts"), "declare const x: number")
	if err := os.MkdirAll(filepath.Join(wt, "packages", "precision"), 0o755); err != nil {
		t.Fatal(err)
	}

	f := xexec.NewFake()
	if _, err := New(f, src).Prepare(context.Background(), wt); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.CommandLines(), "\n")
	if !strings.Contains(joined, filepath.Join("packages", "precision", "dist")) {
		t.Errorf("workspace build output not cloned:\n%s", joined)
	}
}

// Build output already in the worktree belongs to the pull request and must win.
func TestPrepareLeavesExistingBuildOutputAlone(t *testing.T) {
	src, wt := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "node_modules", "react", "index.js"), "x")
	write(t, filepath.Join(src, "packages", "precision", "dist", "index.d.ts"), "source")
	write(t, filepath.Join(wt, "packages", "precision", "dist", "index.d.ts"), "worktree")

	f := xexec.NewFake()
	if _, err := New(f, src).Prepare(context.Background(), wt); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(f.CommandLines(), "\n"), filepath.Join("packages", "precision", "dist")) {
		t.Error("cloned over build output the worktree already had")
	}
}

// A clone that is interrupted must not leave a half-populated tree behind:
// tooling would believe it and resolve against a partial dependency set.
func TestPrepareStagesThenMovesIntoPlace(t *testing.T) {
	src, wt := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "node_modules", "react", "index.js"), "x")

	f := xexec.NewFake()
	if _, err := New(f, src).Prepare(context.Background(), wt); err != nil {
		t.Fatal(err)
	}
	lines := f.CommandLines()
	var cloneTarget string
	for _, l := range lines {
		if strings.Contains(l, "cp -c") {
			cloneTarget = l
		}
	}
	if cloneTarget == "" {
		t.Fatal("no clone issued")
	}
	if !strings.Contains(cloneTarget, ".pr-buddy-deps-tmp") {
		t.Errorf("clone did not stage into a temporary directory:\n%s", cloneTarget)
	}
}
