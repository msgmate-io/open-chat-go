package msgmate

import "testing"

// TestCollectRuntimeSelectorsAllowResetIsOptOut pins the ci#1135 pass-through
// contract: the proposing tool (git_propose_interaction_runtime) may set
// allow_reset:false to forbid resetting a selector, and that flag must reach the
// persisted selector. Absent means allowed, so the key is dropped instead of
// being materialised as true - selectors persisted before ci#1135 therefore keep
// behaving unchanged.
func TestCollectRuntimeSelectorsAllowResetIsOptOut(t *testing.T) {
	cases := []struct {
		name          string
		payload       map[string]interface{}
		wantKey       bool
		wantAllowBool bool
	}{
		{
			name:    "absent_stays_absent",
			payload: map[string]interface{}{"id": "tc-1", "title": "Pick a runtime"},
			wantKey: false,
		},
		{
			name:          "explicit_false_is_carried",
			payload:       map[string]interface{}{"id": "tc-1", "allow_reset": false},
			wantKey:       true,
			wantAllowBool: false,
		},
		{
			name:    "explicit_true_is_normalised_away",
			payload: map[string]interface{}{"id": "tc-1", "allow_reset": true},
			wantKey: false,
		},
		{
			name:    "non_bool_is_ignored",
			payload: map[string]interface{}{"id": "tc-1", "allow_reset": "false"},
			wantKey: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toolCalls := []interface{}{
				map[string]interface{}{
					"id":               "call-1",
					"runtime_selector": tc.payload,
				},
			}
			selectors := collectRuntimeSelectors(toolCalls)
			if len(selectors) != 1 {
				t.Fatalf("expected one selector, got %d", len(selectors))
			}
			selector, ok := selectors[0].(map[string]interface{})
			if !ok {
				t.Fatalf("expected a selector map, got %#v", selectors[0])
			}
			raw, present := selector["allow_reset"]
			if present != tc.wantKey {
				t.Fatalf("expected allow_reset present=%v, got present=%v (%#v)", tc.wantKey, present, raw)
			}
			if tc.wantKey {
				allowReset, isBool := raw.(bool)
				if !isBool {
					t.Fatalf("expected allow_reset to stay a bool, got %#v", raw)
				}
				if allowReset != tc.wantAllowBool {
					t.Fatalf("expected allow_reset %v, got %v", tc.wantAllowBool, allowReset)
				}
			}
		})
	}
}

// TestCollectRuntimeSelectorsKeepsIDsAndAllowReset ensures the ci#1135 flag does
// not disturb the stable selector id derivation (a message may carry several
// selectors and the widget targets one by id).
func TestCollectRuntimeSelectorsKeepsIDsAndAllowReset(t *testing.T) {
	toolCalls := []interface{}{
		map[string]interface{}{
			"id": "call-1",
			"runtime_selector": map[string]interface{}{
				"allow_reset": false,
			},
		},
		map[string]interface{}{
			"id": "call-2",
			"runtime_selector": map[string]interface{}{
				"id":    "explicit-id",
				"title": "second",
			},
		},
	}

	selectors := collectRuntimeSelectors(toolCalls)
	if len(selectors) != 2 {
		t.Fatalf("expected two selectors, got %d", len(selectors))
	}
	first := selectors[0].(map[string]interface{})
	if first["id"] != "call-1" {
		t.Fatalf("expected the tool-call id to seed the selector id, got %#v", first["id"])
	}
	if allowReset, ok := first["allow_reset"].(bool); !ok || allowReset {
		t.Fatalf("expected allow_reset:false on the first selector, got %#v", first["allow_reset"])
	}
	second := selectors[1].(map[string]interface{})
	if second["id"] != "explicit-id" {
		t.Fatalf("expected an explicit id to win, got %#v", second["id"])
	}
	if _, present := second["allow_reset"]; present {
		t.Fatalf("expected no allow_reset key on the second selector, got %#v", second["allow_reset"])
	}
}
