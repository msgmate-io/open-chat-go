package msgmate

import (
	"encoding/json"
	"fmt"
	"strings"

	tooldefs "backend/api/msgmate/tools"
	"github.com/msgmate-io/open-chat-go-community-coder-integration/coder"
)

// coderCompactRunner is the core-side runner behind the `coder_compact` tool
// (ci#394 gap 7, ticket ci#957): it folds a transcript excerpt into this
// chat's bounded, oldest-first, duplicate-dropped session summary and persists
// it, and answers read calls with the current summary. It mirrors
// coderScheduleRunner: the tools package cannot import the msgmate package it
// is embedded in, so ToolConstructors publishes this implementation through
// the tooldefs.CoderCompactRunnerProvider seam.
type coderCompactRunner struct {
	// surface is the coder_compact surface bound to a chat. It is lazily
	// resolved from the shared parity surface of the msgmate handler; when the
	// deployment publishes none, calls fail loudly.
	surface *coder.CoderParitySurface
}

// The seams are package-level (the coderScheduleRunner pattern) so a test can
// drive the runner against a scratch store instead of a live bot context.
var (
	coderCompactGlobalSurface = func() *coder.CoderParitySurface {
		if h := GetGlobalMsgmateHandler(); h != nil {
			if s := h.GetCoderParitySurface(); s != nil {
				return s
			}
		}
		return nil
	}
	coderCompactResetTest = func() {}
)

// coderCompactOps is the subset of the nativecoder coder_compact surface the
// runner needs. The backing type is unexported, so the runner holds it as this
// interface instead of a concrete package type.
type coderCompactOps interface {
	Compact(coder.CoderCompactRequest) (coder.CoderCompactResult, error)
	State(chatID string) (coder.CoderCompactResult, error)
}

// resolveSurface returns the coder_compact surface for chatUUID, resolving the
// process-wide parity surface on first use and caching it on the runner.
func (r *coderCompactRunner) resolveSurface(chatUUID string) (coderCompactOps, error) {
	if strings.TrimSpace(chatUUID) == "" {
		return nil, fmt.Errorf("coder_compact needs the chat identity of the running turn")
	}
	if r.surface == nil {
		r.surface = coderCompactGlobalSurface()
	}
	if r.surface == nil {
		return nil, fmt.Errorf("coder_compact is unavailable: this process published no coder parity surface")
	}
	if r.surface.Compact == nil {
		return nil, fmt.Errorf("coder_compact is unavailable: no coder_compact surface registered")
	}
	return r.surface.Compact, nil
}

// CompactCoderContext folds text into the bounded summary of chatUUID and
// returns the persisted state of the summary.
func (r *coderCompactRunner) CompactCoderContext(chatUUID string, text string, capBytes int) (map[string]interface{}, error) {
	compact, err := r.resolveSurface(chatUUID)
	if err != nil {
		return nil, err
	}
	result, err := compact.Compact(coder.CoderCompactRequest{
		ChatID:   chatUUID,
		Text:     text,
		Capacity: capBytes,
	})
	if err != nil {
		return nil, err
	}
	return coderCompactResultState(result), nil
}

// CoderContextSummary returns the current bounded summary of chatUUID.
func (r *coderCompactRunner) CoderContextSummary(chatUUID string) (map[string]interface{}, error) {
	compact, err := r.resolveSurface(chatUUID)
	if err != nil {
		return nil, err
	}
	result, err := compact.State(chatUUID)
	if err != nil {
		return nil, err
	}
	return coderCompactResultState(result), nil
}

// coderCompactResultState renders the runner's answer as a JSON-able map. The
// full summary (not just the transcript render) is what the reader's persisted
// context_summary needs. A chat with no persisted summary yields an empty map
// (the tool layer turns that into its own "no summary" error), so a read of a
// never-touched chat never invents a placeholder summary.
func coderCompactResultState(result coder.CoderCompactResult) map[string]interface{} {
	if strings.TrimSpace(result.Summary) == "" {
		return map[string]interface{}{}
	}
	state := map[string]interface{}{
		"summary":  result.Summary,
		"rendered": result.Rendered,
	}
	if result.JSON != "" {
		var stable map[string]interface{}
		if err := json.Unmarshal([]byte(result.JSON), &stable); err == nil {
			for k, v := range stable {
				state[k] = v
			}
		}
	}
	return state
}

// coderCompactToolRunner is the provider the tools package calls. It returns a
// zero-value runner; the runner itself resolves the process-wide parity surface
// lazily, so a deployment without it fails loudly at call time instead of at
// init.
func coderCompactToolRunner() tooldefs.CoderCompactRunner {
	return &coderCompactRunner{}
}

// ensure backend/coder.JSON is referenced (kept for parity with the
// schedule runner's explicit json use).
var _ = json.Marshal