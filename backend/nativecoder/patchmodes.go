package nativecoder

// Multi-hunk patch application — opencode `patch` parity (ci#394 gap 3).
//
// `coder_edit` applies one exact-string replacement per confirmation, so a
// large refactor costs the user one confirmation per hunk. opencode's `patch`
// tool replaces a whole set of hunks in one call.
//
// This file is the core-side contract for the same capability: a pure,
// filesystem-free engine that turns an ordered list of exact-string hunks
// (each targeting one workspace-relative file path) into the resulting file
// contents, plus a per-chat store and a surface dispatcher. The integration
// performs the actual file write (it owns file access); this package owns hunk
// validation, sequential application, atomicity (all hunks or none) and the
// deterministic render of what one `coder_patch` call changed.
//
// Like the shell session store, no full file content is retained beyond the
// call: the engine returns the computed `Files` for the integration to write,
// while the store keeps only per-hunk byte counts and the revision that names
// one accepted patch. A reader that needs the material diff gets it from the
// computed `Files` of the successful call, never from the store.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// CoderPatchToolName is the coder tool that closes the gap.
	CoderPatchToolName = "coder_patch"
	// CoderPatchParityGapID names the ci#394 parity gap this closes.
	CoderPatchParityGapID = "multi_edit_patch"

	// MaxCoderPatchHunks bounds the hunks of one call, so a runaway loop cannot
	// push an unbounded patch.
	MaxCoderPatchHunks = 64
	// MaxCoderPatchFiles bounds the distinct files of one call.
	MaxCoderPatchFiles = 64
	// MaxCoderPatchOldRunes bounds one hunk's matched text, so a stray paste
	// cannot feed a giant old string into an Index scan.
	MaxCoderPatchOldRunes = 1 << 16
	// MaxCoderPatchNewRunes bounds one hunk's replacement text.
	MaxCoderPatchNewRunes = 1 << 20
)

// Sentinel errors the caller can classify instead of parsing message text.
var (
	ErrCoderPatchNoHunks      = errors.New("nativecoder: coder_patch requires at least one hunk")
	ErrCoderPatchTooManyHunks = errors.New("nativecoder: coder_patch exceeds the hunk cap")
	ErrCoderPatchTooManyFiles = errors.New("nativecoder: coder_patch exceeds the file cap")
	ErrCoderPatchBadFile      = errors.New("nativecoder: coder_patch has an unknown or empty file path")
	ErrCoderPatchBadHunk      = errors.New("nativecoder: coder_patch has an invalid hunk")
	ErrCoderPatchOldMissing   = errors.New("nativecoder: coder_patch old text is not present in the file")
)

// CoderPatchHunk is one exact-string replacement in one file.
type CoderPatchHunk struct {
	// FilePath is the workspace-relative path the hunk applies to.
	FilePath string
	// Old is the exact text to replace. It must be non-empty and present in the
	// file's running content at the moment the hunk is applied (hunks apply in
	// call order, so a later hunk may match text an earlier one wrote).
	Old string
	// New is the replacement text.
	New string
}

// CoderPatchRequest is the tool-dispatch form of a `coder_patch` call. Files is
// the current content per path, supplied by the integration (it owns file
// access); Hunks apply in order, grouped by file path.
type CoderPatchRequest struct {
	// ChatID identifies the chat the call belongs to.
	ChatID string
	// Files maps a workspace-relative file path to its current content.
	Files map[string]string
	// Hunks are the replacements to apply, in call order.
	Hunks []CoderPatchHunk
	// At is the write instant recorded in the store (a zero value is kept as-is).
	At time.Time
}

// CoderPatchHunkResult is the per-hunk outcome of a successful patch. Only
// metadata is carried; the material new content lives in CoderPatchResult.Files.
type CoderPatchHunkResult struct {
	// ID is a stable per-call hunk label (the call's ordinal, 1-based).
	ID string
	// FilePath is the path the hunk applied to.
	FilePath string
	// Applied reports the hunk was applied (always true on a successful call).
	Applied bool
}

// CoderPatchResult is what a successful `coder_patch` call returns.
type CoderPatchResult struct {
	// Files is the computed new content per path — the integration writes these
	// to the workspace. It is present only for the successful call whose Hunks
	// produced it; it is never retained by the store.
	Files map[string]string
	// Applied counts the hunks applied in total.
	Applied int
	// HunkResults lists each hunk's outcome in call order.
	HunkResults []CoderPatchHunkResult
	// Revision is the store revision that identifies this accepted patch.
	Revision uint64
	// Rendered is the deterministic text shown for the applied patch.
	Rendered string
	// JSON is the stable JSON form (revision, per-file counts) for structured
	// tool metadata; it never includes file content.
	JSON string
}

