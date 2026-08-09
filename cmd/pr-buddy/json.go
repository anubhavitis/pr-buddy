package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anubhavitis/pr-buddy/internal/artifact"
	"github.com/anubhavitis/pr-buddy/internal/deps"
	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
	"github.com/anubhavitis/pr-buddy/internal/gh"
	"github.com/anubhavitis/pr-buddy/internal/render"
	"github.com/anubhavitis/pr-buddy/internal/runner"
	"github.com/anubhavitis/pr-buddy/internal/worktree"
)

// The subcommands below exist for programmatic callers such as the VS Code
// extension. They emit one JSON object on stdout and nothing else, so a caller
// never has to parse human-readable output.

// emit writes v as JSON to stdout.
func emit(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// cmdList answers one level of the org → repo → PR tree.
//
// Levels are separate calls so a caller can expand lazily; listing every PR in
// every repository of every org up front would be far too slow to be useful.
func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	org := fs.String("org", "", "list repositories in this org")
	repo := fs.String("repo", "", "list open pull requests in this repository (owner/name)")
	limit := fs.Int("limit", 0, "maximum entries to return")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `usage: pr-buddy list [-org <login> | -repo <owner/name>]

With no flags, lists the orgs visible to the authenticated user.
Emits JSON on stdout.
`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client := gh.New(xexec.Real{})

	switch {
	case *repo != "":
		prs, err := client.PRs(ctx, *repo, *limit)
		if err != nil {
			return err
		}
		return emit(map[string]any{"repo": *repo, "pull_requests": prs})
	case *org != "":
		repos, err := client.Repos(ctx, *org, *limit)
		if err != nil {
			return err
		}
		return emit(map[string]any{"org": *org, "repos": repos})
	default:
		orgs, warning, err := client.Orgs(ctx)
		if err != nil {
			return err
		}
		out := map[string]any{"orgs": orgs}
		if warning != "" {
			// Both channels: stderr for a human running this directly, and the
			// payload so the extension can surface it without parsing stderr.
			fmt.Fprintln(os.Stderr, "pr-buddy:", warning)
			out["warning"] = warning
		}
		return emit(out)
	}
}

// prepareResult is what a caller needs to open a pull request for review.
type prepareResult struct {
	Repo        string   `json:"repo"`
	PRNumber    int      `json:"pr_number"`
	Title       string   `json:"title"`
	State       string   `json:"state"`
	BaseRef     string   `json:"base_ref"`
	BaseSHA     string   `json:"base_sha"`
	HeadSHA     string   `json:"head_sha"`
	IsFork      bool     `json:"is_fork"`
	Worktree    string   `json:"worktree"`
	ArtifactDir string   `json:"artifact_dir"`
	ReviewJSON  string   `json:"review_json"`
	ReviewMD    string   `json:"review_md"`
	Created     bool     `json:"created"`
	Refreshed   bool     `json:"refreshed"`
	ChangedFile []string `json:"changed_files"`
	// ReviewStatus is the state of any stored review: absent, pending,
	// running, complete, failed, or stale.
	ReviewStatus string `json:"review_status"`
	StaleReason  string `json:"stale_reason,omitempty"`
}

// cmdPrepare checks a pull request out and reports where everything lives,
// without running a review.
//
// The extension calls this first so the worktree and diff are usable within
// seconds, then starts a review separately in the background.
func cmdPrepare(args []string) error {
	fs := flag.NewFlagSet("prepare", flag.ExitOnError)
	repo := fs.String("repo", "", "repository as owner/name (defaults to the current repository)")
	root := fs.String("root", defaultRoot(), "directory holding review worktrees")
	model := fs.String("model", defaultModel, "model the review will use, for cache identity")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: pr-buddy prepare [-repo <owner/name>] <pr-number>\n")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	number, err := prNumber(fs.Arg(0))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	r := xexec.Real{}
	client := gh.New(r)
	cwd, _ := os.Getwd()

	slug := *repo
	if slug == "" {
		if slug, err = client.CurrentRepo(ctx, cwd); err != nil {
			return fmt.Errorf("determining current repository (use -repo owner/name): %w", err)
		}
	}

	pr, err := client.ViewPR(ctx, cwd, slug, number)
	if err != nil {
		return err
	}

	src, err := sourceRepoDir(ctx, r, cwd, slug, *root)
	if err != nil {
		return err
	}

	wt, err := worktree.New(r, *root).Ensure(ctx, src, pr)
	if err != nil {
		return err
	}

	prov := artifact.Provenance{
		Repo: pr.Repo, PRNumber: pr.Number,
		BaseSHA: pr.BaseSHA, HeadSHA: pr.HeadSHA,
		RubricVersion: runner.RubricVersion, Model: *model,
		SchemaVersion: artifact.Version,
	}
	artifactDir := reviewDir(*root, pr.Repo, pr.Number)

	res := prepareResult{
		Repo: pr.Repo, PRNumber: pr.Number, Title: pr.Title,
		State: string(pr.State), BaseRef: pr.BaseRef,
		BaseSHA: pr.BaseSHA, HeadSHA: pr.HeadSHA, IsFork: pr.IsFork,
		Worktree: wt.Path, ArtifactDir: artifactDir,
		ReviewJSON: filepath.Join(artifactDir, "review.json"),
		ReviewMD:   filepath.Join(artifactDir, "review.md"),
		Created:    wt.Created, Refreshed: wt.Refreshed,
		ReviewStatus: "absent",
	}

	// Changed files are best effort: the worktree is usable without them.
	if files, err := client.ChangedFiles(ctx, pr.Repo, pr.Number); err == nil {
		res.ChangedFile = files
	}

	if stored, err := artifact.ReadReview(artifactDir); err == nil {
		switch {
		case stored.Usable(prov):
			res.ReviewStatus = string(artifact.StatusComplete)
		case stored.Status == artifact.StatusComplete:
			res.ReviewStatus = "stale"
			res.StaleReason = stored.StaleReason(prov)
		default:
			res.ReviewStatus = string(stored.Status)
		}
	} else if !errors.Is(err, artifact.ErrNotFound) {
		res.ReviewStatus = "unreadable"
	}

	return emit(res)
}

