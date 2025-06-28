package msgmate

import (
	"strings"
	"testing"
)

func TestResolveProviderEndpointMsgmateCluster(t *testing.T) {
	t.Run("env host overrides config endpoint", func(t *testing.T) {
		t.Setenv("MSGMATE_CLUSTER_HOST", "https://cluster.example.com/v1")
		got, err := resolveProviderEndpoint("msgmate_cluster", "https://config.example.com/v1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "https://cluster.example.com/v1" {
			t.Fatalf("expected env host, got %q", got)
		}
	})

	t.Run("config endpoint used when env host unset", func(t *testing.T) {
		t.Setenv("MSGMATE_CLUSTER_HOST", "")
		got, err := resolveProviderEndpoint("msgmate_cluster", "https://config.example.com/v1/")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "https://config.example.com/v1" {
			t.Fatalf("expected trimmed config endpoint, got %q", got)
		}
	})

	t.Run("missing host errors", func(t *testing.T) {
		t.Setenv("MSGMATE_CLUSTER_HOST", "")
		if _, err := resolveProviderEndpoint("msgmate_cluster", ""); err == nil {
			t.Fatalf("expected error for missing cluster host")
		} else if !strings.Contains(err.Error(), "msgmate_cluster") {
			t.Fatalf("expected error to name the provider, got %v", err)
		}
	})
}

func TestResolveProviderEndpointLitellm(t *testing.T) {
	t.Setenv("LITELLM_API_HOST", "https://litellm.example.com/v1")
	got, err := resolveProviderEndpoint("litellm", "https://config.example.com/v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://litellm.example.com/v1" {
		t.Fatalf("expected env host, got %q", got)
	}
}

func TestResolveProviderEndpointAnthropicFallback(t *testing.T) {
	t.Setenv("ANTHROPIC_API_HOST", "")
	got, err := resolveProviderEndpoint("anthropic", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.anthropic.com/v1" {
		t.Fatalf("expected anthropic public api fallback, got %q", got)
	}
}

func TestResolveProviderEndpointPassthrough(t *testing.T) {
	got, err := resolveProviderEndpoint("deepinfra", "https://api.deepinfra.com/v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.deepinfra.com/v1" {
		t.Fatalf("expected passthrough endpoint, got %q", got)
	}
}
