// Package render turns a review artifact into human-readable output. Rendering
// is one-way: the artifact is the source of truth and is never reconstructed
// from rendered text.
package render

import (
	"fmt"
	"strings"

	"github.com/anubhavitis/pr-buddy/internal/artifact"
)

// Markdown renders a review for reading beside the diff.
//
// Findings are written as `path:line` so an editor can turn them into clickable
// locations. Session identity is deliberately absent: it belongs to the session
// artifact, not to anything a reviewer might paste into a comment.
func Markdown(r *artifact.Review, title string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Review: %s#%d\n\n", r.Provenance.Repo, r.Provenance.PRNumber)
	if title != "" {
		fmt.Fprintf(&b, "**%s**\n\n", title)
	}
	if r.Summary != "" {
		fmt.Fprintf(&b, "%s\n\n", r.Summary)
	}

	if r.Status != artifact.StatusComplete {
		fmt.Fprintf(&b, "> Status: **%s**\n", r.Status)
		if r.Failure != nil {
			fmt.Fprintf(&b, ">\n> %s: %s\n", r.Failure.Kind, r.Failure.Message)
		}
		b.WriteString("\n")
		return b.String()
	}

	counts := countBySeverity(r.Findings)
	fmt.Fprintf(&b, "%d error, %d warning, %d info — reviewed `%s` against `%s`\n\n",
		counts[artifact.SeverityError], counts[artifact.SeverityWarning], counts[artifact.SeverityInfo],
		short(r.Provenance.HeadSHA), short(r.Provenance.BaseSHA))

	if len(r.Findings) == 0 {
		b.WriteString("No findings.\n\n")
	} else {
		b.WriteString("## Findings\n\n")
		for _, f := range r.Findings {
			fmt.Fprintf(&b, "### %s %s\n\n", marker(f.Severity), f.Message)
			fmt.Fprintf(&b, "`%s`", location(f.Location))
			if f.Rule != "" {
				fmt.Fprintf(&b, " · %s", f.Rule)
			}
			fmt.Fprintf(&b, " · confidence %.0f%%\n\n", f.Confidence*100)
			if f.Evidence != "" {
				fmt.Fprintf(&b, "%s\n\n", f.Evidence)
			}
		}
	}

	if len(r.ReadingGuide) > 0 {
		b.WriteString("## Reading guide\n\n")
		for i, g := range r.ReadingGuide {
			fmt.Fprintf(&b, "%d. **%s**", i+1, g.Name)
			if g.Summary != "" {
				fmt.Fprintf(&b, " — %s", g.Summary)
			}
			b.WriteString("\n")
			for _, p := range g.Paths {
				fmt.Fprintf(&b, "   - `%s`\n", p)
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

func location(l artifact.Location) string {
	if l.Line > 0 {
		return fmt.Sprintf("%s:%d", l.Path, l.Line)
	}
	return l.Path
}

func marker(s artifact.Severity) string {
	switch s {
	case artifact.SeverityError:
		return "[error]"
	case artifact.SeverityWarning:
		return "[warn]"
	default:
		return "[info]"
	}
}

func countBySeverity(findings []artifact.Finding) map[artifact.Severity]int {
	m := map[artifact.Severity]int{}
	for _, f := range findings {
		m[f.Severity]++
	}
	return m
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
