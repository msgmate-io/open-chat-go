package tools

import "testing"

func TestRandomNumberSeededToolDeterministicForSameSeed(t *testing.T) {
	input := RandomNumberToolInput{Min: 1, Max: 1000000}

	first, err := RandomNumberSeededToolDef.RunFunction(input, map[string]interface{}{"seed": float64(42)})
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}

	second, err := RandomNumberSeededToolDef.RunFunction(input, map[string]interface{}{"seed": float64(42)})
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}

	if first != second {
		t.Fatalf("expected deterministic result for same seed, got %q and %q", first, second)
	}
}

func TestRandomNumberSeededToolVariesBySeed(t *testing.T) {
	input := RandomNumberToolInput{Min: 1, Max: 1000000}

	seed42, err := RandomNumberSeededToolDef.RunFunction(input, map[string]interface{}{"seed": float64(42)})
	if err != nil {
		t.Fatalf("seed 42 call failed: %v", err)
	}

	seed43, err := RandomNumberSeededToolDef.RunFunction(input, map[string]interface{}{"seed": float64(43)})
	if err != nil {
		t.Fatalf("seed 43 call failed: %v", err)
	}

	if seed42 == seed43 {
		t.Fatalf("expected different seeds to produce different values, both were %q", seed42)
	}
}

func TestRandomNumberSeededToolRequiresIntegerSeed(t *testing.T) {
	input := RandomNumberToolInput{Min: 1, Max: 100}

	if _, err := RandomNumberSeededToolDef.RunFunction(input, map[string]interface{}{}); err == nil {
		t.Fatalf("expected error for missing seed")
	}

	if _, err := RandomNumberSeededToolDef.RunFunction(input, map[string]interface{}{"seed": "42"}); err == nil {
		t.Fatalf("expected error for non-integer seed type")
	}

	if _, err := RandomNumberSeededToolDef.RunFunction(input, map[string]interface{}{"seed": 42.5}); err == nil {
		t.Fatalf("expected error for non-integer seed value")
	}
}
