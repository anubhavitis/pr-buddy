package artifact

import (
	"path/filepath"
	"testing"
)

func TestMarkAndUnmark(t *testing.T) {
	dir := t.TempDir()
	p := &Progress{}

	p.Mark("a.go", "blob-1")
	if !p.Reviewed("a.go", "blob-1") {
		t.Error("marked file not reported as reviewed")
	}
	if err := WriteProgress(dir, p); err != nil {
		t.Fatal(err)
	}

	loaded, err := ReadProgress(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Reviewed("a.go", "blob-1") {
		t.Error("mark did not survive a round trip")
	}

	loaded.Unmark("a.go")
	if loaded.Reviewed("a.go", "blob-1") {
		t.Error("unmark did not clear the mark")
	}
}

// The whole point of keying on content: a file the author changed after it was
// reviewed is no longer reviewed, and saying otherwise would be a lie of exactly
// the kind the stale-review handling exists to prevent.
func TestMarkDoesNotSurviveAContentChange(t *testing.T) {
	p := &Progress{}
	p.Mark("a.go", "blob-1")

	if p.Reviewed("a.go", "blob-2") {
		t.Error("mark survived a change to the file's content")
	}
	// The file is untouched at its original content, so the mark still holds.
	if !p.Reviewed("a.go", "blob-1") {
		t.Error("mark lost for unchanged content")
	}
}

// A push that touches two files must not cost the reviewer the other five.
func TestMarksAreIndependentPerFile(t *testing.T) {
	p := &Progress{}
	p.Mark("kept.go", "blob-1")
	p.Mark("changed.go", "blob-1")

	if !p.Reviewed("kept.go", "blob-1") {
		t.Error("untouched file lost its mark")
	}
	if p.Reviewed("changed.go", "blob-2") {
		t.Error("changed file kept its mark")
	}
}

// An unreviewed file is simply one with no mark; nothing needs to record that.
func TestUnmarkedFileIsNotReviewed(t *testing.T) {
	p := &Progress{}
	if p.Reviewed("never-seen.go", "blob-1") {
		t.Error("a file with no mark reported as reviewed")
	}
}

// Progress is per pull request, and lives beside the review it belongs to.
func TestReadProgressIsEmptyWhenAbsent(t *testing.T) {
	p, err := ReadProgress(t.TempDir())
	if err != nil {
		t.Fatalf("a pull request with no progress yet is not an error: %v", err)
	}
	if p == nil {
		t.Fatal("ReadProgress returned nil")
	}
	if p.Reviewed("a.go", "blob-1") {
		t.Error("empty progress reported a reviewed file")
	}
}

// Progress is the reviewer's own record, not derived from the review, so
// regenerating a review must not disturb it.
func TestProgressIsStoredSeparatelyFromTheReview(t *testing.T) {
	dir := t.TempDir()
	p := &Progress{}
	p.Mark("a.go", "blob-1")
	if err := WriteProgress(dir, p); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadReview(dir); err == nil {
		t.Fatal("writing progress created a review artifact")
	}
	if _, err := ReadProgress(dir); err != nil {
		t.Fatalf("progress not readable on its own: %v", err)
	}
	if _, err := ReadProgress(filepath.Dir(dir)); err != nil {
		t.Fatalf("a directory without progress must read as empty: %v", err)
	}
}

func TestCountsReviewedFiles(t *testing.T) {
	p := &Progress{}
	p.Mark("a.go", "blob-1")
	p.Mark("b.go", "blob-2")
	p.Mark("c.go", "blob-3")
	p.Unmark("b.go")

	got := p.CountReviewed(map[string]string{
		"a.go": "blob-1", // reviewed
		"b.go": "blob-2", // unmarked
		"c.go": "changed", // marked, but the content moved
		"d.go": "blob-4", // never marked
	})
	if got != 1 {
		t.Errorf("CountReviewed = %d, want 1", got)
	}
}
