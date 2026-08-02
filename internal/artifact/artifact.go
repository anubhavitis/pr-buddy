// Package artifact defines the machine-readable review artifact that is the
// single source of truth for a review. Human-readable output and any future
// editor integration are derived representations, never the source.
package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Version is the artifact format version. Bump on any breaking schema change;
// a mismatch invalidates the cache.
const Version = 1

// Status is the lifecycle state of a review run.
type Status string

const (
	StatusPending  Status = "pending"
	StatusRunning  Status = "running"
	StatusComplete Status = "complete"
	StatusFailed   Status = "failed"
)

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusRunning, StatusComplete, StatusFailed:
		return true
	}
	return false
}

// Severity ranks a finding. Only Error and Warning count toward the pilot's
// useful-finding rate.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

func (s Severity) Valid() bool {
	switch s {
	case SeverityError, SeverityWarning, SeverityInfo:
		return true
	}
	return false
}

// rank orders severities for display. Lower sorts first.
func (s Severity) rank() int {
	switch s {
	case SeverityError:
		return 0
	case SeverityWarning:
		return 1
	case SeverityInfo:
		return 2
	}
	return 3
}

// Location points at reviewed code. Path is always relative to the repository
// root so a finding stays valid across different worktree paths.
type Location struct {
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	EndLine int    `json:"end_line,omitempty"`
}

// Finding is one review observation.
type Finding struct {
	// ID is stable across runs for the same underlying issue. Derived, not
	// supplied by the model.
	ID       string   `json:"id"`
	Severity Severity `json:"severity"`
	Rule     string   `json:"rule"`
	Message  string   `json:"message"`
	Location Location `json:"location"`
	// Evidence is the concrete reason the finding holds: the failing input,
	// the conflicting call site, the missed branch.
	Evidence string `json:"evidence,omitempty"`
	// Confidence in [0,1]. The runner does not act on it; the reviewer does.
	Confidence float64 `json:"confidence"`
	// ReadingGroup names the guided-tour section this finding belongs to.
	ReadingGroup string `json:"reading_group,omitempty"`
}

// ReadingGroup is one stop on the guided tour. Order is meaningful.
type ReadingGroup struct {
	Name    string   `json:"name"`
	Summary string   `json:"summary,omitempty"`
	Paths   []string `json:"paths"`
}

// Provenance records exactly what was reviewed and with what. Every field here
// participates in cache invalidation.
type Provenance struct {
	Repo          string `json:"repo"` // "owner/name"
	PRNumber      int    `json:"pr_number"`
	BaseSHA       string `json:"base_sha"`
	HeadSHA       string `json:"head_sha"`
	RubricVersion string `json:"rubric_version"`
	Model         string `json:"model"`
	SchemaVersion int    `json:"schema_version"`
}

// CacheKey is a deterministic digest of everything that, when changed, must
// invalidate a stored review. Cache validity is decided by comparing this
// against a freshly computed key -- never by timestamps.
func (p Provenance) CacheKey() string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%d\x00%s\x00%s\x00%s\x00%s\x00%d",
		p.Repo, p.PRNumber, p.BaseSHA, p.HeadSHA,
		p.RubricVersion, p.Model, p.SchemaVersion)
	return hex.EncodeToString(h.Sum(nil))
}

// Review is the source-of-truth artifact for one review run.
type Review struct {
	SchemaVersion int            `json:"schema_version"`
	Status        Status         `json:"status"`
	Provenance    Provenance     `json:"provenance"`
	CacheKey      string         `json:"cache_key"`
	Findings      []Finding      `json:"findings"`
	ReadingGuide  []ReadingGroup `json:"reading_guide,omitempty"`
	Summary       string         `json:"summary,omitempty"`
	// Failure is populated only when Status is failed. Preserved for diagnosis.
	Failure   *Failure  `json:"failure,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
}

// Failure captures why a run did not complete.
type Failure struct {
	Kind    string `json:"kind"` // invocation, malformed_output, timeout, interrupted
	Message string `json:"message"`
	// Raw is the unparsed model output when Kind is malformed_output.
	Raw string `json:"raw,omitempty"`
}

// Session holds the resumable Claude session identity. Stored separately from
// review content so that regenerating a review never disturbs chat continuity,
// and so session ids never leak into rendered human-readable output.
type Session struct {
	SchemaVersion int       `json:"schema_version"`
	SessionID     string    `json:"session_id"`
	Model         string    `json:"model"`
	CacheKey      string    `json:"cache_key"`
	CreatedAt     time.Time `json:"created_at"`
}

// FindingID derives a stable identity for a finding.
//
// Line number is deliberately excluded: an edit elsewhere in the file shifts
// lines without changing the issue, and a finding whose id churns cannot be
// suppressed or tracked across runs.
func FindingID(path, rule, message string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s",
		strings.TrimSpace(path),
		strings.TrimSpace(rule),
		normalizeMessage(message))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// normalizeMessage reduces incidental wording variation between runs so the
// same issue keeps the same id.
func normalizeMessage(m string) string {
	return strings.Join(strings.Fields(strings.ToLower(m)), " ")
}
