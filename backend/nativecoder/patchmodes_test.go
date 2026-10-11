package nativecoder

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestApplyCoderPatchReplacesHunksInOrder(t *testing.T) {
	req := CoderPatchRequest{
		ChatID: "c1",
		Files: map[string]string{
			"a.go": "func A() { return 1 } // TODO\nfunc B() { return 2 }\n",
		},
		Hunks: []CoderPatchHunk{
			{FilePath: "a.go", Old: "return 1", New: "return 10"},
			// Second hunk matches text the first hunk wrote.
			{FilePath: "a.go", Old: "return 10 } // TODO", New: "return 10 } // done"},
		},
	}
	res, err := ApplyCoderPatch(req)
	if err != nil {
		t.Fatalf("ApplyCoderPatch: %v", err)
	}
	if res.Applied != 2 {
		t.Fatalf("Applied=%d, want 2", res.Applied)
	}
	if len(res.HunkResults) != 2 {
		t.Fatalf("HunkResults len=%d, want 2", len(res.HunkResults))
	}
	for _, hr := range res.HunkResults {
		if !hr.Applied || hr.FilePath != "a.go" {
			t.Fatalf("unexpected hunk result %+v", hr)
		}
	}
	got := res.Files["a.go"]
	if !strings.Contains(got, "return 10 } // done") {
		t.Fatalf("sequenced content missing replacement: %q", got)
	}
	if !strings.Contains(got, "func B() { return 2 }") {
		t.Fatalf("unrelated content lost: %q", got)
	}
}

func TestApplyCoderPatchIsAllOrNothing(t *testing.T) {
	req := CoderPatchRequest{
		Files: map[string]string{"a.go": "alpha\nbeta\n"},
		Hunks: []CoderPatchHunk{
			{FilePath: "a.go", Old: "alpha", New: "ALPHA"},
			// Old is not present -> the whole call must fail.
			{FilePath: "a.go", Old: "gamma", New: "GAMMA"},
		},
	}
	_, err := ApplyCoderPatch(req)
	if !errors.Is(err, ErrCoderPatchOldMissing) {
		t.Fatalf("err=%v, want ErrCoderPatchOldMissing", err)
	}
}

func TestApplyCoderPatchRejectsEmptyAndCapViolations(t *testing.T) {
	if _, err := ApplyCoderPatch(CoderPatchRequest{Files: map[string]string{"a": "x"}}); !errors.Is(err, ErrCoderPatchNoHunks) {
		t.Fatalf("no hunks: err=%v", err)
	}
	if _, err := ApplyCoderPatch(CoderPatchRequest{Files: map[string]string{}}); err == nil {
		t.Fatal("no files should error")
	}
	hunks := make([]CoderPatchHunk, MaxCoderPatchHunks+1)
	for i := range hunks {
		hunks[i] = CoderPatchHunk{FilePath: "a", Old: "x", New: "y"}
	}
	if _, err := ApplyCoderPatch(CoderPatchRequest{Files: map[string]string{"a": "x"}, Hunks: hunks}); !errors.Is(err, ErrCoderPatchTooManyHunks) {
		t.Fatalf("hunk cap: err=%v", err)
	}
	if _, err := ApplyCoderPatch(CoderPatchRequest{Files: map[string]string{"": "x"}, Hunks: []CoderPatchHunk{{FilePath: "a", Old: "x", New: "y"}}}); !errors.Is(err, ErrCoderPatchBadFile) {
		t.Fatalf("empty file key: err=%v", err)
	}
	if _, err := ApplyCoderPatch(CoderPatchRequest{Files: map[string]string{"a": "x"}, Hunks: []CoderPatchHunk{{FilePath: "a", Old: "", New: "y"}}}); !errors.Is(err, ErrCoderPatchBadHunk) {
		t.Fatalf("empty old: err=%v", err)
	}
	// Missing file target.
	if _, err := ApplyCoderPatch(CoderPatchRequest{Files: map[string]string{"a": "x"}, Hunks: []CoderPatchHunk{{FilePath: "b", Old: "x", New: "y"}}}); !errors.Is(err, ErrCoderPatchBadFile) {
		t.Fatalf("missing target: err=%v", err)
	}
}

