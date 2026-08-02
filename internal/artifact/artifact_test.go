package artifact

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testProvenance() Provenance {
	return Provenance{
		Repo:          "acme/widgets",
		PRNumber:      42,
		BaseSHA:       "aaaa111",
		HeadSHA:       "bbbb222",
		RubricVersion: "code-review@1",
		Model:         "claude-opus-5",
		SchemaVersion: Version,
	}
}

func completeReview() *Review {
	p := testProvenance()
	return &Review{
		SchemaVersion: Version,
		Status:        StatusComplete,
		Provenance:    p,
		CacheKey:      p.CacheKey(),
		Findings: []Finding{{
			ID:         FindingID("pkg/a.go", "nil-deref", "possible nil dereference"),
			Severity:   SeverityError,
			Rule:       "nil-deref",
			Message:    "possible nil dereference",
			Location:   Location{Path: "pkg/a.go", Line: 12},
			Confidence: 0.8,
		}},
		StartedAt: time.Now().Add(-time.Minute),
		EndedAt:   time.Now(),
	}
}

func TestCacheKeyChangesWithEveryInvalidatingField(t *testing.T) {
	base := testProvenance()
	mutations := map[string]func(*Provenance){
		"head moved":    func(p *Provenance) { p.HeadSHA = "cccc333" },
		"base moved":    func(p *Provenance) { p.BaseSHA = "dddd444" },
		"rubric bumped": func(p *Provenance) { p.RubricVersion = "code-review@2" },
		"model changed": func(p *Provenance) { p.Model = "claude-sonnet-5" },
		"schema bumped": func(p *Provenance) { p.SchemaVersion = Version + 1 },
		"repo changed":  func(p *Provenance) { p.Repo = "acme/other" },
		"pr changed":    func(p *Provenance) { p.PRNumber = 43 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			p := base
			mutate(&p)
			if p.CacheKey() == base.CacheKey() {
				t.Fatalf("cache key unchanged after %s", name)
			}
		})
	}
}

func TestCacheKeyIsDeterministic(t *testing.T) {
	a, b := testProvenance(), testProvenance()
	if a.CacheKey() != b.CacheKey() {
		t.Fatal("identical provenance produced different cache keys")
	}
}

func TestFindingIDIgnoresLineAndWording(t *testing.T) {
	a := FindingID("pkg/a.go", "nil-deref", "possible nil dereference")
	b := FindingID("pkg/a.go", "nil-deref", "  Possible   Nil Dereference  ")
	if a != b {
		t.Fatal("finding id should be stable across whitespace and case")
	}
	c := FindingID("pkg/b.go", "nil-deref", "possible nil dereference")
	if a == c {
		t.Fatal("finding id should differ across files")
	}
}

func TestUsableRequiresCompleteAndMatching(t *testing.T) {
	cur := testProvenance()

	t.Run("complete and matching", func(t *testing.T) {
		if !completeReview().Usable(cur) {
			t.Fatal("expected cache hit")
		}
	})

	t.Run("nil review", func(t *testing.T) {
		var r *Review
		if r.Usable(cur) {
			t.Fatal("nil review must not be usable")
		}
	})

	for _, st := range []Status{StatusPending, StatusRunning, StatusFailed} {
		t.Run("status "+string(st), func(t *testing.T) {
			r := completeReview()
			r.Status = st
			if r.Usable(cur) {
				t.Fatalf("status %q must not be usable", st)
			}
		})
	}

	t.Run("head moved", func(t *testing.T) {
		r := completeReview()
		moved := cur
		moved.HeadSHA = "cccc333"
		if r.Usable(moved) {
			t.Fatal("moved head must invalidate cache")
		}
	})
}

func TestStaleReasonNamesTheCause(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Provenance)
		want   string
	}{
		{"head", func(p *Provenance) { p.HeadSHA = "x" }, "pr head moved"},
		{"base", func(p *Provenance) { p.BaseSHA = "x" }, "base branch moved"},
		{"rubric", func(p *Provenance) { p.RubricVersion = "x" }, "rubric changed"},
		{"model", func(p *Provenance) { p.Model = "x" }, "model changed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cur := testProvenance()
			tc.mutate(&cur)
			if got := completeReview().StaleReason(cur); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	if got := completeReview().StaleReason(testProvenance()); got != "" {
		t.Fatalf("current review reported stale: %q", got)
	}
}

func TestValidateRejectsInconsistentArtifacts(t *testing.T) {
	cases := map[string]func(*Review){
		"bad schema":         func(r *Review) { r.SchemaVersion = 99 },
		"bad status":         func(r *Review) { r.Status = "weird" },
		"no repo":            func(r *Review) { r.Provenance.Repo = "" },
		"no head":            func(r *Review) { r.Provenance.HeadSHA = "" },
		"tampered key":       func(r *Review) { r.CacheKey = "deadbeef" },
		"failed no detail":   func(r *Review) { r.Status = StatusFailed },
		"complete no end":    func(r *Review) { r.EndedAt = time.Time{} },
		"bad severity":       func(r *Review) { r.Findings[0].Severity = "nope" },
		"no path":            func(r *Review) { r.Findings[0].Location.Path = "" },
		"bad confidence":     func(r *Review) { r.Findings[0].Confidence = 1.5 },
		"missing finding id": func(r *Review) { r.Findings[0].ID = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := completeReview()
			mutate(r)
			if err := r.Validate(); err == nil {
				t.Fatalf("expected validation error for %s", name)
			}
		})
	}
	if err := completeReview().Validate(); err != nil {
		t.Fatalf("valid review rejected: %v", err)
	}
}