// depsResult reports what dependency setup did.
type depsResult struct {
	Repo     string `json:"repo"`
	PRNumber int    `json:"pr_number"`
	Worktree string `json:"worktree"`
	Cloned   bool   `json:"cloned"`
	// AlreadyPresent reports that the worktree already had dependencies.
	AlreadyPresent bool `json:"already_present"`
	// LockfileDiffers warns that the cloned tree was resolved from a different
	// lockfile than this pull request's, so resolved types may not be the ones
	// it would build against.
	LockfileDiffers bool     `json:"lockfile_differs"`
	Paths           []string `json:"paths,omitempty"`
	Source          string   `json:"source,omitempty"`
}

// cmdDeps clones the reviewer's installed dependencies into a pull request's
// worktree, so imports resolve and the editor can navigate.
//
// This is a separate subcommand rather than part of prepare because it is by far
// the slowest step: a checkout takes seconds and a dependency clone can take a
// minute. Callers run it in the background while the diff is already readable.
//
// Nothing from the pull request is installed or executed; see internal/deps.
func cmdDeps(args []string) error {
	fs := flag.NewFlagSet("deps", flag.ExitOnError)
	repo := fs.String("repo", "", "repository as owner/name (defaults to the current repository)")
	root := fs.String("root", defaultRoot(), "directory holding review worktrees")
	source := fs.String("source", "", "checkout whose installed dependencies are cloned (defaults to the current directory)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: pr-buddy deps [-repo <owner/name>] [-source <dir>] <pr-number>\n")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	number, err := prNumber(fs.Arg(0))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	r := xexec.Real{}
	cwd, _ := os.Getwd()

	slug := *repo
	if slug == "" {
		if slug, err = gh.New(r).CurrentRepo(ctx, cwd); err != nil {
			return fmt.Errorf("determining current repository (use -repo owner/name): %w", err)
		}
	}

	src := *source
	if src == "" {
		src = cwd
	}

	path := worktree.New(r, *root).Path(slug, number)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no worktree for %s#%d; run prepare first", slug, number)
	}

	res, err := deps.New(r, src).Prepare(ctx, path)
	if err != nil {
		return err
	}
	return emit(depsResult{
		Repo: slug, PRNumber: number, Worktree: path,
		Cloned: res.Cloned, AlreadyPresent: res.AlreadyPresent,
		LockfileDiffers: res.LockfileDiffers, Paths: res.Paths,
		Source: src,
	})
}

// progressResult reports which files carry a valid reviewed mark.
type progressResult struct {
	Repo     string `json:"repo"`
	PRNumber int    `json:"pr_number"`
	// Reviewed lists the repository-relative paths still marked reviewed at
	// their current content.
	Reviewed []string `json:"reviewed"`
}

