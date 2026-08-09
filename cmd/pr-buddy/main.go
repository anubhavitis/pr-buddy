// Command pr-buddy prepares a pull request for review: it checks the PR head
// out into an isolated worktree, runs a read-only review, and opens the result
// beside the diff.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/anubhavitis/pr-buddy/internal/artifact"
	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
	"github.com/anubhavitis/pr-buddy/internal/gh"
	"github.com/anubhavitis/pr-buddy/internal/render"
	"github.com/anubhavitis/pr-buddy/internal/runner"
	"github.com/anubhavitis/pr-buddy/internal/worktree"
)

const defaultModel = "claude-opus-5"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "pr-buddy:", err)
		os.Exit(1)
	}
}

func run() error {
	// Subcommands emit JSON for programmatic callers such as the VS Code
	// extension. Bare `pr-buddy <n>` remains the human-facing form.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "list":
			return cmdList(os.Args[2:])
		case "prepare":
			return cmdPrepare(os.Args[2:])
		case "review":
			return cmdReviewJSON(os.Args[2:])
		case "remove":
			return cmdRemove(os.Args[2:])
		case "deps":
			return cmdDeps(os.Args[2:])
		}
	}

	var (
		repo    = flag.String("repo", "", "repository as owner/name (defaults to the current repository)")
		model   = flag.String("model", defaultModel, "model to review with")
		root    = flag.String("root", defaultRoot(), "directory holding review worktrees")
		open    = flag.Bool("open", true, "open the worktree and review in VS Code")
		force   = flag.Bool("force", false, "re-review even when a valid cached review exists")
		timeout = flag.Duration("timeout", 15*time.Minute, "maximum time for one review")
	)
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() != 1 {
		usage()
		return errors.New("expected exactly one pull request number")
	}
	number, err := strconv.Atoi(flag.Arg(0))
	if err != nil || number <= 0 {
		return fmt.Errorf("invalid pull request number %q", flag.Arg(0))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	r := xexec.Real{}
	client := gh.New(r)

	slug := *repo
	if slug == "" {
		slug, err = client.CurrentRepo(ctx, cwd)
		if err != nil {
			return fmt.Errorf("determining current repository (use -repo owner/name): %w", err)
		}
	}

	fmt.Printf("Reading %s#%d\n", slug, number)
	pr, err := client.ViewPR(ctx, cwd, slug, number)
	if err != nil {
		return err
	}
	if pr.IsFork {
		fmt.Printf("  fork: %s (treated as untrusted, as all PR code is)\n", pr.HeadRepo)
	}

	wm := worktree.New(r, *root)
	wt, err := wm.Ensure(ctx, cwd, pr)
	if err != nil {
		if errors.Is(err, worktree.ErrDirtyWorktree) {
			return fmt.Errorf("%w\n  the worktree holds changes that are not mine; resolve them or remove it manually", err)
		}
		return err
	}
	switch {
	case wt.Created:
		fmt.Printf("  worktree created at %s\n", wt.Path)
	case wt.Refreshed:
		fmt.Printf("  worktree refreshed to %s\n", short(wt.HeadSHA))
	default:
		fmt.Printf("  worktree current at %s\n", short(wt.HeadSHA))
	}

	prov := artifact.Provenance{
		Repo:          pr.Repo,
		PRNumber:      pr.Number,
		BaseSHA:       pr.BaseSHA,
		HeadSHA:       pr.HeadSHA,
		RubricVersion: runner.RubricVersion,
		Model:         *model,
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

	res, err := run.Run(ctx, artifactDir, wt.Path, prov)
	if err != nil {
		return fmt.Errorf("review failed: %w", err)
	}
	if res.FromCache {
		fmt.Println("  reusing cached review")
	} else {
		if res.StaleReason != "" {
			fmt.Printf("  re-reviewing: %s\n", res.StaleReason)
		}
		fmt.Println("  review complete")
	}

	reviewPath := filepath.Join(artifactDir, "review.md")
	md := render.Markdown(res.Review, pr.Title)
	if err := os.WriteFile(reviewPath, []byte(md), 0o644); err != nil {
		return err
	}

	summarize(res.Review)
	fmt.Printf("\n  worktree: %s\n  review:   %s\n", wt.Path, reviewPath)
	if sess, err := artifact.ReadSession(artifactDir); err == nil && sess.ResumeCommand != "" {
		fmt.Printf("  resume:   %s\n", sess.ResumeCommand)
	}

	if *open {
		if _, err := r.Run(ctx, "", editorBin(), wt.Path, reviewPath); err != nil {
			fmt.Fprintf(os.Stderr, "  (could not open the editor: %v)\n", err)
		}
	}
	return nil
}

func summarize(r *artifact.Review) {
	var e, w, i int
	for _, f := range r.Findings {
		switch f.Severity {
		case artifact.SeverityError:
			e++
		case artifact.SeverityWarning:
			w++
		default:
			i++
		}
	}
	fmt.Printf("\n  %d error, %d warning, %d info\n", e, w, i)
	for _, f := range r.Findings {
		if f.Severity == artifact.SeverityInfo {
			continue
		}
		loc := f.Location.Path
		if f.Location.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.Location.Path, f.Location.Line)
		}
		fmt.Printf("    %-7s %s  %s\n", f.Severity, loc, f.Message)
	}
}

// editorBin locates the editor launcher.
//
// `code` is commonly a shell alias rather than a binary on PATH, and pr-buddy
// never invokes a shell, so an alias is invisible to it. Fall back to the known
// macOS install location before giving up.
func editorBin() string {
	if v := os.Getenv("PR_BUDDY_EDITOR"); v != "" {
		return v
	}
	if path, err := exec.LookPath("code"); err == nil {
		return path
	}
	const macOS = "/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code"
	if _, err := os.Stat(macOS); err == nil {
		return macOS
	}
	return "code"
}

func defaultRoot() string {
	if v := os.Getenv("PR_BUDDY_ROOT"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".pr-buddy"
	}
	return filepath.Join(home, ".pr-buddy", "worktrees")
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func usage() {
	fmt.Fprintf(os.Stderr, `pr-buddy prepares a pull request for review.

usage: pr-buddy [flags] <pr-number>

Pull request code is treated as untrusted. Nothing from the pull request is
built, installed, or executed, and the review runs without write access.

flags:
`)
	flag.PrintDefaults()
}
