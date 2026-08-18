package bots

import (
	"errors"
	"strings"
	"testing"
)

// canaryConfigDetail is the detail the Live Runtime Canary read from a bot whose
// configured `coder_select_workspace` had no stored tool_init. Before this
// change the create-interaction envelope prefixed its own stage label onto an
// error that already carried the same label, so the body was:
//
//	HTTP 400 code=invalid_interaction_config body={"error":"invalid tools/tool_init:
//	default_shared_config invalid tools/tool_init: missing tool_init for required
//	tool \"coder_select_workspace\""}
//
// — the same failure named twice, and long enough that CI logs truncate it.
const canaryConfigDetail = `default_shared_config invalid tools/tool_init: missing tool_init for required tool "coder_select_workspace"`

// TestConfigValidationMessageCollapsesTheDuplicatedStageLabel pins the actual
// canary payload: the envelope must label the stage exactly once while keeping
// the validator's field context and the actionable tool name, so the next canary
// run names the missing tool_init without repeating the stage (ci#865).
func TestConfigValidationMessageCollapsesTheDuplicatedStageLabel(t *testing.T) {
	got := configValidationMessage(toolInitStagePrefix, errors.New(canaryConfigDetail))
	want := `invalid tools/tool_init in default_shared_config: missing tool_init for required tool "coder_select_workspace"`
	if got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
	if n := strings.Count(got, toolInitStagePrefix); n != 1 {
		t.Fatalf("stage label appears %d times in %q, want exactly once", n, got)
	}
	if strings.Contains(got, "default_shared_config invalid") {
		t.Fatalf("message still repeats the field + stage pair: %q", got)
	}
	// The actionable part must survive the collapse.
	if !strings.Contains(got, "coder_select_workspace") {
		t.Fatalf("message lost the offending tool name: %q", got)
	}
}

// TestConfigValidationMessageIsIdempotent makes sure a message that already
// leads with the envelope's label is not labelled again, so the helper can be
// applied at more than one layer without reintroducing the duplication.
func TestConfigValidationMessageIsIdempotent(t *testing.T) {
	once := configValidationMessage(toolInitStagePrefix, errors.New(canaryConfigDetail))
	twice := configValidationMessage(toolInitStagePrefix, errors.New(once))
	if once != twice {
		t.Fatalf("second pass changed the message: %q -> %q", once, twice)
	}
}

func TestConfigValidationMessagePrefixesUnlabelledDetail(t *testing.T) {
	got := configValidationMessage(integrationsStagePrefix, errors.New(`integration "gh" not found or not enabled`))
	want := `invalid integrations: integration "gh" not found or not enabled`
	if got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

func TestConfigValidationMessageKeepsUnrelatedFieldDetail(t *testing.T) {
	got := configValidationMessage(toolInitStagePrefix, errors.New("default_shared_config must be an object"))
	want := "invalid tools/tool_init: default_shared_config must be an object"
	if got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

// TestConfigValidationMessageBareMarker handles a marker with no trailing
// detail: the collapsed message must not end in a dangling ": ".
func TestConfigValidationMessageBareMarker(t *testing.T) {
	got := configValidationMessage(toolInitStagePrefix, errors.New("default_shared_config "+toolInitStagePrefix))
	want := "invalid tools/tool_init in default_shared_config"
	if got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

// TestConfigValidationMessageNeverEmpty keeps the envelope parseable: the
// canary calls response.json() on every rejection (ci#865), so even a nil error
// must yield a non-empty message.
func TestConfigValidationMessageNeverEmpty(t *testing.T) {
	for _, prefix := range []string{toolInitStagePrefix, integrationsStagePrefix} {
		if got := configValidationMessage(prefix, nil); got != prefix {
			t.Fatalf("nil error message = %q, want %q", got, prefix)
		}
	}
}