// cmdProgress reads or updates which files the reviewer has finished with.
//
// A mark is tied to the file's blob, not just its path, so a file the author
// changes after it was reviewed loses its mark while untouched files keep
// theirs. Blobs are read from the worktree rather than GitHub: the checkout is
// already local and already at the revision under review.
func cmdProgress(args []string) error {
	fs := flag.NewFlagSet("progress", flag.ExitOnError)
	repo := fs.String("repo", "", "repository as owner/name (defaults to the current repository)")
	root := fs.String("root", defaultRoot(), "directory holding review worktrees")
	mark := fs.String("mark", "", "mark this repository-relative path reviewed")
	unmark := fs.String("unmark", "", "clear the mark on this path")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: pr-buddy progress [-repo <owner/name>] [-mark <path> | -unmark <path>] <pr-number>\n")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	number, err := prNumber(fs.Arg(0))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	r := xexec.Real{}
	cwd, _ := os.Getwd()

	slug := *repo
	if slug == "" {
		if slug, err = gh.New(r).CurrentRepo(ctx, cwd); err != nil {
			return fmt.Errorf("determining current repository (use -repo owner/name): %w", err)
		}
	}

	worktreePath := worktree.New(r, *root).Path(slug, number)
	artifactDir := reviewDir(*root, slug, number)

	prog, err := artifact.ReadProgress(artifactDir)
	if err != nil {
		return err
	}

	switch {
	case *mark != "":
		blob, err := blobSHA(ctx, r, worktreePath, *mark)
		if err != nil {
			return err
		}
		prog.Mark(*mark, blob)
	case *unmark != "":
		prog.Unmark(*unmark)
	}
	if *mark != "" || *unmark != "" {
		if err := artifact.WriteProgress(artifactDir, prog); err != nil {
			return err
		}
	}

	// Report only marks that still hold, so a caller never has to re-derive
	// which ones a new push invalidated.
	reviewed := []string{}
	for path := range prog.Files {
		blob, err := blobSHA(ctx, r, worktreePath, path)
		if err != nil {
			// A path that no longer exists at this head cannot be reviewed.
			continue
		}
		if prog.Reviewed(path, blob) {
			reviewed = append(reviewed, path)
		}
	}
	sort.Strings(reviewed)

	return emit(progressResult{Repo: slug, PRNumber: number, Reviewed: reviewed})
}

// blobSHA reports git's content hash for one path at the worktree's head.
func blobSHA(ctx context.Context, r xexec.Runner, worktreePath, path string) (string, error) {
	out, err := r.Run(ctx, worktreePath, "git", "rev-parse", "HEAD:"+path)
	if err != nil {
		return "", fmt.Errorf("reading the current content of %s: %w", path, err)
	}
	return strings.TrimSpace(out), nil
}

// removeResult reports what a remove took away.
type removeResult struct {
	Repo     string `json:"repo"`
	PRNumber int    `json:"pr_number"`
	Removed  bool   `json:"removed"`
	Worktree string `json:"worktree"`
}

// cmdRemove ends a review: it deletes the worktree and the cached review.
//
// GitHub is not consulted, so a pull request that has since been merged, closed,
// or deleted can still be cleaned up.
func cmdRemove(args []string) error {
	fs := flag.NewFlagSet("remove", flag.ExitOnError)
	repo := fs.String("repo", "", "repository as owner/name (defaults to the current repository)")
	root := fs.String("root", defaultRoot(), "directory holding review worktrees")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: pr-buddy remove [-repo <owner/name>] <pr-number>\n")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	number, err := prNumber(fs.Arg(0))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	r := xexec.Real{}
	client := gh.New(r)
	cwd, _ := os.Getwd()

	slug := *repo
	if slug == "" {
		if slug, err = client.CurrentRepo(ctx, cwd); err != nil {
			return fmt.Errorf("determining current repository (use -repo owner/name): %w", err)
		}
	}

	src, err := sourceRepoDir(ctx, r, cwd, slug, *root)
	if err != nil {
		return err
	}

	wm := worktree.New(r, *root)
	path := wm.Path(slug, number)
	if err := wm.Remove(ctx, src, slug, number); err != nil {
		if errors.Is(err, worktree.ErrDirtyWorktree) {
			return fmt.Errorf("%w\n  the worktree holds changes that are not mine; resolve them or remove it manually", err)
		}
		return err
	}

	// Only once the worktree is gone: a refused removal must leave the review
	// that describes it intact.
	_ = os.RemoveAll(reviewDir(*root, slug, number))

	return emit(removeResult{
		Repo: slug, PRNumber: number,
		Removed: true, Worktree: path,
	})
}

// reviewResult reports the outcome of a review run.
type reviewResult struct {
	Repo        string `json:"repo"`
	PRNumber    int    `json:"pr_number"`
	Status      string `json:"status"`
	FromCache   bool   `json:"from_cache"`
	StaleReason string `json:"stale_reason,omitempty"`
	Worktree    string `json:"worktree"`
	ReviewJSON  string `json:"review_json"`
	ReviewMD    string `json:"review_md"`
	SessionID   string `json:"session_id,omitempty"`
	// ResumeCommand is composed by the runner so that every caller offers the
	// same recipe rather than each assembling its own.
	ResumeCommand string             `json:"resume_command,omitempty"`
	Counts        map[string]int     `json:"counts"`
	Findings      []artifact.Finding `json:"findings"`
}

