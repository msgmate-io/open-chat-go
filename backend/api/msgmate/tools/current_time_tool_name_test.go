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

	// Mirror the registration done in setupTools, which keys toolMap by the
	// tool name AND by the effective function name (BaseTool falls back to the
	// tool name when no explicit function name is set). With the old colliding
	// function name both tools wrote "get_current_time", the plain one won and
	// the pending confirmation had no tool to resolve back to.
	toolMap := map[string]string{}
	for _, def := range []struct {
		def    ToolDefinition
		key    string
		marker string
	}{
		{CurrentTimeToolDef, effectiveFunctionKey(CurrentTimeToolDef), "plain"},
		{CurrentTimeConfirmedToolDef, effectiveFunctionKey(CurrentTimeConfirmedToolDef), "confirmed"},
	} {
		toolMap[def.def.Name] = def.marker
		toolMap[def.key] = def.marker
	}

	if got := toolMap[CurrentTimeConfirmedToolDef.Name]; got != "confirmed" {
		t.Fatalf("confirmable tool name resolved to %q, want confirmed (entry was shadowed)", got)
	}
	if got := toolMap[effectiveFunctionKey(CurrentTimeConfirmedToolDef)]; got != "confirmed" {
		t.Fatalf("confirmable function name resolved to %q, want confirmed (confirmation result cannot be routed back)", got)
	}
	if got := toolMap[CurrentTimeToolDef.Name]; got != "plain" {
		t.Fatalf("plain tool entry was overwritten by %q", got)
	}
	if len(toolMap) != 2 {
		t.Fatalf("expected 2 distinct toolMap keys, got %d", len(toolMap))
	}
}

// effectiveFunctionKey mirrors BaseTool.GetToolFunctionName: the explicit
// function name when set, otherwise the tool name.
func effectiveFunctionKey(def ToolDefinition) string {
	if def.FunctionName != "" {
		return def.FunctionName
	}
	return def.Name
}
