package artifact

import (
	"path/filepath"
	"time"
)

const progressFile = "progress.json"

// Progress records which files the reviewer has read.
//
// It is the reviewer's own record, not the model's, and is stored separately
// from the review for the same reason session identity is: regenerating a
// review must not discard what the human has already done.
type Progress struct {
	SchemaVersion int `json:"schema_version"`
	// Files maps a repository-relative path to the mark it carries.
	Files map[string]Mark `json:"files,omitempty"`
}

// Mark records that one file was reviewed at one exact content.
//
// Blob is what makes the mark honest across a force-push or a new commit: it
// identifies the bytes reviewed, so a file the author has since changed cannot
// keep a mark earned by an earlier version. A path alone could not tell the
// difference.
type Mark struct {
	Blob       string    `json:"blob"`
	ReviewedAt time.Time `json:"reviewed_at"`
}

// Mark records path as reviewed at the given blob.
func (p *Progress) Mark(path, blob string) {
	if p.Files == nil {
		p.Files = map[string]Mark{}
	}
	p.Files[path] = Mark{Blob: blob, ReviewedAt: time.Now()}
}

// Unmark clears any mark on path.
func (p *Progress) Unmark(path string) {
	delete(p.Files, path)
}

// Reviewed reports whether path was reviewed at exactly the given blob. A file
// whose content has moved since it was marked is not reviewed.
func (p *Progress) Reviewed(path, blob string) bool {
	if p == nil || blob == "" {
		return false
	}
	m, ok := p.Files[path]
	return ok && m.Blob == blob
}

// CountReviewed counts how many of the given files, keyed by path to current
// blob, still carry a valid mark.
func (p *Progress) CountReviewed(current map[string]string) int {
	n := 0
	for path, blob := range current {
		if p.Reviewed(path, blob) {
			n++
		}
	}
	return n
}

// WriteProgress persists progress into dir.
func WriteProgress(dir string, p *Progress) error {
	p.SchemaVersion = Version
	return writeJSONAtomic(filepath.Join(dir, progressFile), p)
}

// ReadProgress loads the progress stored in dir. A pull request nobody has
// marked anything in yet reads as empty rather than missing, since "nothing
// reviewed yet" is the ordinary starting state.
func ReadProgress(dir string) (*Progress, error) {
	var p Progress
	err := readJSON(filepath.Join(dir, progressFile), &p)
	if err == ErrNotFound {
		return &Progress{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}
