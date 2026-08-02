package artifact

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const (
	reviewFile  = "review.json"
	sessionFile = "session.json"
)

// ErrNotFound reports that no artifact exists at the given location.
var ErrNotFound = errors.New("artifact not found")

// Validate rejects artifacts that are internally inconsistent. A review that
// fails validation is never treated as a usable cache hit.
func (r *Review) Validate() error {
	if r.SchemaVersion != Version {
		return fmt.Errorf("schema version %d, want %d", r.SchemaVersion, Version)
	}
	if !r.Status.Valid() {
		return fmt.Errorf("invalid status %q", r.Status)
	}
	if r.Provenance.Repo == "" || r.Provenance.PRNumber <= 0 {
		return errors.New("provenance missing repo or pr number")
	}
	if r.Provenance.HeadSHA == "" || r.Provenance.BaseSHA == "" {
		return errors.New("provenance missing base or head sha")
	}
	if want := r.Provenance.CacheKey(); r.CacheKey != want {
		return errors.New("cache key does not match provenance")
	}
	if r.Status == StatusFailed && r.Failure == nil {
		return errors.New("failed review has no failure detail")
	}
	if r.Status == StatusComplete && r.EndedAt.IsZero() {
		return errors.New("complete review has no end time")
	}
	for i, f := range r.Findings {
		if !f.Severity.Valid() {
			return fmt.Errorf("finding %d: invalid severity %q", i, f.Severity)
		}
		if f.Location.Path == "" {
			return fmt.Errorf("finding %d: missing path", i)
		}
		if f.Confidence < 0 || f.Confidence > 1 {
			return fmt.Errorf("finding %d: confidence %v out of range", i, f.Confidence)
		}
		if f.ID == "" {
			return fmt.Errorf("finding %d: missing id", i)
		}
	}
	return nil
}

// Usable reports whether a stored review may be served from cache. Only a
// complete, valid review whose provenance still matches counts. A run that was
// interrupted mid-flight is left as pending or running and can never look
// complete.
func (r *Review) Usable(current Provenance) bool {
	if r == nil || r.Status != StatusComplete {
		return false
	}
	if r.Validate() != nil {
		return false
	}
	return r.CacheKey == current.CacheKey()
}

// StaleReason explains why a stored review does not satisfy current
// provenance. Empty means the review is current.
func (r *Review) StaleReason(current Provenance) string {
	switch {
	case r == nil:
		return "no previous review"
	case r.Status != StatusComplete:
		return fmt.Sprintf("previous run ended in state %q", r.Status)
	case r.Provenance.HeadSHA != current.HeadSHA:
		return "pr head moved"
	case r.Provenance.BaseSHA != current.BaseSHA:
		return "base branch moved"
	case r.Provenance.RubricVersion != current.RubricVersion:
		return "rubric changed"
	case r.Provenance.Model != current.Model:
		return "model changed"
	case r.Provenance.SchemaVersion != current.SchemaVersion:
		return "artifact schema changed"
	case r.Validate() != nil:
		return "stored review is invalid"
	}
	return ""
}

// Sort orders findings by severity then path then line, so rendered output is
// deterministic across runs.
func (r *Review) Sort() {
	sort.SliceStable(r.Findings, func(i, j int) bool {
		a, b := r.Findings[i], r.Findings[j]
		if a.Severity.rank() != b.Severity.rank() {
			return a.Severity.rank() < b.Severity.rank()
		}
		if a.Location.Path != b.Location.Path {
			return a.Location.Path < b.Location.Path
		}
		return a.Location.Line < b.Location.Line
	})
}

// WriteReview atomically persists a review into dir. The write is staged to a
// temporary file and renamed, so a crash mid-write can never leave a partial
// artifact that parses as complete.
func WriteReview(dir string, r *Review) error {
	r.SchemaVersion = Version
	r.Provenance.SchemaVersion = Version
	r.CacheKey = r.Provenance.CacheKey()
	r.Sort()
	if err := r.Validate(); err != nil {
		return fmt.Errorf("refusing to write invalid review: %w", err)
	}
	return writeJSONAtomic(filepath.Join(dir, reviewFile), r)
}

// ReadReview loads the review stored in dir.
func ReadReview(dir string) (*Review, error) {
	var r Review
	if err := readJSON(filepath.Join(dir, reviewFile), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// WriteSession persists session identity separately from review content.
func WriteSession(dir string, s *Session) error {
	s.SchemaVersion = Version
	if s.SessionID == "" {
		return errors.New("refusing to write session with empty id")
	}
	return writeJSONAtomic(filepath.Join(dir, sessionFile), s)
}

// ReadSession loads the session stored in dir.
func ReadSession(dir string) (*Session, error) {
	var s Session
	if err := readJSON(filepath.Join(dir, sessionFile), &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func writeJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	// fsync before rename so the rename cannot expose a truncated file.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
