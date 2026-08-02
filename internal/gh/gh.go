// Package gh reads pull request metadata from GitHub. It only ever reads;
// nothing here comments, approves, or merges.
package gh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
)

// State is the GitHub lifecycle state of a pull request.
type State string

const (
	StateOpen   State = "OPEN"
	StateClosed State = "CLOSED"
	StateMerged State = "MERGED"
)

// PR is the metadata pr-buddy needs to review a pull request. BaseSHA and
// HeadSHA are the revisions GitHub itself reports, not whatever happens to be
// checked out locally.
type PR struct {
	Number   int
	Repo     string // "owner/name" of the repository the PR targets
	Title    string
	State    State
	BaseRef  string
	BaseSHA  string
	HeadRef  string
	HeadSHA  string
	IsFork   bool
	HeadRepo string // "owner/name" of the head repository; differs when forked
}

// ErrNotFound reports that the pull request does not exist or is not visible.
var ErrNotFound = errors.New("pull request not found")

// Client reads pull request data via the gh CLI.
type Client struct {
	Runner xexec.Runner
}

// New returns a Client using the real gh binary.
func New(r xexec.Runner) *Client { return &Client{Runner: r} }

// rawPR mirrors the subset of `gh pr view --json` output that we consume.
type rawPR struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	State          string `json:"state"`
	BaseRefName    string `json:"baseRefName"`
	BaseRefOid     string `json:"baseRefOid"`
	HeadRefName    string `json:"headRefName"`
	HeadRefOid     string `json:"headRefOid"`
	IsCrossRepo    bool   `json:"isCrossRepository"`
	HeadRepository *struct {
		Name  string `json:"name"`
		Owner *struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"headRepository"`
	HeadRepositoryOwner *struct {
		Login string `json:"login"`
	} `json:"headRepositoryOwner"`
}

const prFields = "number,title,state,baseRefName,baseRefOid,headRefName,headRefOid," +
	"isCrossRepository,headRepository,headRepositoryOwner"

// ViewPR fetches metadata for a pull request in the given repository. dir is
// the local repository used to resolve gh's context.
func (c *Client) ViewPR(ctx context.Context, dir, repo string, number int) (*PR, error) {
	args := []string{"pr", "view", fmt.Sprint(number), "--json", prFields}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	out, err := c.Runner.Run(ctx, dir, "gh", args...)
	if err != nil {
		if isNotFound(err, out) {
			return nil, fmt.Errorf("%w: %s#%d", ErrNotFound, repo, number)
		}
		return nil, err
	}

	var raw rawPR
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parsing gh pr view output: %w", err)
	}
	if raw.HeadRefOid == "" || raw.BaseRefOid == "" {
		return nil, fmt.Errorf("gh returned a pull request without base or head revision")
	}

	pr := &PR{
		Number:  raw.Number,
		Repo:    repo,
		Title:   raw.Title,
		State:   State(strings.ToUpper(raw.State)),
		BaseRef: raw.BaseRefName,
		BaseSHA: raw.BaseRefOid,
		HeadRef: raw.HeadRefName,
		HeadSHA: raw.HeadRefOid,
		IsFork:  raw.IsCrossRepo,
	}
	if raw.HeadRepository != nil {
		owner := ""
		switch {
		case raw.HeadRepository.Owner != nil:
			owner = raw.HeadRepository.Owner.Login
		case raw.HeadRepositoryOwner != nil:
			owner = raw.HeadRepositoryOwner.Login
		}
		if owner != "" {
			pr.HeadRepo = owner + "/" + raw.HeadRepository.Name
		}
	}
	return pr, nil
}

// CurrentRepo reports the "owner/name" of the repository containing dir.
func (c *Client) CurrentRepo(ctx context.Context, dir string) (string, error) {
	out, err := c.Runner.Run(ctx, dir, "gh", "repo", "view", "--json", "nameWithOwner")
	if err != nil {
		return "", err
	}
	var raw struct {
		NameWithOwner string `json:"nameWithOwner"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return "", fmt.Errorf("parsing gh repo view output: %w", err)
	}
	if raw.NameWithOwner == "" {
		return "", errors.New("gh did not report a repository name")
	}
	return raw.NameWithOwner, nil
}

func isNotFound(err error, out string) bool {
	var xe *xexec.Error
	if errors.As(err, &xe) {
		s := strings.ToLower(xe.Stderr)
		if strings.Contains(s, "could not resolve to a pullrequest") ||
			strings.Contains(s, "no pull requests found") ||
			strings.Contains(s, "not found") {
			return true
		}
	}
	return strings.Contains(strings.ToLower(out), "could not resolve to a pullrequest")
}
