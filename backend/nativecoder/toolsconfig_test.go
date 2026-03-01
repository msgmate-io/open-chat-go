package nativecoder

import "testing"

// TestDropEmptyToolsTreatsAnEmptyListAsUnset guards the ci#617 fix: a config
// with `tools: []` must be treated exactly like a config that never mentions
// tools, so the integration defaults can fill the full coder tool set instead
// of being suppressed by the empty list.
func TestDropEmptyToolsTreatsAnEmptyListAsUnset(t *testing.T) {
	cases := []struct {
		name  string
		value interface{}
		want  bool // is the `tools` key present afterwards?
	}{
		{"empty interface slice", []interface{}{}, false},
		{"empty string slice", []string{}, false},
		{"null", nil, false},
		{"non-empty", []interface{}{"coder_bash"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := DropEmptyTools(map[string]interface{}{"tools": tc.value})
			_, present := out[ToolsConfigKey]
			if present != tc.want {
				t.Fatalf("tools present = %v, want %v (%#v)", present, tc.want, out)
			}
		})
	}
}

// TestDropEmptyToolsKeepsTheRestOfTheConfig guards that nothing else is touched
// and that the input map is not mutated.
func TestDropEmptyToolsKeepsTheRestOfTheConfig(t *testing.T) {
	config := map[string]interface{}{
		"model":   "some-model",
		"backend": "litellm",
		"tools":   []interface{}{},
	}
	out := DropEmptyTools(config)
	if out["model"] != "some-model" || out["backend"] != "litellm" {
		t.Fatalf("expected the other keys to survive, got %#v", out)
	}
	if _, present := out[ToolsConfigKey]; present {
		t.Fatalf("expected the empty tools list to be gone, got %#v", out[ToolsConfigKey])
	}
	if _, present := config[ToolsConfigKey]; !present {
		t.Fatal("DropEmptyTools must not mutate its input")
	}

	// A config that never mentions tools is returned unchanged.
	absent := map[string]interface{}{"model": "some-model"}
	if got := DropEmptyTools(absent); len(got) != 1 || got["model"] != "some-model" {
		t.Fatalf("expected an untouched config, got %#v", got)
	}
}
