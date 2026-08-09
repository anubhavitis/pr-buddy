// Package deps makes a review worktree navigable by giving it the dependency
// tree the reviewer already has installed.
//
// Safety model: pull request code is untrusted, and that does not change here.
// Nothing is installed, built, or executed — no package manager is ever invoked
// and no manifest from the pull request is honoured. The dependency tree is
// copied, never linked: a symlink would let anything writing in the worktree
// reach through into the reviewer's own checkout, which is exactly the blast
// radius this package exists to avoid.
//
// The copy is a copy-on-write clone, so it shares physical storage with the
// source until something writes. That is what makes copying a multi-gigabyte
// tree affordable enough to prefer over linking.
package deps

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
)

// depsDir holds installed dependencies.
const depsDir = "node_modules"

// buildDir holds a workspace package's compiled output. It is cloned because
// packages declare their type declarations inside it while gitignoring it, so a
// checkout alone never has what an import needs.
const buildDir = "dist"

// stagingPrefix names the directory a clone is built in before it is moved into
// place. An interrupted clone leaves this behind rather than a partial
// node_modules, which tooling would otherwise trust.
const stagingPrefix = ".pr-buddy-deps-tmp"

// lockfiles are compared to decide whether the reviewer's installed tree still
// describes what the pull request expects.
var lockfiles = []string{"pnpm-lock.yaml", "package-lock.json", "yarn.lock", "bun.lock"}

// workspaceParents are the directories searched for nested dependency trees.
// Workspace packages keep their own, and resolution fails without them.
var workspaceParents = []string{"apps", "packages"}

// Manager clones a dependency tree from a source checkout into worktrees.
type Manager struct {
	Runner xexec.Runner
	// Source is the reviewer's own checkout, whose installed dependencies are
	// the ones cloned. It is never written to.
	Source string
}

// New returns a Manager cloning from source.
func New(r xexec.Runner, source string) *Manager {
	return &Manager{Runner: r, Source: source}
}

// Result reports what Prepare did.
type Result struct {
	// Cloned reports that a dependency tree was placed in the worktree.
	Cloned bool
	// AlreadyPresent reports that the worktree already had one, so nothing was
	// done.
	AlreadyPresent bool
	// LockfileDiffers reports that the pull request's lockfile is not the one
	// the cloned tree was resolved from, so the types on screen may not be the
	// ones the pull request would build against.
	LockfileDiffers bool
	// Paths are the dependency directories created, relative to the worktree.
	Paths []string
}

// Prepare gives worktreeDir the dependency tree from the source checkout.
//
// It is a no-op when the source has no dependencies to clone or the worktree
// already has its own. Failure to clone is reported, but callers are expected to
// treat it as degraded navigation rather than a failed review: the worktree is
// still readable without dependencies.
func (m *Manager) Prepare(ctx context.Context, worktreeDir string) (*Result, error) {
	if m.Source == "" || worktreeDir == "" {
		return nil, errors.New("deps: source and worktree are both required")
	}
	res := &Result{}

	if !exists(filepath.Join(m.Source, depsDir)) {
		return res, nil
	}

	res.LockfileDiffers = m.lockfileDiffers(worktreeDir)

	// Each directory is judged on its own. Treating the root as a proxy for the
	// whole tree would leave a worktree that lost only some of its dependency
	// directories permanently unable to recover them, since the root's presence
	// would report the work as already done.
	missing := 0
	for _, rel := range m.cloneable(worktreeDir) {
		if exists(filepath.Join(worktreeDir, rel)) {
			continue
		}
		missing++
		if err := m.clone(ctx, rel, worktreeDir); err != nil {
			return res, err
		}
		res.Paths = append(res.Paths, rel)
		res.Cloned = true
	}
	res.AlreadyPresent = missing == 0
	return res, nil
}

// cloneable lists the directories to clone, relative to a checkout. A nested
// directory is included only when the worktree actually has the package it
// belongs to.
//
// Build output is included alongside dependencies because workspace packages
// routinely declare their types and entry points inside it while gitignoring
// it. A fresh checkout therefore has none, and every import of such a package
// fails to resolve -- the exact problem cloning dependencies is meant to solve.
func (m *Manager) cloneable(worktreeDir string) []string {
	rels := []string{depsDir}
	for _, parent := range workspaceParents {
		entries, err := os.ReadDir(filepath.Join(m.Source, parent))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			// Cloning into a package the worktree does not have would create a
			// directory belonging to no source tree.
			if !exists(filepath.Join(worktreeDir, parent, e.Name())) {
				continue
			}
			for _, child := range []string{depsDir, buildDir} {
				rel := filepath.Join(parent, e.Name(), child)
				if exists(filepath.Join(m.Source, rel)) {
					rels = append(rels, rel)
				}
			}
		}
	}
	return rels
}

// clone copies one dependency directory into the worktree.
//
// `cp -c` requests a copy-on-write clone. It silently falls back to a full copy
// when the two paths are on different filesystems, which is correct but costly;
// callers keeping worktrees on the same volume as their checkout get the cheap
// path.
func (m *Manager) clone(ctx context.Context, rel, worktreeDir string) error {
	src := filepath.Join(m.Source, rel)
	dst := filepath.Join(worktreeDir, rel)

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	// Stage under a name derived from the destination so concurrent preparations
	// of different worktrees cannot collide, then move into place: a clone that
	// is interrupted leaves staging behind rather than a partial node_modules.
	staging := filepath.Join(filepath.Dir(dst), stagingName(dst))
	_ = os.RemoveAll(staging)

	if _, err := m.Runner.Run(ctx, "", "cp", "-c", "-R", src, staging); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("cloning %s: %w", rel, err)
	}
	// A runner that reported success without producing anything has nothing to
	// move; that is not a failure of this step.
	if !exists(staging) {
		return nil
	}
	if err := os.Rename(staging, dst); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("installing cloned %s: %w", rel, err)
	}
	return nil
}

func stagingName(dst string) string {
	h := sha256.Sum256([]byte(dst))
	return fmt.Sprintf("%s-%x", stagingPrefix, h[:4])
}

// lockfileDiffers reports whether the pull request's lockfile differs from the
// one the source's dependencies were resolved from. A lockfile present in only
// one of the two counts as a difference; absent from both does not.
func (m *Manager) lockfileDiffers(worktreeDir string) bool {
	for _, name := range lockfiles {
		srcSum, srcOK := digest(filepath.Join(m.Source, name))
		wtSum, wtOK := digest(filepath.Join(worktreeDir, name))
		if !srcOK && !wtOK {
			continue
		}
		if srcOK != wtOK || srcSum != wtSum {
			return true
		}
	}
	return false
}

func digest(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), true
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