func TestWriteReviewRefusesInvalid(t *testing.T) {
	dir := t.TempDir()
	r := completeReview()
	r.Findings[0].Severity = "bogus"
	if err := WriteReview(dir, r); err == nil {
		t.Fatal("expected refusal to write invalid review")
	}
	if _, err := os.Stat(filepath.Join(dir, reviewFile)); !os.IsNotExist(err) {
		t.Fatal("invalid review must not leave a file behind")
	}
}

func TestWriteReviewRoundTrips(t *testing.T) {
	dir := t.TempDir()
	want := completeReview()
	if err := WriteReview(dir, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadReview(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.CacheKey != want.CacheKey || got.Status != want.Status {
		t.Fatal("round trip lost provenance")
	}
	if len(got.Findings) != 1 || got.Findings[0].ID != want.Findings[0].ID {
		t.Fatal("round trip lost findings")
	}
	if !got.Usable(testProvenance()) {
		t.Fatal("round-tripped review should be a cache hit")
	}
}

func TestWriteLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	if err := WriteReview(dir, completeReview()); err != nil {
		t.Fatalf("write: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

// A truncated file must not parse as a complete review. This is the property
// that makes an interrupted run safe.
func TestTruncatedReviewIsNotUsable(t *testing.T) {
	dir := t.TempDir()
	if err := WriteReview(dir, completeReview()); err != nil {
		t.Fatalf("write: %v", err)
	}
	path := filepath.Join(dir, reviewFile)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b[:len(b)/2], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadReview(dir); err == nil {
		t.Fatal("truncated review parsed without error")
	}
}

func TestSessionStoredSeparatelyFromReview(t *testing.T) {
	dir := t.TempDir()
	p := testProvenance()
	if err := WriteReview(dir, completeReview()); err != nil {
		t.Fatalf("write review: %v", err)
	}
	sess := &Session{SessionID: "sess-abc", Model: p.Model, CacheKey: p.CacheKey(), CreatedAt: time.Now()}
	if err := WriteSession(dir, sess); err != nil {
		t.Fatalf("write session: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, reviewFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "sess-abc") {
		t.Fatal("session id leaked into review artifact")
	}

	got, err := ReadSession(dir)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if got.SessionID != "sess-abc" {
		t.Fatalf("got session %q", got.SessionID)
	}
}

func TestWriteSessionRejectsEmptyID(t *testing.T) {
	if err := WriteSession(t.TempDir(), &Session{}); err == nil {
		t.Fatal("expected refusal to write empty session id")
	}
}

func TestReadMissingArtifactReportsNotFound(t *testing.T) {
	if _, err := ReadReview(t.TempDir()); err != ErrNotFound {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if _, err := ReadSession(t.TempDir()); err != ErrNotFound {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestSortIsDeterministicBySeverityThenLocation(t *testing.T) {
	r := completeReview()
	r.Findings = []Finding{
		{ID: "1", Severity: SeverityInfo, Location: Location{Path: "a.go", Line: 1}, Confidence: 1},
		{ID: "2", Severity: SeverityError, Location: Location{Path: "z.go", Line: 9}, Confidence: 1},
		{ID: "3", Severity: SeverityWarning, Location: Location{Path: "b.go", Line: 2}, Confidence: 1},
		{ID: "4", Severity: SeverityError, Location: Location{Path: "a.go", Line: 5}, Confidence: 1},
	}
	r.Sort()
	var order []string
	for _, f := range r.Findings {
		order = append(order, f.ID)
	}
	want := []string{"4", "2", "3", "1"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("got order %v, want %v", order, want)
		}
	}
}

func TestArtifactIsPlainJSON(t *testing.T) {
	dir := t.TempDir()
	if err := WriteReview(dir, completeReview()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, reviewFile))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("artifact is not valid json: %v", err)
	}
	for _, k := range []string{"schema_version", "status", "provenance", "cache_key", "findings"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("artifact missing required key %q", k)
		}
	}
}
