package render

import (
	"strings"
	"testing"
	"time"

	"github.com/anubhavitis/pr-buddy/internal/artifact"
)

func review() *artifact.Review {
	p := artifact.Provenance{
		Repo: "acme/widgets", PRNumber: 42,
		BaseSHA: "1111111111111111", HeadSHA: "2222222222222222",
		RubricVersion: "code-review@1", Model: "claude-opus-5",
		SchemaVersion: artifact.Version,
	}
	return &artifact.Review{
		SchemaVersion: artifact.Version,
		Status:        artifact.StatusComplete,
		Provenance:    p,
		CacheKey:      p.CacheKey(),
		Summary:       "Adds retry backoff.",
		Findings: []artifact.Finding{{
			ID: "abc", Severity: artifact.SeverityError, Rule: "unbounded-retry",
			Message:  "Retry loop has no ceiling",
			Location: artifact.Location{Path: "client/fetch.go", Line: 88},
			Evidence: "attempts is never compared to maxAttempts", Confidence: 0.9,
		}},
		ReadingGuide: []artifact.ReadingGroup{{
			Name: "Retry logic", Summary: "Start here", Paths: []string{"client/fetch.go"},
		}},
		StartedAt: time.Now(), EndedAt: time.Now(),
	}
}

func TestMarkdownIncludesClickableLocations(t *testing.T) {
	got := Markdown(review(), "Fix retry backoff")
	if !strings.Contains(got, "client/fetch.go:88") {
		t.Errorf("missing path:line location\n%s", got)
	}
}

func TestMarkdownIncludesFindingsAndGuide(t *testing.T) {
	got := Markdown(review(), "Fix retry backoff")
	for _, want := range []string{
		"acme/widgets#42", "Fix retry backoff", "Adds retry backoff.",
		"Retry loop has no ceiling", "attempts is never compared",
		"Reading guide", "Retry logic", "1 error",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing %q", want)
		}
	}
}

// Session identity must not appear in anything a reviewer might paste into a
// GitHub comment.
func TestMarkdownOmitsSessionIdentity(t *testing.T) {
	got := Markdown(review(), "t")
	for _, forbidden := range []string{"session", "cache_key", "session_id"} {
		if strings.Contains(strings.ToLower(got), forbidden) {
			t.Errorf("rendered output leaks %q", forbidden)
		}
	}
}

func TestMarkdownHandlesNoFindings(t *testing.T) {
	r := review()
	r.Findings = nil
	got := Markdown(r, "t")
	if !strings.Contains(got, "No findings") {
		t.Errorf("clean review not rendered clearly\n%s", got)
	}
}

func TestMarkdownSurfacesFailure(t *testing.T) {
	r := review()
	r.Status = artifact.StatusFailed
	r.Failure = &artifact.Failure{Kind: "timeout", Message: "exceeded 10m"}
	got := Markdown(r, "t")
	if !strings.Contains(got, "failed") || !strings.Contains(got, "timeout") {
		t.Errorf("failure not surfaced\n%s", got)
	}
	if strings.Contains(got, "No findings") {
		t.Error("failed review rendered as if it were clean")
	}
}

func TestMarkdownRendersLocationWithoutLine(t *testing.T) {
	r := review()
	r.Findings[0].Location.Line = 0
	got := Markdown(r, "t")
	if strings.Contains(got, "client/fetch.go:0") {
		t.Error("rendered a zero line number")
	}
	if !strings.Contains(got, "client/fetch.go") {
		t.Error("path lost when line is absent")
	}
}
