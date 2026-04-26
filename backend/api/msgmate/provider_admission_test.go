package msgmate

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"backend/logging"
)

// ci#620: the effective provider admission cap used to be pinned at the literal
// default of 3, because the shared-config default merge wrote
// `coder_provider_max_concurrency: 3` into every native coder agent and the
// deployment lever `OCI_MSGMATE_CODER_PROVIDER_MAX_CONCURRENCY` was only read
// when that key was *absent*. These tests pin the sizing/admission decisions.

func withEnv(t *testing.T, key, value string) {
	t.Helper()
	previous, had := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, previous)
			return
		}
		_ = os.Unsetenv(key)
	})
}

// TestResolveProviderAdmissionConfigEnvBeatsMaterialisedDefault is the regression
// test for the ticket: a materialised default of 3 in the agent config must not
// shadow the deployment env.
func TestResolveProviderAdmissionConfigEnvBeatsMaterialisedDefault(t *testing.T) {
	withEnv(t, ProviderMaxConcurrencyEnvKey, "12")
	config := map[string]interface{}{
		// What the shared-config default merge writes into every coder agent.
		"coder_provider_max_concurrency": 3,
		"coder_interaction":              "autonomous",
	}
	resolved := ResolveProviderAdmissionConfig(config)
	if resolved.MaxConcurrency != 12 {
		t.Fatalf("MaxConcurrency = %d, want the env value 12 (the materialised default 3 must not win)", resolved.MaxConcurrency)
	}
	if resolved.Source != "env:"+ProviderMaxConcurrencyEnvKey {
		t.Fatalf("Source = %q, want the env source", resolved.Source)
	}
}

// TestResolveProviderAdmissionConfigWithoutEnvUsesAgentValue keeps the per-agent
// key working for a deployment that does not set the env lever.
func TestResolveProviderAdmissionConfigWithoutEnvUsesAgentValue(t *testing.T) {
	withEnv(t, ProviderMaxConcurrencyEnvKey, "")
	config := map[string]interface{}{"coder_provider_max_concurrency": 7}
	if got := ResolveProviderAdmissionConfig(config).MaxConcurrency; got != 7 {
		t.Fatalf("MaxConcurrency = %d, want 7 from the agent config", got)
	}
}

// TestResolveProviderAdmissionConfigDefaultsToThree pins the documented default.
func TestResolveProviderAdmissionConfigDefaultsToThree(t *testing.T) {
	withEnv(t, ProviderMaxConcurrencyEnvKey, "")
	resolved := ResolveProviderAdmissionConfig(map[string]interface{}{})
	if resolved.MaxConcurrency != DefaultProviderMaxConcurrency || DefaultProviderMaxConcurrency != 3 {
		t.Fatalf("default MaxConcurrency = %d, want 3", resolved.MaxConcurrency)
	}
	if resolved.Source != "default" {
		t.Fatalf("Source = %q, want default", resolved.Source)
	}
}

// TestResolveProviderAdmissionConfigRespectsPoolBound keeps the ci#602 alignment:
// the provider cap never exceeds the pooled sandbox bound.
func TestResolveProviderAdmissionConfigRespectsPoolBound(t *testing.T) {
	withEnv(t, ProviderMaxConcurrencyEnvKey, "10")
	config := map[string]interface{}{"coder_sandbox_max_agents_per_pool": 4}
	resolved := ResolveProviderAdmissionConfig(config)
	if resolved.MaxConcurrency != 4 {
		t.Fatalf("MaxConcurrency = %d, want the pool bound 4", resolved.MaxConcurrency)
	}
}

