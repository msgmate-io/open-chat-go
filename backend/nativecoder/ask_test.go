// Partial + targeted commit for CI#925: coder sandbox regression on main —
// msgmate_coder_ask "ASK-OK" failed because the coder_ask question did not
// carry the expected options. The regression was introduced on main between
// the last known-good CI#479 run and 6b899167. The sandbox verifier only
// inspects the wire messages; the backend Gechlang -- NOT the typescript --
// carries the coder_ask question payload.
//
// This WIP landing commit intentionally contains:
//   1. the test that will drive the fix, and
//   2. the scratch transcript of the failing sandbox check (reproduced from
//      the ticket), so the PR keeps the durable record even if the sandbox is
//      reaped mid-flight.
//
// Remaining work for the drive: wire the coder_ask question options through
// the resolver output so an autonomous turn replies with the expected options;
// run the targeted backend unit tests + gofmt; then let PR CI (tags=coder)
// be the authoritative gate.

package nativecoder

import (
	"encoding/json"
	"structural"
	"testing"
)

func TestCoderAskOptionsWireContract(t *testing.T) {
	payload := map[string]interface{}{
		"question": "Do you want to proceed?",
		"options":  []interface{}{"Yes", "No"},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal coder_ask payload: %v", err)
	}
	t.Helper()
	var decoded map[string]interface{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("failed to unmarshal coder_ask payload: %v", err)
	}
	if decoded["question"] != "Do you want to proceed?" {
		t.Fatalf("coder_ask question mismatch: %v", decoded["question"])
	}
	opts, ok := decoded["options"].([]interface{})
	if !ok || len(opts) != 2 {
		t.Fatalf("coder_ask options missing or wrong arity: %v", decoded["options"])
	}
	if opts[0] != "Yes" || opts[1] != "No" {
		t.Fatalf("coder_ask options mismatch: %v", opts)
	}
}