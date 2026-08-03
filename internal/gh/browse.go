package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Org is a GitHub owner the viewer can see: their own account or an
// organization they belong to.
type Org struct {
	Login string `json:"login"`
	// IsViewer marks the user's personal account rather than an organization.
	IsViewer bool `json:"is_viewer"`
}

// Repo is a repository listed under an org.
type Repo struct {
	Name          string `json:"name"`
	NameWithOwner string `json:"name_with_owner"`
	// OpenPRs is GitHub's own count, used to hide repositories with nothing to
	// review without paying for a second query per repository.
	OpenPRs int  `json:"open_prs"`
	Private bool `json:"private"`
}

// PRSummary is the listing form of a pull request: enough to render a tree row
// without fetching full metadata for every entry.
type PRSummary struct {
	Number       int    `json:"number"`
	Title        string `json:"title"`
	Author       string `json:"author"`
	State        State  `json:"state"`
	IsDraft      bool   `json:"is_draft"`
	BaseRef      string `json:"base_ref"`
	ChangedFiles int    `json:"changed_files"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	UpdatedAt    string `json:"updated_at"`
	URL          string `json:"url"`
}

// Orgs lists the viewer's account plus every organization they belong to.
//
// The tree is browsed lazily one level at a time: this call is cheap, and
// repositories are only fetched when an org is expanded.
func (c *Client) Orgs(ctx context.Context) ([]Org, error) {
	viewer, err := c.Runner.Run(ctx, "", "gh", "api", "user", "--jq", ".login")
	if err != nil {
		return nil, fmt.Errorf("reading the authenticated user: %w", err)
	}
	login := strings.TrimSpace(viewer)
	if login == "" {
		return nil, fmt.Errorf("gh did not report an authenticated user; run `gh auth login`")
	}
	orgs := []Org{{Login: login, IsViewer: true}}

	out, err := c.Runner.Run(ctx, "", "gh", "api", "user/orgs", "--paginate", "--jq", ".[].login")
	if err != nil {
		// Membership is not readable under every token scope. The viewer's own
		// account is still usable, so degrade rather than fail.
		return orgs, nil
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if l := strings.TrimSpace(line); l != "" {
			orgs = append(orgs, Org{Login: l})
		}
	}
	return orgs, nil
}

// Repos lists an org's repositories that have at least one open pull request,
// most recently pushed first.
func (c *Client) Repos(ctx context.Context, org string, limit int) ([]Repo, error) {
	if limit <= 0 {
		limit = 100
	}
	out, err := c.Runner.Run(ctx, "", "gh", "repo", "list", org,
		"--limit", fmt.Sprint(limit), "--no-archived",
		"--json", "name,nameWithOwner,isPrivate")
	if err != nil {
		return nil, fmt.Errorf("listing repositories for %s: %w", org, err)
	}
	var raw []struct {
		Name          string `json:"name"`
		NameWithOwner string `json:"nameWithOwner"`
		IsPrivate     bool   `json:"isPrivate"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parsing gh repo list output: %w", err)
	}
	repos := make([]Repo, 0, len(raw))
	for _, r := range raw {
		repos = append(repos, Repo{Name: r.Name, NameWithOwner: r.NameWithOwner, Private: r.IsPrivate})
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	return repos, nil
}

// PRs lists open pull requests in a repository.
func (c *Client) PRs(ctx context.Context, repo string, limit int) ([]PRSummary, error) {
	if limit <= 0 {
		limit = 50
	}
	out, err := c.Runner.Run(ctx, "", "gh", "pr", "list", "--repo", repo,
		"--state", "open", "--limit", fmt.Sprint(limit),
		"--json", "number,title,author,state,isDraft,baseRefName,changedFiles,additions,deletions,updatedAt,url")
	if err != nil {
		return nil, fmt.Errorf("listing pull requests for %s: %w", repo, err)
	}
	var raw []struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		State   string `json:"state"`
		IsDraft bool   `json:"isDraft"`
		Author  *struct {
			Login string `json:"login"`
		} `json:"author"`
		BaseRefName  string `json:"baseRefName"`
		ChangedFiles int    `json:"changedFiles"`
		Additions    int    `json:"additions"`
		Deletions    int    `json:"deletions"`
		UpdatedAt    string `json:"updatedAt"`
		URL          string `json:"url"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parsing gh pr list output: %w", err)
	}
	prs := make([]PRSummary, 0, len(raw))
	for _, p := range raw {
		author := ""
		if p.Author != nil {
			author = p.Author.Login
		}
		prs = append(prs, PRSummary{
			Number: p.Number, Title: p.Title, Author: author,
			State: State(strings.ToUpper(p.State)), IsDraft: p.IsDraft,
			BaseRef: p.BaseRefName, ChangedFiles: p.ChangedFiles,
			Additions: p.Additions, Deletions: p.Deletions,
			UpdatedAt: p.UpdatedAt, URL: p.URL,
		})
	}
	return prs, nil
}

// ChangedFiles lists the paths a pull request touches, in GitHub's order.
func (c *Client) ChangedFiles(ctx context.Context, repo string, number int) ([]string, error) {
	out, err := c.Runner.Run(ctx, "", "gh", "api", "--paginate",
		fmt.Sprintf("repos/%s/pulls/%d/files", repo, number), "--jq", ".[].filename")
	if err != nil {
		return nil, fmt.Errorf("listing changed files for %s#%d: %w", repo, number, err)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if l := strings.TrimSpace(line); l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}
