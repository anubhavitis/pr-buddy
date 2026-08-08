// Package runner produces review artifacts. It decides whether a cached review
// still applies, invokes the model read-only when it does not, and records the
// outcome so that an interrupted run can never be mistaken for a finished one.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anubhavitis/pr-buddy/internal/artifact"
)

// RubricVersion identifies the review rubric. Bumping it invalidates every
// cached review, which is the point: a changed rubric means a changed review.
const RubricVersion = "code-review@1"

// Reviewer performs one read-only review of a prepared worktree.
//
// No session id is passed in: the identity of a conversation is issued by the
// reviewer and reported back, because an id asserted by the caller is consumed
// on first use and can never be resumed.
type Reviewer interface {
	Review(ctx context.Context, dir, prompt string) (*ClaudeResult, error)
}

// Clock supplies the current time, injected so tests stay deterministic.
type Clock func() time.Time

// Runner produces and caches review artifacts.
type Runner struct {
	Reviewer Reviewer
	Now      Clock
	// Timeout bounds a single review invocation. Zero means no bound.
	Timeout time.Duration
}

// Result reports what a Run produced.
type Result struct {
	Review *artifact.Review
	// FromCache reports that no model invocation occurred.
	FromCache bool
	// StaleReason explains why the cache was not used. Empty on a cache hit.
	StaleReason string
}

// Run returns a review for the given provenance, reusing a valid cached result
// when one exists.
//
// artifactDir holds the stored review; worktreeDir is the checkout under
// review. On failure a failed artifact is persisted so the reason survives.
func (r *Runner) Run(ctx context.Context, artifactDir, worktreeDir string, prov artifact.Provenance) (*Result, error) {
	prov.SchemaVersion = artifact.Version
	if prov.RubricVersion == "" {
		prov.RubricVersion = RubricVersion
	}
	now := r.now()

	cached, err := artifact.ReadReview(artifactDir)
	if err != nil && !errors.Is(err, artifact.ErrNotFound) {
		// An unreadable artifact is treated as absent rather than fatal: the
		// worst case is one extra review.
		cached = nil
	}
	if cached.Usable(prov) {
		return &Result{Review: cached, FromCache: true}, nil
	}
	// A first-ever run has no cache to be stale against, and reporting one makes
	// callers announce a re-review of something never reviewed.
	var reason string
	if cached != nil {
		reason = cached.StaleReason(prov)
	}

	// Record that a run is in flight. Because this status is neither complete
	// nor valid-as-cache, an interruption here leaves an artifact that can
	// never be served.
	running := &artifact.Review{
		Status:     artifact.StatusRunning,
		Provenance: prov,
		StartedAt:  now,
	}
	_ = writeStatus(artifactDir, running)

	runCtx := ctx
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}

	// No session id is supplied: the reviewer lets the CLI issue one, because an
	// id we assert here would be consumed and could never be resumed.
	res, err := r.Reviewer.Review(runCtx, worktreeDir, Prompt(prov))
	if err != nil {
		failed := &artifact.Review{
			Status:     artifact.StatusFailed,
			Provenance: prov,
			StartedAt:  now,
			EndedAt:    r.now(),
			Failure:    classify(err),
		}
		_ = writeStatus(artifactDir, failed)
		return nil, err
	}

	findings, guide, summary, err := ParseReview(res.Raw)
	if err != nil {
		failed := &artifact.Review{
			Status:     artifact.StatusFailed,
			Provenance: prov,
			StartedAt:  now,
			EndedAt:    r.now(),
			Failure:    &artifact.Failure{Kind: "malformed_output", Message: err.Error(), Raw: truncate(res.Raw, 8000)},
		}
		_ = writeStatus(artifactDir, failed)
		return nil, err
	}

	review := &artifact.Review{
		Status:       artifact.StatusComplete,
		Provenance:   prov,
		Findings:     findings,
		ReadingGuide: guide,
		Summary:      summary,
		StartedAt:    now,
		EndedAt:      r.now(),
	}
	if err := artifact.WriteReview(artifactDir, review); err != nil {
		return nil, err
	}
	model := res.Model
	if model == "" {
		// The reviewer could not report what served the request; the model we
		// asked for is a better record than nothing.
		model = prov.Model
	}
	if err := artifact.WriteSession(artifactDir, &artifact.Session{
		SessionID:     res.SessionID,
		Model:         model,
		ResumeCommand: ResumeCommand(res.SessionID),
		CacheKey:      prov.CacheKey(),
		CreatedAt:     r.now(),
	}); err != nil {
		return nil, err
	}
	return &Result{Review: review, StaleReason: reason}, nil
}

