package tools

import "testing"

func TestToPascalIdentifier(t *testing.T) {
	testCases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: "Tool"},
		{name: "standard", input: "create_confirmable_action_suggestion", want: "CreateConfirmableActionSuggestion"},
		{name: "leading number", input: "2fa_setup", want: "Tool2faSetup"},
		{name: "special chars", input: " weather-tool ", want: "WeatherTool"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := toPascalIdentifier(tc.input); got != tc.want {
				t.Fatalf("toPascalIdentifier(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestAddSchemaTitle(t *testing.T) {
	input := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"foo": map[string]interface{}{"type": "string"},
		},
	}

	updated := addSchemaTitle(input, "MyTitle")
	if got, _ := updated["title"].(string); got != "MyTitle" {
		t.Fatalf("expected title %q, got %q", "MyTitle", got)
	}

	if _, hasOriginalTitle := input["title"]; hasOriginalTitle {
		t.Fatalf("expected original map to stay unmodified")
	}

	withTitle := map[string]interface{}{"type": "object", "title": "Existing"}
	preserved := addSchemaTitle(withTitle, "Ignored")
	if got, _ := preserved["title"].(string); got != "Existing" {
		t.Fatalf("expected existing title to be preserved, got %q", got)
	}
}
