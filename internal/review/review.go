// Package review owns the workflow that turns a pull request number into a
// prepared checkout and a stored review.
//
// It exists because both entry points -- the human `pr-buddy <n>` form and the
// JSON subcommands the editor drives -- need the same sequence: resolve the
// repository, read the pull request from GitHub, materialize a worktree, build
// provenance, and consult or produce the cached artifact. Each used to carry
// its own copy of that sequence, and the copies drifted: one resolved the
// source repository through a mirror and the other assumed the current
// directory was already the right checkout, so `-repo` outside the current
// checkout silently reviewed against the wrong repository.
//
// The rule this package encodes is that the CLI decides *what* to review and
// how to present it, and this package decides *how* a review happens. Callers
// contribute flags and formatting, never lifecycle.
package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/anubhavitis/pr-buddy/internal/artifact"
	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
	"github.com/anubhavitis/pr-buddy/internal/gh"
	"github.com/anubhavitis/pr-buddy/internal/render"
	"github.com/anubhavitis/pr-buddy/internal/runner"
	"github.com/anubhavitis/pr-buddy/internal/worktree"
)

// DefaultModel is the model a review uses when a caller names none.
const DefaultModel = "claude-opus-5"

// Metadata reads the pull request data a review depends on. The concrete
// implementation is gh.Client; the interface keeps this package testable
// without a GitHub account.
type Metadata interface {
	ViewPR(ctx context.Context, dir, repo string, number int) (*gh.PR, error)
	CurrentRepo(ctx context.Context, dir string) (string, error)
}

// Service performs review workflows.
type Service struct {
	Runner xexec.Runner
	Meta   Metadata
	// Root holds worktrees, cached reviews, and the bare mirrors used for
	// repositories the reviewer has not cloned.
	Root string
	// Cwd is the directory the caller invoked from, used to resolve "the
	// current repository" and as a worktree source when it already is one.
	Cwd string
}

// New returns a Service backed by the real gh binary.
func New(r xexec.Runner, root, cwd string) *Service {
	return &Service{Runner: r, Meta: gh.New(r), Root: root, Cwd: cwd}
}

// Options names the pull request to work on and how.
type Options struct {
	// Repo is "owner/name". Empty means the repository containing Cwd.
	Repo   string
	Number int
	// Model is the reviewing model. Empty means DefaultModel.
	Model string
	// Force discards any cached review before running.
	Force bool
	// Timeout bounds one model invocation. Zero means the runner's default.
	Timeout time.Duration
}

// Prepared is a pull request checked out and ready to read, with the state of
// any stored review for it.
type Prepared struct {
	PR       *gh.PR
	Worktree *worktree.Worktree
	// Provenance identifies the review this checkout would produce.
	Provenance artifact.Provenance
	// ArtifactDir holds review.json, review.md, and session.json.
	ArtifactDir string
	// Status is the state of any stored review: absent, pending, running,
	// complete, failed, stale, or unreadable.
	Status string
	// StaleReason explains a stale status and is empty otherwise.
	StaleReason string
}

// ReviewJSON and ReviewMD report where the artifacts for this review live.
func (p *Prepared) ReviewJSON() string { return filepath.Join(p.ArtifactDir, "review.json") }
func (p *Prepared) ReviewMD() string   { return filepath.Join(p.ArtifactDir, "review.md") }

// Result is a completed review together with the checkout it describes.
type Result struct {
	*Prepared
	Review *artifact.Review
	// FromCache reports that no model invocation occurred.
	FromCache bool
	// StaleReason explains why the cache was not reused. Empty on a cache hit.
	StaleReason string
	// Session carries the resumable conversation, when one was recorded.
	Session *artifact.Session
}

// Prepare checks a pull request out and reports where everything lives, without
// invoking the model.
//
// The editor calls this first so the diff is readable within seconds, then asks
// for a review separately in the background.
func (s *Service) Prepare(ctx context.Context, opts Options) (*Prepared, error) {
	slug, err := s.resolveRepo(ctx, opts.Repo)
	if err != nil {
		return nil, err
	}

	pr, err := s.Meta.ViewPR(ctx, s.Cwd, slug, opts.Number)
	if err != nil {
		return nil, err
	}

	src, err := s.sourceRepoDir(ctx, slug)
	if err != nil {
		return nil, err
	}

	wt, err := worktree.New(s.Runner, s.Root).Ensure(ctx, src, pr)
	if err != nil {
		return nil, err
	}

	p := &Prepared{
		PR:          pr,
		Worktree:    wt,
		Provenance:  s.provenance(pr, opts.Model),
		ArtifactDir: s.ArtifactDir(pr.Repo, pr.Number),
		Status:      "absent",
	}
	p.Status, p.StaleReason = storedStatus(p.ArtifactDir, p.Provenance)
	return p, nil
}

