package integrationsettings

import (
	"testing"

	"backend/runtimecfg"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

func TestInferFieldType(t *testing.T) {
	cases := []struct {
		name    string
		decl    integrationinterface.RuntimeEnvVar
		current string
		want    string
	}{
		{"sensitive", integrationinterface.RuntimeEnvVar{Key: "OCI_A", Sensitive: true}, "", FieldTypeSecret},
		{"enabled", integrationinterface.RuntimeEnvVar{Key: "OCI_A_ENABLED"}, "", FieldTypeBool},
		{"enable substring", integrationinterface.RuntimeEnvVar{Key: "OCI_ENABLE_A"}, "", FieldTypeBool},
		{"bool value", integrationinterface.RuntimeEnvVar{Key: "OCI_A"}, "true", FieldTypeBool},
		{"json key", integrationinterface.RuntimeEnvVar{Key: "OCI_A_JSON"}, "", FieldTypeJSON},
		{"spec key", integrationinterface.RuntimeEnvVar{Key: "OCI_A_SPEC"}, "", FieldTypeJSON},
		{"multiline", integrationinterface.RuntimeEnvVar{Key: "OCI_A"}, "a\nb", FieldTypeJSON},
		{"plain string", integrationinterface.RuntimeEnvVar{Key: "OCI_A"}, "hello", FieldTypeString},
		{"explicit number", integrationinterface.RuntimeEnvVar{Key: "OCI_A", Type: "number"}, "", FieldTypeNumber},
		{"explicit select", integrationinterface.RuntimeEnvVar{Key: "OCI_A", Type: "select"}, "", FieldTypeSelect},
		{"rate heuristic", integrationinterface.RuntimeEnvVar{Key: "OCI_A_RATE"}, "", FieldTypeNumber},
		{"tokens heuristic", integrationinterface.RuntimeEnvVar{Key: "OCI_A_TOKENS"}, "", FieldTypeNumber},
		{"numeric value", integrationinterface.RuntimeEnvVar{Key: "OCI_A"}, "42", FieldTypeNumber},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := InferFieldType(tc.decl, tc.current); got != tc.want {
				t.Fatalf("InferFieldType() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildDescriptorsMasksSensitive(t *testing.T) {
	def := integrationinterface.Definition{
		Name: "demo",
		RuntimeEnvVars: []integrationinterface.RuntimeEnvVar{
			{Key: "OCI_DEMO_TOKEN", Sensitive: true},
			{Key: "OCI_DEMO_HOST"},
		},
	}
	values := map[string]runtimecfg.Value{
		"OCI_DEMO_TOKEN": {Value: "super-secret", Sensitive: true},
		"OCI_DEMO_HOST":  {Value: "example.com"},
	}

	masked := BuildDescriptors(def, values, false)
	if len(masked) != 2 {
		t.Fatalf("expected 2 descriptors, got %d", len(masked))
	}
	if masked[0].Value != "(hidden)" {
		t.Fatalf("expected sensitive value masked, got %q", masked[0].Value)
	}
	if !masked[0].Configured {
		t.Fatalf("expected token to be marked configured")
	}
	if masked[1].Value != "example.com" {
		t.Fatalf("expected non-sensitive value preserved, got %q", masked[1].Value)
	}

	revealed := BuildDescriptors(def, values, true)
	if revealed[0].Value != "super-secret" {
		t.Fatalf("expected revealed value, got %q", revealed[0].Value)
	}
}

func TestBuildDescriptorsPassesMetadata(t *testing.T) {
	min := 1.0
	max := 10.0
	step := 1.0
	def := integrationinterface.Definition{
		Name: "demo",
		RuntimeEnvVars: []integrationinterface.RuntimeEnvVar{
			{
				Key:         "OCI_DEMO_RATE",
				Label:       "Conversion rate",
				Type:        "number",
				Group:       "Conversion",
				Order:       3,
				Default:     "5",
				Placeholder: "1-10",
				Required:    true,
				UserVisible: true,
				Min:         &min,
				Max:         &max,
				Step:        &step,
			},
			{Key: "OCI_DEMO_UNLABELLED"},
		},
	}
	descriptors := BuildDescriptors(def, nil, false)
	if len(descriptors) != 2 {
		t.Fatalf("expected 2 descriptors, got %d", len(descriptors))
	}
	rate := descriptors[0]
	if rate.Label != "Conversion rate" || rate.Group != "Conversion" || rate.Order != 3 {
		t.Fatalf("metadata not passed through: %+v", rate)
	}
	if !rate.Required || !rate.UserVisible {
		t.Fatalf("required/user_visible not passed through: %+v", rate)
	}
	if rate.Type != FieldTypeNumber || rate.Min == nil || rate.Max == nil || rate.Step == nil {
		t.Fatalf("numeric bounds not passed through: %+v", rate)
	}
	if descriptors[1].Label != "Demo unlabelled" {
		t.Fatalf("expected humanized fallback label, got %q", descriptors[1].Label)
	}
}

func TestResolveConfigTargetPrefersAlias(t *testing.T) {
	def := integrationinterface.Definition{
		Name: "demo",
		RuntimeEnvVars: []integrationinterface.RuntimeEnvVar{
			{Key: "OCI_DEMO_TOKEN"},
			{Key: "OCI_DEMO_HOST"},
		},
		RuntimeConfigAliases: []integrationinterface.RuntimeConfigAlias{
			{JSONKey: "token", EnvKey: "OCI_DEMO_TOKEN"},
		},
	}
	descriptors := BuildDescriptors(def, nil, false)
	targets := map[string]string{}
	for _, d := range descriptors {
		targets[d.Key] = d.ConfigTarget
	}
	if targets["OCI_DEMO_TOKEN"] != "integrations.demo.token" {
		t.Fatalf("expected alias target, got %q", targets["OCI_DEMO_TOKEN"])
	}
	if targets["OCI_DEMO_HOST"] != "env.OCI_DEMO_HOST" {
		t.Fatalf("expected env target, got %q", targets["OCI_DEMO_HOST"])
	}
}