// TestAdmissionLimiterResizesAtRuntime covers "changeable via config without a
// redeploy": raising the cap admits more requests immediately, and lowering it
// never cuts an in-flight request off.
func TestAdmissionLimiterResizesAtRuntime(t *testing.T) {
	resetProviderAdmissionForTest()
	withEnv(t, ProviderMaxConcurrencyEnvKey, "")
	policy := ProviderAdmissionConfig{MaxConcurrency: 2, QueueTimeout: 50 * time.Millisecond, Source: "test"}

	release := make([]func(), 0, 4)
	for i := 0; i < 2; i++ {
		r, _, err := providerAdmissionLimiterFor("test-key", policy).acquire(context.Background())
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		release = append(release, r)
	}

	// A third request queues and reports saturation instead of firing into the
	// provider's in-flight cap.
	if _, _, err := providerAdmissionLimiterFor("test-key", policy).acquire(context.Background()); err == nil {
		t.Fatalf("the third acquire at max_concurrency=2 must report saturation")
	} else if _, ok := err.(*ProviderSaturationError); !ok {
		t.Fatalf("err = %T (%v), want *ProviderSaturationError", err, err)
	}

	// Raise the cap at runtime: the next acquire is admitted immediately.
	raised := policy
	raised.MaxConcurrency = 4
	extraRelease, _, err := providerAdmissionLimiterFor("test-key", raised).acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire after raising the cap: %v", err)
	}

	snapshot := providerAdmissionLimiterFor("test-key", raised).snapshot()
	if snapshot.MaxConcurrency != 4 {
		t.Fatalf("effective cap = %d, want the raised cap 4", snapshot.MaxConcurrency)
	}
	if snapshot.InFlight != 3 {
		t.Fatalf("in_flight = %d, want 3", snapshot.InFlight)
	}

	// Lower the cap again: the in-flight requests are not cut off, but no new
	// admission is granted until the fleet drains below the new cap.
	lowered := policy
	lowered.MaxConcurrency = 1
	blocked := lowered
	blocked.QueueTimeout = 20 * time.Millisecond
	if _, _, err := providerAdmissionLimiterFor("test-key", blocked).acquire(context.Background()); err == nil {
		t.Fatalf("acquire at a lowered cap with 3 in flight must report saturation")
	} else if _, ok := err.(*ProviderSaturationError); !ok {
		t.Fatalf("err = %T, want *ProviderSaturationError", err)
	}

	release[0]()
	release[1]()
	extraRelease()
	if got := providerAdmissionLimiterFor("test-key", lowered).snapshot().InFlight; got != 0 {
		t.Fatalf("in_flight after releasing = %d, want 0", got)
	}
}

// TestAdmissionLimiterAdmitsConfiguredConcurrency checks that the configured cap
// is really admitted in parallel — the throughput the development loop depends
// on — and that it is observable through the status snapshot.
func TestAdmissionLimiterAdmitsConfiguredConcurrency(t *testing.T) {
	resetProviderAdmissionForTest()
	withEnv(t, ProviderMaxConcurrencyEnvKey, "5")
	policy := ResolveProviderAdmissionConfig(map[string]interface{}{"coder_provider_max_concurrency": 3})
	if policy.MaxConcurrency != 5 {
		t.Fatalf("resolved cap = %d, want the env value 5", policy.MaxConcurrency)
	}

	var wg sync.WaitGroup
	releases := make(chan func(), policy.MaxConcurrency)
	for i := 0; i < policy.MaxConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, _, err := providerAdmissionLimiterFor("kept-key", policy).acquire(context.Background())
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			releases <- release
		}()
	}
	wg.Wait()
	close(releases)

	status := ProviderAdmissionStatus()
	if len(status) != 1 {
		t.Fatalf("status entries = %d, want 1", len(status))
	}
	if status[0].MaxConcurrency != 5 || status[0].InFlight != 5 {
		t.Fatalf("status = %+v, want max_concurrency=5 in_flight=5", status[0])
	}
	if !status[0].Saturated {
		t.Fatalf("status = %+v, want saturated=true at the cap", status[0])
	}
	for release := range releases {
		release()
	}
	if got := ProviderAdmissionStatus()[0].InFlight; got != 0 {
		t.Fatalf("in_flight after release = %d, want 0", got)
	}
}