// Run prepares the pull request and returns a review for it, reusing a valid
// cached review unless Force is set.
//
// The rendered markdown is written as a side effect, because every caller wants
// it and none of them should have to know how a review is rendered.
func (s *Service) Run(ctx context.Context, opts Options) (*Result, error) {
	p, err := s.Prepare(ctx, opts)
	if err != nil {
		return nil, err
	}

	if opts.Force {
		DiscardCached(p.ArtifactDir)
	}

	run := &runner.Runner{
		Reviewer: &runner.Claude{Runner: s.Runner, Model: p.Provenance.Model},
		Timeout:  opts.Timeout,
	}
	out, err := run.Run(ctx, p.ArtifactDir, p.Worktree.Path, p.Provenance)
	if err != nil {
		return nil, err
	}

	md := render.Markdown(out.Review, p.PR.Title)
	if err := os.WriteFile(p.ReviewMD(), []byte(md), 0o644); err != nil {
		return nil, err
	}

	res := &Result{
		Prepared:    p,
		Review:      out.Review,
		FromCache:   out.FromCache,
		StaleReason: out.StaleReason,
	}
	// A missing session is not an error: a cache hit predates this read, and a
	// review remains valid whether or not its conversation can be resumed.
	if sess, err := artifact.ReadSession(p.ArtifactDir); err == nil {
		res.Session = sess
	}
	return res, nil
}

// Remove ends a review: it deletes the worktree and the cached artifacts.
//
// GitHub is not consulted, so a pull request that has since been merged,
// closed, or deleted can still be cleaned up.
func (s *Service) Remove(ctx context.Context, opts Options) (string, error) {
	slug, err := s.resolveRepo(ctx, opts.Repo)
	if err != nil {
		return "", err
	}
	src, err := s.sourceRepoDir(ctx, slug)
	if err != nil {
		return "", err
	}

	wm := worktree.New(s.Runner, s.Root)
	path := wm.Path(slug, opts.Number)
	if err := wm.Remove(ctx, src, slug, opts.Number); err != nil {
		return "", err
	}

	// Only once the worktree is gone: a refused removal must leave the review
	// that describes it intact.
	_ = os.RemoveAll(s.ArtifactDir(slug, opts.Number))
	return path, nil
}

// WorktreePath reports where a pull request's checkout lives, without creating
// it.
func (s *Service) WorktreePath(repo string, number int) string {
	return worktree.New(s.Runner, s.Root).Path(repo, number)
}

// ArtifactDir reports where a pull request's stored review lives.
func (s *Service) ArtifactDir(repo string, number int) string {
	return filepath.Join(s.Root, ".reviews", worktree.DirName(repo, number))
}

// ResolveRepo reports the repository a caller means: the one named, or the one
// containing the current directory.
func (s *Service) ResolveRepo(ctx context.Context, repo string) (string, error) {
	return s.resolveRepo(ctx, repo)
}

func (s *Service) resolveRepo(ctx context.Context, repo string) (string, error) {
	if repo != "" {
		return repo, nil
	}
	slug, err := s.Meta.CurrentRepo(ctx, s.Cwd)
	if err != nil {
		return "", fmt.Errorf("determining current repository (use -repo owner/name): %w", err)
	}
	return slug, nil
}

func (s *Service) provenance(pr *gh.PR, model string) artifact.Provenance {
	if model == "" {
		model = DefaultModel
	}
	return artifact.Provenance{
		Repo:          pr.Repo,
		PRNumber:      pr.Number,
		BaseSHA:       pr.BaseSHA,
		HeadSHA:       pr.HeadSHA,
		RubricVersion: runner.RubricVersion,
		Model:         model,
		SchemaVersion: artifact.Version,
	}
}

// sourceRepoDir returns a local clone of slug to create worktrees from.
//
// The current directory is used when it is already that repository. Otherwise a
// bare mirror is kept under the review root, so a pull request can be opened in
// a repository the reviewer has never cloned. Both entry points resolve this
// the same way; when they did not, `-repo` outside the current checkout
// reviewed the wrong repository.
func (s *Service) sourceRepoDir(ctx context.Context, slug string) (string, error) {
	if current, err := s.Meta.CurrentRepo(ctx, s.Cwd); err == nil && current == slug {
		return s.Cwd, nil
	}
	// The mirror lives under the same root as the worktrees it feeds, so that
	// moving the root moves the whole review tree rather than splitting it.
	dir := filepath.Join(s.Root, ".repos", worktree.DirName(slug, 0)+".git")
	if _, err := os.Stat(dir); err == nil {
		// Refresh so a newly opened pull request is reachable.
		_, _ = s.Runner.Run(ctx, dir, "git", "fetch", "--no-tags", "origin")
		return dir, nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	url := fmt.Sprintf("https://github.com/%s.git", slug)
	if _, err := s.Runner.Run(ctx, "", "git", "clone", "--bare", "--filter=blob:none", url, dir); err != nil {
		return "", fmt.Errorf("cloning %s: %w", slug, err)
	}
	return dir, nil
}

// storedStatus classifies the review stored in dir against current provenance.
func storedStatus(dir string, prov artifact.Provenance) (status, staleReason string) {
	stored, err := artifact.ReadReview(dir)
	if err != nil {
		if errors.Is(err, artifact.ErrNotFound) {
			return "absent", ""
		}
		return "unreadable", ""
	}
	switch {
	case stored.Usable(prov):
		return string(artifact.StatusComplete), ""
	case stored.Status == artifact.StatusComplete:
		return "stale", stored.StaleReason(prov)
	default:
		return string(stored.Status), ""
	}
}

// DiscardCached drops a stored review and the session that produced it.
//
// The two travel together: a session id belongs to a specific review, so
// keeping it after discarding the review would advertise a resume command for a
// conversation that no longer has an artifact.
func DiscardCached(artifactDir string) {
	_ = os.Remove(filepath.Join(artifactDir, "review.json"))
	_ = os.Remove(filepath.Join(artifactDir, "session.json"))
}
