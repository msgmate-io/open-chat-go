package integrationsettings

import (
	"os"
	"strings"
	"testing"
)

func TestApplyDeltaSetNestedPath(t *testing.T) {
	root := map[string]interface{}{
		"bootstrap": map[string]interface{}{
			"bots": []interface{}{
				map[string]interface{}{
					"bot":                   map[string]interface{}{"name": "odoo-plan-bot"},
					"default_shared_config": map[string]interface{}{"model": "old-model"},
				},
			},
		},
	}

	delta := ConfigDelta{Set: []ConfigSetOp{
		{Path: "bootstrap.bots[bot.name=odoo-plan-bot].default_shared_config.opencode_project", Value: "little-world-odoo-dev"},
	}}
	if err := ApplyDelta(root, delta); err != nil {
		t.Fatalf("apply delta: %v", err)
	}

	bots := root["bootstrap"].(map[string]interface{})["bots"].([]interface{})
	shared := bots[0].(map[string]interface{})["default_shared_config"].(map[string]interface{})
	if shared["opencode_project"] != "little-world-odoo-dev" {
		t.Fatalf("expected opencode_project set, got %#v", shared)
	}
	if shared["model"] != "old-model" {
		t.Fatalf("expected existing sibling field preserved, got %#v", shared)
	}
}

func TestApplyDeltaSetCreatesMissingParents(t *testing.T) {
	root := map[string]interface{}{}
	delta := ConfigDelta{Set: []ConfigSetOp{
		{Path: "anchors.little-world-odoo-admin-kubeconfig-b64", Value: "YWRtaW4="},
	}}
	if err := ApplyDelta(root, delta); err != nil {
		t.Fatalf("apply delta: %v", err)
	}
	anchors := root["anchors"].(map[string]interface{})
	if anchors["little-world-odoo-admin-kubeconfig-b64"] != "YWRtaW4=" {
		t.Fatalf("expected anchor created, got %#v", anchors)
	}
}

func TestApplyDeltaSetMissingSelectorFails(t *testing.T) {
	root := map[string]interface{}{}
	delta := ConfigDelta{Set: []ConfigSetOp{
		{Path: "bootstrap.bots[bot.name=missing].model", Value: "x"},
	}}
	if err := ApplyDelta(root, delta); err == nil {
		t.Fatal("expected error when the list selector matches nothing")
	}
}

func TestApplyDeltaUpsertInsertsAndMerges(t *testing.T) {
	root := map[string]interface{}{
		"bootstrap": map[string]interface{}{
			"bots": []interface{}{
				map[string]interface{}{
					"bot":               map[string]interface{}{"name": "odoo-plan-bot", "username": "odoo-plan-bot"},
					"primary_owner":     "admin",
					"additional_owners": []interface{}{"a", "b"},
				},
			},
		},
	}

	delta := ConfigDelta{Upsert: []ConfigUpsertOp{
		{
			Path: "bootstrap.bots",
			Key:  "bot",
			Value: map[string]interface{}{
				"bot":           map[string]interface{}{"name": "odoo-plan-bot", "username": "odoo-plan-bot"},
				"primary_owner": "admin",
				"description":   "updated",
			},
		},
	}}
	if err := ApplyDelta(root, delta); err != nil {
		t.Fatalf("apply upsert: %v", err)
	}

	bots := root["bootstrap"].(map[string]interface{})["bots"].([]interface{})
	if len(bots) != 1 {
		t.Fatalf("expected merge in place, got %d entries", len(bots))
	}
	row := bots[0].(map[string]interface{})
	if row["description"] != "updated" {
		t.Fatalf("expected merged fields, got %#v", row)
	}
	if _, ok := row["additional_owners"]; !ok {
		t.Fatalf("expected unrelated fields preserved, got %#v", row)
	}

	delta.Upsert[0].Value = map[string]interface{}{
		"bot": map[string]interface{}{"name": "new-bot", "username": "new-bot"},
	}
	if err := ApplyDelta(root, delta); err != nil {
		t.Fatalf("apply upsert append: %v", err)
	}
	bots = root["bootstrap"].(map[string]interface{})["bots"].([]interface{})
	if len(bots) != 2 {
		t.Fatalf("expected new entry appended, got %d", len(bots))
	}
}

func TestSetConfigDeltaPersists(t *testing.T) {
	path := applyTestState(t, "anchors:\n  existing: keep\nbootstrap:\n  bots: []\n")

	delta := ConfigDelta{Set: []ConfigSetOp{
		{Path: "anchors.little-world-odoo-admin-kubeconfig-b64", Value: "cGxhY2Vob2xkZXI="},
		{Path: "bootstrap.bots[bot.name=odoo-plan-bot].default_shared_config.opencode_project", Value: "little-world-odoo-dev"},
	}}
	// The path only exists after an upsert, so seed the bot first.
	seed := ConfigDelta{Upsert: []ConfigUpsertOp{{
		Path: "bootstrap.bots",
		Key:  "bot",
		Value: map[string]interface{}{
			"bot":                   map[string]interface{}{"name": "odoo-plan-bot", "username": "odoo-plan-bot"},
			"default_shared_config": map[string]interface{}{},
		},
	}}}
	if _, err := SetConfigDelta(path, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	result, err := SetConfigDelta(path, delta)
	if err != nil {
		t.Fatalf("set config delta: %v", err)
	}
	if !result.Persisted {
		t.Fatalf("expected persisted, got %q", result.PersistError)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "little-world-odoo-admin-kubeconfig-b64") || !strings.Contains(text, "cGxhY2Vob2xkZXI=") {
		t.Fatalf("expected anchor written, got:\n%s", text)
	}
	if !strings.Contains(text, "little-world-odoo-dev") {
		t.Fatalf("expected opencode_project written, got:\n%s", text)
	}
	if !strings.Contains(text, "existing: keep") {
		t.Fatalf("expected unrelated anchor preserved, got:\n%s", text)
	}
}