// writeStatus persists an in-flight or failed artifact. Such artifacts are
// intentionally not required to satisfy full validation, so the write bypasses
// WriteReview's completeness checks while remaining atomic.
func writeStatus(dir string, r *artifact.Review) error {
	r.SchemaVersion = artifact.Version
	r.Provenance.SchemaVersion = artifact.Version
	r.CacheKey = r.Provenance.CacheKey()
	return artifact.WriteRaw(dir, r)
}

func classify(err error) *artifact.Failure {
	var me *MalformedError
	switch {
	case errors.As(err, &me):
		return &artifact.Failure{Kind: "malformed_output", Message: me.Error(), Raw: truncate(me.Raw, 8000)}
	case errors.Is(err, context.DeadlineExceeded):
		return &artifact.Failure{Kind: "timeout", Message: err.Error()}
	case errors.Is(err, context.Canceled):
		return &artifact.Failure{Kind: "interrupted", Message: err.Error()}
	default:
		return &artifact.Failure{Kind: "invocation", Message: err.Error()}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n...[truncated]"
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// modelReview is the JSON contract the review prompt asks the model to produce.
type modelReview struct {
	Summary  string `json:"summary"`
	Findings []struct {
		Severity     string  `json:"severity"`
		Rule         string  `json:"rule"`
		Message      string  `json:"message"`
		Path         string  `json:"path"`
		Line         int     `json:"line"`
		EndLine      int     `json:"end_line"`
		Evidence     string  `json:"evidence"`
		Confidence   float64 `json:"confidence"`
		ReadingGroup string  `json:"reading_group"`
	} `json:"findings"`
	ReadingGuide []struct {
		Name    string   `json:"name"`
		Summary string   `json:"summary"`
		Paths   []string `json:"paths"`
	} `json:"reading_guide"`
}

// ParseReview converts raw model output into artifact types, deriving stable
// finding ids rather than trusting the model to supply them.
func ParseReview(raw string) ([]artifact.Finding, []artifact.ReadingGroup, string, error) {
	body, err := extractJSON(raw)
	if err != nil {
		return nil, nil, "", &MalformedError{Raw: raw, Err: err}
	}
	var mr modelReview
	if err := json.Unmarshal([]byte(body), &mr); err != nil {
		return nil, nil, "", &MalformedError{Raw: raw, Err: err}
	}

	findings := make([]artifact.Finding, 0, len(mr.Findings))
	for i, f := range mr.Findings {
		sev := artifact.Severity(strings.ToLower(strings.TrimSpace(f.Severity)))
		if !sev.Valid() {
			return nil, nil, "", &MalformedError{
				Raw: raw,
				Err: fmt.Errorf("finding %d has unknown severity %q", i, f.Severity),
			}
		}
		path := strings.TrimSpace(f.Path)
		if path == "" {
			return nil, nil, "", &MalformedError{Raw: raw, Err: fmt.Errorf("finding %d has no path", i)}
		}
		conf := f.Confidence
		if conf < 0 {
			conf = 0
		}
		if conf > 1 {
			conf = 1
		}
		findings = append(findings, artifact.Finding{
			ID:           artifact.FindingID(path, f.Rule, f.Message),
			Severity:     sev,
			Rule:         strings.TrimSpace(f.Rule),
			Message:      strings.TrimSpace(f.Message),
			Location:     artifact.Location{Path: path, Line: f.Line, EndLine: f.EndLine},
			Evidence:     strings.TrimSpace(f.Evidence),
			Confidence:   conf,
			ReadingGroup: strings.TrimSpace(f.ReadingGroup),
		})
	}

	guide := make([]artifact.ReadingGroup, 0, len(mr.ReadingGuide))
	for _, g := range mr.ReadingGuide {
		guide = append(guide, artifact.ReadingGroup{
			Name:    strings.TrimSpace(g.Name),
			Summary: strings.TrimSpace(g.Summary),
			Paths:   g.Paths,
		})
	}
	return findings, guide, strings.TrimSpace(mr.Summary), nil
}

// extractJSON pulls the JSON object out of model output that may be wrapped in
// prose or a fenced code block.
func extractJSON(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("empty model output")
	}
	if fenced := betweenFence(s); fenced != "" {
		s = fenced
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return "", errors.New("no json object in model output")
	}
	return s[start : end+1], nil
}

func betweenFence(s string) string {
	const fence = "```"
	i := strings.Index(s, fence)
	if i < 0 {
		return ""
	}
	rest := s[i+len(fence):]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	}
	j := strings.Index(rest, fence)
	if j < 0 {
		return ""
	}
	return rest[:j]
}