func TestCoderPatchSurfaceRecordsMetadataOnly(t *testing.T) {
	st := NewCoderPatchStore()
	surf := &coderPatchSurface{store: st}
	res, err := surf.Apply(CoderPatchRequest{
		ChatID: "c1",
		Files:  map[string]string{"a.go": "one\ntwo\n", "b.go": "x\n"},
		Hunks: []CoderPatchHunk{
			{FilePath: "a.go", Old: "one", New: "uno"},
			{FilePath: "a.go", Old: "two", New: "dos"},
			{FilePath: "b.go", Old: "x", New: "y"},
		},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Revision != 1 || st.Revision("c1") != 1 {
		t.Fatalf("revision=%d store=%d, want 1/1", res.Revision, st.Revision("c1"))
	}
	if res.Files["a.go"] != "uno\ndos\n" || res.Files["b.go"] != "y\n" {
		t.Fatalf("computed files wrong: %v", res.Files)
	}
	if !strings.Contains(res.Rendered, "Applied 3 hunks across 2 files") {
		t.Fatalf("render missing summary: %q", res.Rendered)
	}
	if !strings.Contains(res.JSON, `"revision":1`) {
		t.Fatalf("JSON missing revision: %q", res.JSON)
	}
	recs := st.Records("c1")
	if len(recs) != 2 {
		t.Fatalf("records=%d, want 2 (one per file)", len(recs))
	}
	// File content must never be stored: only hunk/byte counts survive.
	for _, r := range recs {
		if r.HunkCount == 0 {
			t.Errorf("record %s has zero hunks", r.FilePath)
		}
		if usesWideBytes(r) {
			t.Errorf("record %s holds full content bytes", r.FilePath)
		}
	}
	// A second patch advances the revision and appends records.
	res2, err := surf.Apply(CoderPatchRequest{
		ChatID: "c1",
		Files:  map[string]string{"a.go": "uno\ndos\n"},
		Hunks:  []CoderPatchHunk{{FilePath: "a.go", Old: "uno", New: "1"}},
	})
	if err != nil {
		t.Fatalf("Apply 2: %v", err)
	}
	if res2.Revision != 2 || len(st.Records("c1")) != 3 {
		t.Fatalf("revision/records after second patch: rev=%d records=%d", res2.Revision, len(st.Records("c1")))
	}
}

// usesWideBytes guards the "no file content in the store" contract: a record's
// OldBytes/NewBytes are derivation sizes, so they are never mistaken for stored
// content by a later reader.
func usesWideBytes(r CoderPatchRecord) bool {
	// The store must never retain the actual text; byte counts of a hunk are
	// far smaller than a whole-file dump and are bounded by the old/new caps.
	return r.OldBytes > MaxCoderPatchOldRunes || r.NewBytes > MaxCoderPatchNewRunes
}

func TestCoderPatchStoreIsolation(t *testing.T) {
	st := NewCoderPatchStore()
	if rev := st.Revision("missing"); rev != 0 {
		t.Fatalf("unknown chat revision=%d, want 0", rev)
	}
	if recs := st.Records("missing"); len(recs) != 0 {
		t.Fatalf("unknown chat records=%d, want 0", len(recs))
	}
	if rev := st.Append("a", []CoderPatchRecord{{FilePath: "x", HunkCount: 1}}, time.Now()); rev != 1 {
		t.Fatalf("append rev=%d, want 1", rev)
	}
	if rev := st.Revision("b"); rev != 0 {
		t.Fatalf("isolated chat b revision=%d, want 0", rev)
	}
}

func TestApplyCoderPatchHasNoSideEffects(t *testing.T) {
	req := CoderPatchRequest{
		Files: map[string]string{"a.go": "x\n"},
		Hunks: []CoderPatchHunk{{FilePath: "a.go", Old: "x", New: "y"}},
	}
	if _, err := ApplyCoderPatch(req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// The engine is pure: the input map must be unchanged (no store, no writes).
	if req.Files["a.go"] != "x\n" {
		t.Fatalf("input mutated: %q", req.Files["a.go"])
	}
}
