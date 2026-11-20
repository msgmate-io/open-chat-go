package tools

import "testing"

// TestCurrentTimeConfirmedToolHasDistinctFunctionName guards ci#1156: the
// confirmable time tool must expose a function name that does not collide with
// the non-confirmable one, otherwise its toolMap entry is overwritten by the
// plain tool and the confirmation result is never routed back to the pending
// call (the turn then hangs forever).
func TestCurrentTimeConfirmedToolHasDistinctFunctionName(t *testing.T) {
	if CurrentTimeConfirmedToolDef.FunctionName == "" {
		t.Fatal("confirmable time tool has an empty FunctionName")
	}
	if !CurrentTimeConfirmedToolDef.RequiresConfirmation {
		t.Fatal("confirmable time tool must require confirmation")
	}
	if CurrentTimeConfirmedToolDef.FunctionName == CurrentTimeToolDef.FunctionName {
		t.Fatalf("confirmable time tool shares FunctionName %q with the non-confirmable one", CurrentTimeToolDef.FunctionName)
	}
	if CurrentTimeConfirmedToolDef.Name == CurrentTimeToolDef.Name {
		t.Fatalf("confirmable time tool shares Name %q with the non-confirmable one", CurrentTimeToolDef.Name)
	}

	// Mirror the registration done in setupTools: the two keys must stay
	// distinct so neither tool overwrites the other in toolMap.
	toolMap := map[string]string{}
	toolMap[CurrentTimeToolDef.Name] = "plain"
	toolMap[CurrentTimeConfirmedToolDef.Name] = "confirmed"
	toolMap[CurrentTimeConfirmedToolDef.FunctionName] = "confirmed"

	if got := toolMap[CurrentTimeConfirmedToolDef.FunctionName]; got != "confirmed" {
		t.Fatalf("confirmable function name resolved to %q, want confirmed", got)
	}
	if got := toolMap[CurrentTimeToolDef.Name]; got != "plain" {
		t.Fatalf("plain tool entry was overwritten by %q", got)
	}
	if len(toolMap) != 2 {
		t.Fatalf("expected 2 distinct toolMap keys, got %d", len(toolMap))
	}
}