// ApplyCoderPatch is the pure engine: it applies every hunk in order to the
// running content and returns the resulting content. It is all-or-nothing — any
// validation failure returns before any hunk is reported, so the caller never
// writes a partially-patched tree. It has no filesystem access and no side
// effects, so it is fully unit-testable.
func ApplyCoderPatch(req CoderPatchRequest) (CoderPatchResult, error) {
	if len(req.Hunks) == 0 {
		return CoderPatchResult{}, ErrCoderPatchNoHunks
	}
	if len(req.Hunks) > MaxCoderPatchHunks {
		return CoderPatchResult{}, ErrCoderPatchTooManyHunks
	}
	if len(req.Files) > MaxCoderPatchFiles {
		return CoderPatchResult{}, ErrCoderPatchTooManyFiles
	}
	if len(req.Files) == 0 {
		return CoderPatchResult{}, ErrCoderPatchBadFile
	}
	working := make(map[string]string, len(req.Files))
	for path, content := range req.Files {
		if strings.TrimSpace(path) == "" {
			return CoderPatchResult{}, ErrCoderPatchBadFile
		}
		working[path] = content
	}
	results := make([]CoderPatchHunkResult, 0, len(req.Hunks))
	for i, h := range req.Hunks {
		path := strings.TrimSpace(h.FilePath)
		if path == "" {
			return CoderPatchResult{}, fmt.Errorf("%w: hunk %d has no file path", ErrCoderPatchBadFile, i+1)
		}
		content, ok := working[path]
		if !ok {
			return CoderPatchResult{}, fmt.Errorf("%w: hunk %d targets missing file %q", ErrCoderPatchBadFile, i+1, path)
		}
		if h.Old == "" {
			return CoderPatchResult{}, fmt.Errorf("%w: hunk %d in %s has empty old text", ErrCoderPatchBadHunk, i+1, path)
		}
		if utf8.RuneCountInString(h.Old) > MaxCoderPatchOldRunes {
			return CoderPatchResult{}, fmt.Errorf("%w: hunk %d old text exceeds %d runes", ErrCoderPatchBadHunk, i+1, MaxCoderPatchOldRunes)
		}
		if utf8.RuneCountInString(h.New) > MaxCoderPatchNewRunes {
			return CoderPatchResult{}, fmt.Errorf("%w: hunk %d replacement exceeds %d runes", ErrCoderPatchBadHunk, i+1, MaxCoderPatchNewRunes)
		}
		idx := strings.Index(content, h.Old)
		if idx < 0 {
			return CoderPatchResult{}, fmt.Errorf("%w: hunk %d in %s", ErrCoderPatchOldMissing, i+1, path)
		}
		content = content[:idx] + h.New + content[idx+len(h.Old):]
		working[path] = content
		results = append(results, CoderPatchHunkResult{
			ID:       fmt.Sprintf("%d", i+1),
			FilePath: path,
			Applied:  true,
		})
	}
	outFiles := make(map[string]string, len(working))
	for path, content := range working {
		outFiles[path] = content
	}
	return CoderPatchResult{Files: outFiles, Applied: len(results), HunkResults: results}, nil
}

// ------------------------------- store -------------------------------

// CoderPatchRecord is one accepted patch's per-file metadata, kept per chat.
// File content is never retained; only hunk and byte counts plus the revision.
type CoderPatchRecord struct {
	// FilePath the patch touched.
	FilePath string
	// HunkCount is how many hunks applied to that file.
	HunkCount int
	// OldBytes and NewBytes are the aggregate byte counts across those hunks.
	OldBytes int
	NewBytes int
	// At is the write instant.
	At time.Time
}

type coderPatchChatState struct {
	revision uint64
	records  []CoderPatchRecord
}

// CoderPatchStore holds the accepted-patch log per chat.
type CoderPatchStore struct {
	mu    sync.Mutex
	chats map[string]*coderPatchChatState
}

// NewCoderPatchStore returns an empty store.
func NewCoderPatchStore() *CoderPatchStore {
	return &CoderPatchStore{}
}

func (s *CoderPatchStore) chat(chatID string) *coderPatchChatState {
	if s.chats == nil {
		s.chats = map[string]*coderPatchChatState{}
	}
	st := s.chats[chatID]
	if st == nil {
		st = &coderPatchChatState{}
		s.chats[chatID] = st
	}
	return st
}

// Append records one accepted patch and returns the new revision. All records
// share the same At, falling back to Now when the caller passed a zero value.
func (s *CoderPatchStore) Append(chatID string, records []CoderPatchRecord, at time.Time) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at.IsZero() {
		at = time.Now()
	}
	st := s.chat(chatID)
	st.revision++
	for _, r := range records {
		r.At = at
		if r.HunkCount == 0 {
			continue
		}
		st.records = append(st.records, r)
	}
	return st.revision
}

