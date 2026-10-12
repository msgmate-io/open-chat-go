package integrations

import (
	"testing"
	"testing/fstest"

	extiface "github.com/msgmate-io/go-integration-interface/integrationinterface"
)

func TestValidateAndNormalizeDefinitionFrontendDocs(t *testing.T) {
	def, err := validateAndNormalizeDefinition(extiface.Definition{
		Name: "demo",
		FrontendAssets: fstest.MapFS{
			"docs/index.html": &fstest.MapFile{Data: []byte("<html></html>")},
		},
		FrontendDocs: []extiface.FrontendDoc{
			{
				Route:       "integrations/demo/docs/",
				Title:       "  Getting Started  ",
				Description: "  Install and configure  ",
				AssetPath:   "docs/index.html",
				Section:     " Overview ",
				Order:       1,
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(def.FrontendDocs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(def.FrontendDocs))
	}
	doc := def.FrontendDocs[0]
	if doc.Route != "/integrations/demo/docs" {
		t.Fatalf("expected normalized route, got %q", doc.Route)
	}
	if doc.AssetPath != "docs/index.html" {
		t.Fatalf("expected normalized asset path, got %q", doc.AssetPath)
	}
	if doc.Title != "Getting Started" || doc.Description != "Install and configure" || doc.Section != "Overview" {
		t.Fatalf("expected trimmed metadata, got %#v", doc)
	}
	if doc.Order != 1 {
		t.Fatalf("expected order 1, got %d", doc.Order)
	}
}

func TestValidateAndNormalizeDefinitionFrontendDocRequiresAssets(t *testing.T) {
	_, err := validateAndNormalizeDefinition(extiface.Definition{
		Name: "demo",
		FrontendDocs: []extiface.FrontendDoc{
			{Route: "/integrations/demo/docs", AssetPath: "docs/index.html"},
		},
	})
	if err == nil {
		t.Fatalf("expected error when docs are declared without frontend assets")
	}
}

func TestValidateAndNormalizeDefinitionFrontendDocRoutePrefix(t *testing.T) {
	_, err := validateAndNormalizeDefinition(extiface.Definition{
		Name:           "demo",
		FrontendAssets: fstest.MapFS{"docs/index.html": &fstest.MapFile{Data: []byte("x")}},
		FrontendDocs: []extiface.FrontendDoc{
			{Route: "/other/docs", AssetPath: "docs/index.html"},
		},
	})
	if err == nil {
		t.Fatalf("expected error for doc route outside the integration prefix")
	}
}

func TestValidateAndNormalizeDefinitionLabels(t *testing.T) {
	def, err := validateAndNormalizeDefinition(extiface.Definition{
		Name:   "demo",
		Labels: []string{" core ", "Full", "core", ""},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(def.Labels) != 2 {
		t.Fatalf("expected 2 de-duplicated labels, got %#v", def.Labels)
	}
	if def.Labels[0] != "core" || def.Labels[1] != "Full" {
		t.Fatalf("unexpected normalized labels: %#v", def.Labels)
	}
}