// TestAdmissionDisabledAdmitsEverything keeps the zero policy a no-op so a
// non-coder deployment is unaffected.
func TestAdmissionDisabledAdmitsEverything(t *testing.T) {
	resetProviderAdmissionForTest()
	for i := 0; i < 10; i++ {
		release, err := admitProviderRequest(context.Background(), "noop", ProviderAdmissionConfig{})
		if err != nil {
			t.Fatalf("admitProviderRequest: %v", err)
		}
		release()
	}
	if got := len(ProviderAdmissionStatus()); got != 0 {
		t.Fatalf("status entries = %d, want 0 when admission is disabled", got)
	}
}

// TestAdmissionKeyIsSecretFree pins that the admission identity never carries key
// material.
func TestAdmissionKeyIsSecretFree(t *testing.T) {
	key := ProviderAdmissionKeyFor("openrouter", "https://openrouter.ai/api/v1/", "sk-or-supersecret")
	if strings.Contains(key, "supersecret") {
		t.Fatalf("admission key %q leaks key material", key)
	}
	if !strings.Contains(key, "openrouter") {
		t.Fatalf("admission key %q must identify the backend", key)
	}
}

// TestProviderAdmissionLogLevels keeps the ci#645 mapping honest for this call
// site. The routine "effective policy changed" line is bookkeeping, not a
// failure, so it must stay below the default info level; a genuinely saturated
// key is the operator-relevant signal and stays a warning. An earlier revision
// of this file logged the routine line at error level and the saturation at
// debug, which is exactly the heaviness ci#645 complains about.
func TestProviderAdmissionLogLevels(t *testing.T) {
	buf := &bytes.Buffer{}
	reconfigure := func(level string) {
		t.Helper()
		if err := logging.Configure(logging.Options{Level: level, Format: "text", Writer: buf}); err != nil {
			t.Fatalf("configure logging: %v", err)
		}
	}
	t.Cleanup(func() {
		_ = logging.Configure(logging.Options{Level: "info", Format: "text", Writer: io.Discard})
	})

	routine := ProviderAdmissionSnapshot{Key: "log-level-key", MaxConcurrency: 3, InFlight: 0, Source: "test"}
	saturated := ProviderAdmissionSnapshot{Key: "log-level-key", MaxConcurrency: 3, InFlight: 3, Source: "test"}

	// At info the routine change line is silenced.
	buf.Reset()
	resetProviderAdmissionForTest()
	reconfigure("info")
	logProviderAdmissionState(routine)
	if strings.Contains(buf.String(), "effective_max_concurrency") {
		t.Fatalf("the routine admission change must not log at info, got %q", buf.String())
	}

	// A saturated key warns even at info, and never at error level.
	buf.Reset()
	resetProviderAdmissionForTest()
	logProviderAdmissionState(saturated) // first observation: routine change line
	buf.Reset()
	logProviderAdmissionState(saturated)
	if !strings.Contains(buf.String(), "saturated") {
		t.Fatalf("a saturated key must warn at info, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "level=WARN") {
		t.Fatalf("saturation must be a warning, got %q", buf.String())
	}
	if strings.Contains(buf.String(), "level=ERROR") {
		t.Fatalf("saturation is not a failure and must not log at error level, got %q", buf.String())
	}

	// At debug the routine change line is visible again for diagnosis.
	buf.Reset()
	resetProviderAdmissionForTest()
	reconfigure("debug")
	logProviderAdmissionState(routine)
	if !strings.Contains(buf.String(), "effective_max_concurrency") {
		t.Fatalf("the routine admission change must be visible at debug, got %q", buf.String())
	}
	if strings.Contains(buf.String(), "level=ERROR") {
		t.Fatalf("the routine admission change must not log at error level, got %q", buf.String())
	}
}
