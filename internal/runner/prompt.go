package runner

import (
	"fmt"

	"github.com/anubhavitis/pr-buddy/internal/artifact"
)

// Prompt builds the review instruction for a pull request.
//
// The rubric is versioned by RubricVersion; changing this text without bumping
// that constant would silently serve reviews produced under a different rubric.
func Prompt(p artifact.Provenance) string {
	return fmt.Sprintf(`Review pull request #%d in %s.

The working directory is a detached checkout of the pull request head (%s).
Its base is %s. Review only what this pull request changes: run
`+"`git diff %s...%s`"+` conceptually by reading the changed files, and judge the
change, not the pre-existing codebase.

You have read-only access. Do not attempt to modify files or run commands.

Report a finding only when you can point to the specific code that is wrong and
say what goes wrong because of it. A finding that would apply to almost any
codebase is not worth reporting. Prefer a short list of substantiated problems
over broad coverage.

Severity:
- error: a defect that will cause incorrect behaviour, data loss, or a security
  problem.
- warning: a real problem that is likely to cause trouble but is conditional or
  recoverable.
- info: worth the author's attention but not a defect.

Also produce a reading guide: an ordered set of groups that tells a reviewer
which files to read, in what order, and why. Order by what a reviewer must
understand first, not by file path.

Respond with a single JSON object and nothing else:

{
  "summary": "two or three sentences on what this change does and where the risk is",
  "findings": [
    {
      "severity": "error|warning|info",
      "rule": "short-kebab-case-category",
      "message": "what is wrong, in one sentence",
      "path": "path/relative/to/repo/root.go",
      "line": 42,
      "end_line": 45,
      "evidence": "the concrete reason: the input that breaks it, the conflicting call site, the unhandled branch",
      "confidence": 0.0,
      "reading_group": "name of the group this belongs to"
    }
  ],
  "reading_guide": [
    {
      "name": "group name",
      "summary": "why this group matters and what to look for",
      "paths": ["path/one.go", "path/two.go"]
    }
  ]
}

Use paths relative to the repository root. Set confidence between 0 and 1,
reflecting how sure you are the finding is real. Return an empty findings array
if the change is sound.`,
		p.PRNumber, p.Repo, shortSHA(p.HeadSHA), shortSHA(p.BaseSHA),
		shortSHA(p.BaseSHA), shortSHA(p.HeadSHA))
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
