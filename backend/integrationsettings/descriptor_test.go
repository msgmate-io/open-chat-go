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
