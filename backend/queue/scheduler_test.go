package queue

import "testing"

func TestDecodeIntegrationSchedules(t *testing.T) {
	raw := []map[string]interface{}{
		{"id": "poll", "function": "poll_triggers", "spec": "@every 1m"},
	}
	specs, err := decodeIntegrationSchedules(raw)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(specs) != 1 || specs[0].ID != "poll" || specs[0].Function != "poll_triggers" || specs[0].Spec != "@every 1m" {
		t.Fatalf("unexpected specs: %+v", specs)
	}
}

func TestDecodeIntegrationSchedulesEmpty(t *testing.T) {
	if specs, err := decodeIntegrationSchedules(nil); err != nil || specs != nil {
		t.Fatalf("expected nil specs and no error, got %+v, %v", specs, err)
	}
	type scheduled struct {
		ID       string `json:"id"`
		Function string `json:"function"`
		Spec     string `json:"spec"`
	}
	specs, err := decodeIntegrationSchedules([]scheduled{{ID: "a", Function: "b", Spec: "@every 1s"}})
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(specs) != 1 || specs[0].ID != "a" {
		t.Fatalf("unexpected specs: %+v", specs)
	}
}