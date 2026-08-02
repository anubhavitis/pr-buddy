// Package worktree manages one isolated git worktree per pull request.
//
// Safety model: pull request code is untrusted. This package checks out code
// and nothing else. It never installs dependencies, runs hooks, executes
// repository scripts, or copies secrets or dependency directories into a
// worktree.
package worktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
	"github.com/anubhavitis/pr-buddy/internal/gh"
)

// ErrDirtyWorktree reports that a worktree holds changes pr-buddy did not make,
// so it will not be destroyed or reset.
var ErrDirtyWorktree = errors.New("worktree has uncommitted changes")

// Manager creates and refreshes review worktrees.
type Manager struct {
	Runner xexec.Runner
	// Root is the directory holding all review worktrees. It lives outside any
	// reviewed repository so a PR cannot reach it with a relative path.
	Root string
}

// New returns a Manager rooted at root.
func New(r xexec.Runner, root string) *Manager {
	return &Manager{Runner: r, Root: root}
}

// Worktree is a prepared checkout of a pull request head.
type Worktree struct {
	Path    string
	HeadSHA string
	// Created reports whether this call created the worktree rather than
	// reusing an existing one.
	Created bool
	// Refreshed reports whether an existing worktree was moved to a new head.
	Refreshed bool
}

// DirName returns the directory name for a pull request's worktree.
//
// The name includes a digest of the full repository slug, so PR 42 in
// acme/widgets and PR 42 in other/widgets can never collide.
func DirName(repo string, number int) string {
	h := sha256.Sum256([]byte(repo))
	return fmt.Sprintf("%s-%d-%s", slug(repo), number, hex.EncodeToString(h[:])[:8])
}

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// slug reduces a repository slug to a filesystem-safe fragment.
func slug(repo string) string {
	s := unsafeChars.ReplaceAllString(repo, "-")
	s = strings.Trim(s, "-.")
	if s == "" {
		s = "repo"
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return strings.ToLower(s)
}

// Path returns where a pull request's worktree lives.
func (m *Manager) Path(repo string, number int) string {
	return filepath.Join(m.Root, DirName(repo, number))
}

// Ensure prepares a worktree checked out at the pull request's current head.
//
// It is idempotent: calling it repeatedly for an unchanged pull request reuses
// the existing worktree without touching the filesystem. When the head has
// moved, the worktree is refreshed in place, but only if it holds no changes of
// the reviewer's own.
func (m *Manager) Ensure(ctx context.Context, srcRepoDir string, pr *gh.PR) (*Worktree, error) {
	if pr == nil {
		return nil, errors.New("nil pull request")
	}
	if pr.HeadSHA == "" {
		return nil, errors.New("pull request has no head revision")
	}
	if pr.Repo == "" {
		return nil, errors.New("pull request has no repository")
	}

	path := m.Path(pr.Repo, pr.Number)

	if err := m.fetch(ctx, srcRepoDir, pr); err != nil {
		return nil, err
	}

	exists, err := m.worktreeExists(ctx, srcRepoDir, path)
	if err != nil {
		return nil, err
	}
	if !exists {
		// A stale directory with no registered worktree would make `git
		// worktree add` fail; refuse rather than delete something unknown.
		if _, err := os.Stat(path); err == nil {
			return nil, fmt.Errorf("%s exists but is not a registered worktree; remove it manually", path)
		}
		if err := m.add(ctx, srcRepoDir, path, pr.HeadSHA); err != nil {
			return nil, err
		}
		return &Worktree{Path: path, HeadSHA: pr.HeadSHA, Created: true}, nil
	}

	current, err := m.headSHA(ctx, path)
	if err != nil {
		return nil, err
	}
	if current == pr.HeadSHA {
		return &Worktree{Path: path, HeadSHA: pr.HeadSHA}, nil
	}

	dirty, err := m.isDirty(ctx, path)
	if err != nil {
		return nil, err
	}
	if dirty {
		return nil, fmt.Errorf("%w: %s", ErrDirtyWorktree, path)
	}
	if err := m.checkout(ctx, path, pr.HeadSHA); err != nil {
		return nil, err
	}
	return &Worktree{Path: path, HeadSHA: pr.HeadSHA, Refreshed: true}, nil
}

// fetch retrieves the pull request head and base revisions into the source
// repository. Fork heads are reachable through the pull request ref, so no
// remote is ever added for an untrusted fork.
func (m *Manager) fetch(ctx context.Context, dir string, pr *gh.PR) error {
	refspec := fmt.Sprintf("refs/pull/%d/head", pr.Number)
	if _, err := m.Runner.Run(ctx, dir, "git", "fetch", "--no-tags", "--no-recurse-submodules", "origin", refspec); err != nil {
		return fmt.Errorf("fetching pull request head: %w", err)
	}
	if pr.BaseRef != "" {
		// Base movement matters for the cache key, so keep it current too. A
		// failure here is not fatal: the head is what gets reviewed.
		_, _ = m.Runner.Run(ctx, dir, "git", "fetch", "--no-tags", "--no-recurse-submodules", "origin", pr.BaseRef)
	}
	return nil
}

// add creates a detached worktree at sha.
func (m *Manager) add(ctx context.Context, dir, path, sha string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// --detach keeps the review off any branch, so nothing can be pushed from
	// here by accident.
	_, err := m.Runner.Run(ctx, dir, "git", "worktree", "add", "--detach", path, sha)
	if err != nil {
		return fmt.Errorf("creating worktree: %w", err)
	}
	return nil
}

// checkout moves an existing worktree to sha.
func (m *Manager) checkout(ctx context.Context, path, sha string) error {
	if _, err := m.Runner.Run(ctx, path, "git", "checkout", "--detach", sha); err != nil {
		return fmt.Errorf("refreshing worktree: %w", err)
	}
	return nil
}

// worktreeExists reports whether git has a worktree registered at path.
func (m *Manager) worktreeExists(ctx context.Context, dir, path string) (bool, error) {
	out, err := m.Runner.Run(ctx, dir, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("listing worktrees: %w", err)
	}
	want, err := filepath.Abs(path)
	if err != nil {
		want = path
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		got := strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
		if got == want || got == path {
			// A registered worktree whose directory was deleted must be
			// treated as absent.
			if _, err := os.Stat(got); err != nil {
				return false, nil
			}
			return true, nil
		}
	}
	return false, nil
}

// headSHA reports the revision currently checked out in a worktree.
func (m *Manager) headSHA(ctx context.Context, path string) (string, error) {
	out, err := m.Runner.Run(ctx, path, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("reading worktree head: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// isDirty reports whether a worktree holds uncommitted changes.
func (m *Manager) isDirty(ctx context.Context, path string) (bool, error) {
	out, err := m.Runner.Run(ctx, path, "git", "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("checking worktree status: %w", err)
	}
	return strings.TrimSpace(out) != "", nil
}

// Remove deletes a review worktree. It refuses when the worktree holds
// uncommitted changes, so a reviewer's own edits are never silently lost.
func (m *Manager) Remove(ctx context.Context, srcRepoDir, repo string, number int) error {
	path := m.Path(repo, number)
	exists, err := m.worktreeExists(ctx, srcRepoDir, path)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	dirty, err := m.isDirty(ctx, path)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("%w: refusing to remove %s", ErrDirtyWorktree, path)
	}
	if _, err := m.Runner.Run(ctx, srcRepoDir, "git", "worktree", "remove", path); err != nil {
		return fmt.Errorf("removing worktree: %w", err)
	}
	return nil
}