// Records returns a copy of a chat's accepted-patch log, oldest first.
func (s *CoderPatchStore) Records(chatID string) []CoderPatchRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.chats[chatID]
	if st == nil {
		return nil
	}
	out := make([]CoderPatchRecord, len(st.records))
	copy(out, st.records)
	return out
}

// Revision returns a chat's current revision (0 when nothing was recorded).
func (s *CoderPatchStore) Revision(chatID string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.chats[chatID]
	if st == nil {
		return 0
	}
	return st.revision
}

// ------------------------------ surface ------------------------------

// coderPatchSurface is the state + render entry point behind coder_patch.
type coderPatchSurface struct {
	store *CoderPatchStore
}

type coderPatchFileStats struct {
	HunkCount int
	OldBytes  int
	NewBytes  int
}

// Apply runs the engine for one `coder_patch` call. On success it records the
// accepted patch (byte counts only) and returns the computed content plus the
// deterministic render and stable JSON. On any validation failure it returns the
// sentinel-wrapped error and records nothing, so the tree is never half-written.
func (s *coderPatchSurface) Apply(req CoderPatchRequest) (CoderPatchResult, error) {
	res, err := ApplyCoderPatch(req)
	if err != nil {
		return CoderPatchResult{}, err
	}
	stats := coderPatchFileStatsFromHunks(req.Hunks)
	var rev uint64
	if strings.TrimSpace(req.ChatID) != "" {
		records := make([]CoderPatchRecord, 0, len(stats))
		for _, path := range sortedPatchPaths(stats) {
			ft := stats[path]
			records = append(records, CoderPatchRecord{
				FilePath:  path,
				HunkCount: ft.HunkCount,
				OldBytes:  ft.OldBytes,
				NewBytes:  ft.NewBytes,
			})
		}
		rev = s.store.Append(req.ChatID, records, req.At)
	}
	res.Revision = rev
	res.Rendered = renderCoderPatchSummary(res.Applied, stats, rev)
	res.JSON = coderPatchResultJSON(res.Applied, stats, rev)
	return res, nil
}

func coderPatchFileStatsFromHunks(hunks []CoderPatchHunk) map[string]coderPatchFileStats {
	stats := map[string]coderPatchFileStats{}
	for _, h := range hunks {
		ft := stats[h.FilePath]
		ft.HunkCount++
		ft.OldBytes += len(h.Old)
		ft.NewBytes += len(h.New)
		stats[h.FilePath] = ft
	}
	return stats
}

func sortedPatchPaths(stats map[string]coderPatchFileStats) []string {
	paths := make([]string, 0, len(stats))
	for path := range stats {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func renderCoderPatchSummary(applied int, stats map[string]coderPatchFileStats, revision uint64) string {
	buf := strings.Builder{}
	fmt.Fprintf(&buf, "Applied %d hunk", applied)
	if applied != 1 {
		buf.WriteString("s")
	}
	fmt.Fprintf(&buf, " across %d file", len(stats))
	if len(stats) != 1 {
		buf.WriteString("s")
	}
	fmt.Fprintf(&buf, " (revision %d):\n", revision)
	for _, path := range sortedPatchPaths(stats) {
		ft := stats[path]
		fmt.Fprintf(&buf, "- %s (%d hunk", path, ft.HunkCount)
		if ft.HunkCount != 1 {
			buf.WriteString("s")
		}
		fmt.Fprintf(&buf, ", +%d/-%d bytes)\n", ft.NewBytes, ft.OldBytes)
	}
	return strings.TrimRight(buf.String(), "\n")
}

type coderPatchResultJSONShape struct {
	Applied  int                           `json:"applied"`
	Revision uint64                        `json:"revision"`
	Files    map[string]coderPatchFileJSON `json:"files"`
}

type coderPatchFileJSON struct {
	Hunks    int `json:"hunks"`
	OldBytes int `json:"old_bytes"`
	NewBytes int `json:"new_bytes"`
}

func coderPatchResultJSON(applied int, stats map[string]coderPatchFileStats, revision uint64) string {
	shape := coderPatchResultJSONShape{
		Applied:  applied,
		Revision: revision,
		Files:    map[string]coderPatchFileJSON{},
	}
	for path, ft := range stats {
		shape.Files[path] = coderPatchFileJSON{Hunks: ft.HunkCount, OldBytes: ft.OldBytes, NewBytes: ft.NewBytes}
	}
	b, err := json.Marshal(shape)
	if err != nil {
		return "{}"
	}
	return string(b)
}
