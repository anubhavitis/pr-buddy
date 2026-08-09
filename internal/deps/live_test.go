package deps

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
)

// TestLiveCloneIsolatesWrites runs a real clone against the real filesystem.
//
// The isolation it checks is the whole reason this package copies rather than
// links, and a fake runner cannot demonstrate it: only a real `cp -c` shows that
// writing in the worktree leaves the reviewer's own checkout untouched.
func TestLiveCloneIsolatesWrites(t *testing.T) {
	if os.Getenv("PR_BUDDY_LIVE") != "1" {
		t.Skip("set PR_BUDDY_LIVE=1 to run against the real filesystem")
	}

	src, wt := t.TempDir(), t.TempDir()
	pkg := filepath.Join(src, depsDir, "react")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	original := "module.exports = 'original'\n"
	if err := os.WriteFile(filepath.Join(pkg, "index.js"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := New(xexec.Real{}, src).Prepare(context.Background(), wt)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !res.Cloned {
		t.Fatal("nothing was cloned")
	}

	cloned := filepath.Join(wt, depsDir, "react", "index.js")
	got, err := os.ReadFile(cloned)
	if err != nil {
		t.Fatalf("cloned dependency is not readable: %v", err)
	}
	if string(got) != original {
		t.Fatalf("clone content = %q, want %q", got, original)
	}

	// The property that makes copying safe: a write in the worktree must not
	// reach the source. A symlink would fail here.
	if err := os.WriteFile(cloned, []byte("module.exports = 'tampered'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(pkg, "index.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatalf("writing in the worktree modified the source checkout: %q", after)
	}

	// Staging must not survive a successful clone.
	entries, err := os.ReadDir(wt)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if len(e.Name()) >= len(stagingPrefix) && e.Name()[:len(stagingPrefix)] == stagingPrefix {
			t.Errorf("staging directory left behind: %s", e.Name())
		}
	}
}