// cmdReviewJSON runs a review and reports the result as JSON.
func cmdReviewJSON(args []string) error {
	fs := flag.NewFlagSet("review", flag.ExitOnError)
	repo := fs.String("repo", "", "repository as owner/name")
	root := fs.String("root", defaultRoot(), "directory holding review worktrees")
	model := fs.String("model", defaultModel, "model to review with")
	force := fs.Bool("force", false, "re-review even when a valid cached review exists")
	timeout := fs.Duration("timeout", 15*time.Minute, "maximum time for one review")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: pr-buddy review [-repo <owner/name>] [-force] <pr-number>\n")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	number, err := prNumber(fs.Arg(0))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout+time.Minute)
	defer cancel()

	r := xexec.Real{}
	client := gh.New(r)
	cwd, _ := os.Getwd()

	slug := *repo
	if slug == "" {
		if slug, err = client.CurrentRepo(ctx, cwd); err != nil {
			return err
		}
	}
	pr, err := client.ViewPR(ctx, cwd, slug, number)
	if err != nil {
		return err
	}
	src, err := sourceRepoDir(ctx, r, cwd, slug, *root)
	if err != nil {
		return err
	}
	wt, err := worktree.New(r, *root).Ensure(ctx, src, pr)
	if err != nil {
		return err
	}

	prov := artifact.Provenance{
		Repo: pr.Repo, PRNumber: pr.Number,
		BaseSHA: pr.BaseSHA, HeadSHA: pr.HeadSHA,
		RubricVersion: runner.RubricVersion, Model: *model,
		SchemaVersion: artifact.Version,
	}
	artifactDir := reviewDir(*root, pr.Repo, pr.Number)
	if *force {
		discardCachedReview(artifactDir)
	}

	run := &runner.Runner{
		Reviewer: &runner.Claude{Runner: r, Model: *model},
		Timeout:  *timeout,
	}
	out, err := run.Run(ctx, artifactDir, wt.Path, prov)
	if err != nil {
		return err
	}

	mdPath := filepath.Join(artifactDir, "review.md")
	if err := os.WriteFile(mdPath, []byte(render.Markdown(out.Review, pr.Title)), 0o644); err != nil {
		return err
	}

	counts := map[string]int{"error": 0, "warning": 0, "info": 0}
	for _, f := range out.Review.Findings {
		counts[string(f.Severity)]++
	}

	res := reviewResult{
		Repo: pr.Repo, PRNumber: pr.Number,
		Status: string(out.Review.Status), FromCache: out.FromCache,
		StaleReason: out.StaleReason, Worktree: wt.Path,
		ReviewJSON: filepath.Join(artifactDir, "review.json"),
		ReviewMD:   mdPath,
		Counts:     counts, Findings: out.Review.Findings,
	}
	if sess, err := artifact.ReadSession(artifactDir); err == nil {
		res.SessionID = sess.SessionID
		res.ResumeCommand = sess.ResumeCommand
	}
	return emit(res)
}

// sourceRepoDir returns a local clone of slug to create worktrees from.
//
// The current directory is used when it is already that repository. Otherwise
// a bare mirror is kept under the review root, so the extension can open a pull
// request in a repository the user has never cloned.
func sourceRepoDir(ctx context.Context, r xexec.Runner, cwd, slug, root string) (string, error) {
	if current, err := gh.New(r).CurrentRepo(ctx, cwd); err == nil && current == slug {
		return cwd, nil
	}
	if root == "" {
		root = defaultRoot()
	}
	// The mirror lives under the same root as the worktrees it feeds, so that
	// -root moves the whole review tree rather than splitting it in two.
	dir := filepath.Join(root, ".repos", worktree.DirName(slug, 0)+".git")
	if _, err := os.Stat(dir); err == nil {
		// Refresh so a newly opened pull request is reachable.
		_, _ = r.Run(ctx, dir, "git", "fetch", "--no-tags", "origin")
		return dir, nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	url := fmt.Sprintf("https://github.com/%s.git", slug)
	if _, err := r.Run(ctx, "", "git", "clone", "--bare", "--filter=blob:none", url, dir); err != nil {
		return "", fmt.Errorf("cloning %s: %w", slug, err)
	}
	return dir, nil
}

func reviewDir(root, repo string, number int) string {
	return filepath.Join(root, ".reviews", worktree.DirName(repo, number))
}

// discardCachedReview drops a stored review and the session that produced it.
//
// The two travel together: a session id belongs to a specific review, so
// keeping it after discarding the review would advertise a resume command for a
// conversation that no longer has an artifact.
func discardCachedReview(artifactDir string) {
	_ = os.Remove(filepath.Join(artifactDir, "review.json"))
	_ = os.Remove(filepath.Join(artifactDir, "session.json"))
}

func prNumber(arg string) (int, error) {
	n, err := strconv.Atoi(arg)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid pull request number %q", arg)
	}
	return n, nil
}
