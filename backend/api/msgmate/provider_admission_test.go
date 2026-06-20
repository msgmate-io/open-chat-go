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
	if got := resolved.LimitingSource(); got != "pool:coder_sandbox_max_agents_per_pool" {
		t.Fatalf("LimitingSource = %q, want the pool bound to be named as the winning bound", got)
	}
}

// ci#620 follow-up: the saturation message used to blame the deployment env
// lever even when a lower bound (the ci#602 pool clamp) decided the effective
// cap, which sends an operator to raise a lever that cannot change anything. It
// now names the bound that won, and the same answer is machine-readable on the
// policy and the status snapshot.
func TestSaturationErrorNamesTheWinningBound(t *testing.T) {
	t.Run("pool clamp wins over the env lever", func(t *testing.T) {
		withEnv(t, ProviderMaxConcurrencyEnvKey, "32")
		// Not a realistic pair, just the operator's exact symptom: the env
		// lever is set yet the effective cap is a lower pool bound.
		policy := ResolveProviderAdmissionConfig(map[string]interface{}{
			"coder_sandbox_max_agents_per_pool": 3,
		})
		if policy.MaxConcurrency != 3 {
			t.Fatalf("MaxConcurrency = %d, want the pool bound 3", policy.MaxConcurrency)
		}
		err := &ProviderSaturationError{Key: "openrouter|x|key-len73", MaxConcurrency: policy.MaxConcurrency, InFlight: 3, QueueWaiting: 20, Waited: 2 * time.Minute, Source: policy.Source}
		message := err.Error()
		if !strings.Contains(message, "clamped by the pooled sandbox bound coder_sandbox_max_agents_per_pool") {
			t.Fatalf("message %q must name the pool bound that actually decided the cap", message)
		}
		if !strings.Contains(message, "raising "+ProviderMaxConcurrencyEnvKey+" has no effect") {
			t.Fatalf("message %q must say that raising the env lever cannot help", message)
		}
		if !strings.Contains(message, "max_concurrency=3") {
			t.Fatalf("message %q must still carry the effective cap", message)
		}
		if err.LimitingSource() != "pool:coder_sandbox_max_agents_per_pool" {
			t.Fatalf("LimitingSource = %q", err.LimitingSource())
		}
	})

	t.Run("env lever is named when it is the winning bound", func(t *testing.T) {
		withEnv(t, ProviderMaxConcurrencyEnvKey, "32")
		policy := ResolveProviderAdmissionConfig(map[string]interface{}{})
		if policy.LimitingSource() != "env:"+ProviderMaxConcurrencyEnvKey {
			t.Fatalf("LimitingSource = %q, want the env lever", policy.LimitingSource())
		}
		err := &ProviderSaturationError{Key: "k", MaxConcurrency: policy.MaxConcurrency, Source: policy.Source}
		if got := err.Advice(); !strings.Contains(got, "raise "+ProviderMaxConcurrencyEnvKey) {
			t.Fatalf("Advice() = %q, want the env lever", got)
		}
	})

	t.Run("agent key is named when the env lever is unset", func(t *testing.T) {
		withEnv(t, ProviderMaxConcurrencyEnvKey, "")
		policy := ResolveProviderAdmissionConfig(map[string]interface{}{"coder_provider_max_concurrency": 7})
		if policy.LimitingSource() != "agent:coder_provider_max_concurrency" {
			t.Fatalf("LimitingSource = %q, want the agent key", policy.LimitingSource())
		}
	})

	t.Run("no resolved policy falls back to the deployment lever", func(t *testing.T) {
		if got := (&ProviderSaturationError{Key: "k"}).LimitingSource(); got != "" {
			t.Fatalf("LimitingSource = %q, want empty without a resolved policy", got)
		}
		if got := (&ProviderSaturationError{Key: "k"}).Advice(); !strings.Contains(got, "raise "+ProviderMaxConcurrencyEnvKey) {
			t.Fatalf("Advice() = %q, want the env lever fallback", got)
		}
	})
}

// TestSaturationLimiterReportsItsWinningBound checks the wire from the limiter to
// the snapshot: a saturated limiter carries the bound that decided its cap, so
// the admin status can answer "which lever do I raise?" without guessing.
func TestSaturationLimiterReportsItsWinningBound(t *testing.T) {
	resetProviderAdmissionForTest()
	withEnv(t, ProviderMaxConcurrencyEnvKey, "32")
	policy := ResolveProviderAdmissionConfig(map[string]interface{}{"coder_sandbox_max_agents_per_pool": 1})
	policy.QueueTimeout = 20 * time.Millisecond

	release, _, err := providerAdmissionLimiterFor("bound-key", policy).acquire(context.Background(), providerAdmissionSlotMeta{})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	_, _, err = providerAdmissionLimiterFor("bound-key", policy).acquire(context.Background(), providerAdmissionSlotMeta{})
	saturation, ok := err.(*ProviderSaturationError)
	if !ok {
		t.Fatalf("err = %T (%v), want *ProviderSaturationError", err, err)
	}
	if saturation.LimitingSource() != "pool:coder_sandbox_max_agents_per_pool" {
		t.Fatalf("LimitingSource = %q, want the pool bound", saturation.LimitingSource())
	}

	snapshot := providerAdmissionLimiterFor("bound-key", policy).snapshot()
	if snapshot.LimitingSource != "pool:coder_sandbox_max_agents_per_pool" {
		t.Fatalf("snapshot.LimitingSource = %q, want the pool bound (config_source=%q)", snapshot.LimitingSource, snapshot.Source)
	}
	if snapshot.Source != "env:"+ProviderMaxConcurrencyEnvKey+"+pool:coder_sandbox_max_agents_per_pool" {
		t.Fatalf("snapshot.Source = %q, want the full provenance preserved", snapshot.Source)
	}
	release()
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
		r, _, err := providerAdmissionLimiterFor("test-key", policy).acquire(context.Background(), providerAdmissionSlotMeta{})
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		release = append(release, r)
	}

	// A third request queues and reports saturation instead of firing into the
	// provider's in-flight cap.
	if _, _, err := providerAdmissionLimiterFor("test-key", policy).acquire(context.Background(), providerAdmissionSlotMeta{}); err == nil {
		t.Fatalf("the third acquire at max_concurrency=2 must report saturation")
	} else if _, ok := err.(*ProviderSaturationError); !ok {
		t.Fatalf("err = %T (%v), want *ProviderSaturationError", err, err)
	}

	// Raise the cap at runtime: the next acquire is admitted immediately.
	raised := policy
	raised.MaxConcurrency = 4
	extraRelease, _, err := providerAdmissionLimiterFor("test-key", raised).acquire(context.Background(), providerAdmissionSlotMeta{})
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
	if _, _, err := providerAdmissionLimiterFor("test-key", blocked).acquire(context.Background(), providerAdmissionSlotMeta{}); err == nil {
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
			release, _, err := providerAdmissionLimiterFor("kept-key", policy).acquire(context.Background(), providerAdmissionSlotMeta{})
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
		release, err := admitProviderRequest(context.Background(), "noop", ProviderAdmissionConfig{}, providerAdmissionSlotMeta{})
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

	// Derive both snapshots from a real limiter instead of hand-building the
	// struct: Saturated is computed by the limiter (in_flight >= effective cap)
	// and a literal that forgets to set it silently disarms the assertion below,
	// which is exactly how this test went red in CI (got "" for the warn line).
	policy := ProviderAdmissionConfig{MaxConcurrency: 1, Source: "test"}
	limiter := providerAdmissionLimiterFor("log-level-key", policy)
	routine := limiter.snapshot()
	release, _, err := limiter.acquire(context.Background(), providerAdmissionSlotMeta{})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	t.Cleanup(release)
	saturated := limiter.snapshot()
	if !saturated.Saturated {
		t.Fatalf("snapshot = %+v, want a saturated limiter at its cap", saturated)
	}

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

// ci#799: the shared OpenRouter admission key stayed pinned at
// `in_flight=20` at its cap for 2h+ with no coder sandbox pods and no live
// generation behind the slots — the development lead could not acquire a slot
// and only a backend restart cleared the counter. The root cause was that a
// slot was retained when its request ended without reaching its `defer
// release()` (cancelled turn, timeout, retry-exhausted task, a stream reader
// that never returned). These tests pin the per-slot tracking, the defensive
// release and the reclaim path.

// TestAdmissionSlotReleasedOnContextCancel is the ticket's regression test: a
// request acquires a slot, its context is cancelled/abandoned (the turn never
// reaches the release), and in_flight must return to 0 instead of pinning the
// cap. Previously the slot leaked and the key stayed saturated forever.
func TestAdmissionSlotReleasedOnContextCancel(t *testing.T) {
	resetProviderAdmissionForTest()
	policy := ProviderAdmissionConfig{MaxConcurrency: 1, Source: "test"}

	ctx, cancel := context.WithCancel(context.Background())
	limiter := providerAdmissionLimiterFor("cancel-key", policy)
	release, _, err := limiter.acquire(ctx, providerAdmissionSlotMeta{
		RequestID: "provider-request:chat-1#1",
		ChatID:    "chat-1",
		Attempt:   1,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if got := limiter.snapshot().InFlight; got != 1 {
		t.Fatalf("in_flight after acquire = %d, want 1", got)
	}

	// The cancelled turn never calls release.
	cancel()
	waitForInFlight(t, limiter, 0)

	if got := limiter.snapshot().InFlight; got != 0 {
		t.Fatalf("in_flight after ctx cancel = %d, want 0 (the ci#799 leak)", got)
	}
	// The slot must be gone from the per-slot view too.
	if len(limiter.snapshot().Slots) != 0 {
		t.Fatalf("slots after ctx cancel = %+v, want none", limiter.snapshot().Slots)
	}
	// The defensive release is idempotent: a late `defer release()` must not
	// drive the counter negative.
	release()
	if got := limiter.snapshot().InFlight; got != 0 {
		t.Fatalf("in_flight after late release = %d, want 0", got)
	}
}

// TestAdmissionSlotReleasedOnRequestTimeout pins the timeout arm of the same
// regression: a request whose context expired (the HTTP client timeout) must
// give the slot back even though the caller is still blocked on the stream.
func TestAdmissionSlotReleasedOnRequestTimeout(t *testing.T) {
	resetProviderAdmissionForTest()
	policy := ProviderAdmissionConfig{MaxConcurrency: 1, Source: "test"}
	limiter := providerAdmissionLimiterFor("timeout-key", policy)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, _, err := limiter.acquire(ctx, providerAdmissionSlotMeta{RequestID: "req-timeout", Attempt: 2}); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Do not release: wait for the deadline to fire.
	waitForInFlight(t, limiter, 0)
	if got := limiter.snapshot().InFlight; got != 0 {
		t.Fatalf("in_flight after request timeout = %d, want 0", got)
	}
}

// TestAdmissionSlotAttributedInSnapshot checks the ticket's diagnosability
// criterion: a stalled holder must be visible in the admin snapshot with its
// age and its chat/request identity, so an operator can see *who* pins the cap
// instead of only the aggregate count.
func TestAdmissionSlotAttributedInSnapshot(t *testing.T) {
	resetProviderAdmissionForTest()
	policy := ProviderAdmissionConfig{MaxConcurrency: 2, Source: "test"}
	limiter := providerAdmissionLimiterFor("attr-key", policy)

	releaseFirst, _, err := limiter.acquire(context.Background(), providerAdmissionSlotMeta{
		RequestID: providerAdmissionRequestID("chat-alpha", 3),
		ChatID:    "chat-alpha",
		Attempt:   3,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer releaseFirst()
	releaseSecond, _, err := limiter.acquire(context.Background(), providerAdmissionSlotMeta{
		RequestID: providerAdmissionRequestID("chat-beta", 1),
		ChatID:    "chat-beta",
		Attempt:   1,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer releaseSecond()

	snapshot := limiter.snapshot()
	if len(snapshot.Slots) != 2 {
		t.Fatalf("slots = %+v, want 2 attributed slots", snapshot.Slots)
	}
	if snapshot.InFlightMaxAge <= 0 {
		t.Fatalf("in_flight_max_age = %s, want a positive oldest-slot age", snapshot.InFlightMaxAge)
	}
	if snapshot.StaleTimeout != ProviderRequestClientTimeout {
		t.Fatalf("stale_timeout = %s, want the provider client timeout %s", snapshot.StaleTimeout, ProviderRequestClientTimeout)
	}
	// The slots are secret-free: identities only, never key material.
	seen := map[string]bool{}
	for _, slot := range snapshot.Slots {
		if slot.AcquiredAt == "" || slot.Age < 0 || slot.SlotID == 0 {
			t.Fatalf("slot = %+v, want id/acquired_at/age populated", slot)
		}
		if slot.ContextDone || slot.Stale {
			t.Fatalf("slot = %+v, want a fresh live slot", slot)
		}
		seen[slot.ChatID] = true
	}
	if !seen["chat-alpha"] || !seen["chat-beta"] {
		t.Fatalf("slots = %+v, want the chat attribution of both holders", snapshot.Slots)
	}

	// The saturation log line must name the oldest holder (ci#799), so a stalled
	// key is diagnosable from the log alone when the api is not reachable.
	line := providerAdmissionSlotHolderSuffix(snapshot)
	if !strings.Contains(line, "oldest_holder") || !strings.Contains(line, "chat_id=") {
		t.Fatalf("holder suffix = %q, want the oldest holder attribution", line)
	}
}

// TestAdmissionWatchdogReleasesStaleSlotWithDoneContext is the leak the ticket
// describes: a slot outlived the provider client timeout with no live
// generation behind it. The watchdog must reclaim it, so in_flight cannot stay
// at the cap forever (`stale_timeout` is configurable here instead of waiting
// the real 300s).
func TestAdmissionWatchdogReleasesStaleSlotWithDoneContext(t *testing.T) {
	resetProviderAdmissionForTest()
	policy := ProviderAdmissionConfig{
		MaxConcurrency:   1,
		Source:           "test",
		StaleTimeout:     20 * time.Millisecond,
		WatchdogInterval: time.Hour, // drive the sweep explicitly below
	}
	limiter := providerAdmissionLimiterFor("stale-key", policy)
	limiter.mu.Lock()
	limiter.watchdogEvery = time.Hour
	limiter.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	if _, _, err := limiter.acquire(ctx, providerAdmissionSlotMeta{RequestID: "req-stale", ChatID: "chat-stale", Attempt: 1}); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// Simulate the leak shape exactly: the context is done but the slot is still
	// counted (the observer that would have released it is gone), so only the
	// watchdog can reclaim it.
	limiter.markContextDone(limiter.slotForTest(1))
	if got := limiter.snapshot().InFlight; got != 1 {
		t.Fatalf("in_flight after acquire = %d, want 1", got)
	}

	ageSlotForTest(limiter, 3*policy.StaleTimeout/2)

	// Before the sweep the stale slot is already visible in the snapshot.
	staleSnapshot := limiter.snapshot()
	if staleSnapshot.StaleSlots != 1 {
		t.Fatalf("stale_slots = %d, want the aged slot flagged (snapshot %+v)", staleSnapshot.StaleSlots, staleSnapshot)
	}
	if !staleSnapshot.Slots[0].Stale || !staleSnapshot.Slots[0].ContextDone {
		t.Fatalf("slot = %+v, want context_done+stale", staleSnapshot.Slots[0])
	}

	if reclaimed := limiter.sweepStaleSlots(time.Now()); reclaimed != 1 {
		t.Fatalf("sweep reclaimed %d slots, want 1", reclaimed)
	}
	if got := limiter.snapshot().InFlight; got != 0 {
		t.Fatalf("in_flight after watchdog sweep = %d, want 0", got)
	}
	if got := limiter.snapshot().ReclaimedStaleTotal; got != 1 {
		t.Fatalf("reclaimed_stale_total = %d, want 1", got)
	}
	// The watchdog release is idempotent: a late `defer release()` is a no-op.
	cancel()
	time.Sleep(10 * time.Millisecond)
	if got := limiter.snapshot().InFlight; got != 0 {
		t.Fatalf("in_flight after late release = %d, want 0", got)
	}
}

// TestAdmissionWatchdogKeepsLiveLongRequest guards the other side of the
// watchdog: a genuinely long-but-live request (context not done) must not be
// reclaimed at the stale threshold, or a legitimately slow generation would be
// cut off from its own slot accounting. Only a slot that also exceeded a second
// threshold is reclaimed.
func TestAdmissionWatchdogKeepsLiveLongRequest(t *testing.T) {
	resetProviderAdmissionForTest()
	policy := ProviderAdmissionConfig{
		MaxConcurrency: 1,
		Source:         "test",
		StaleTimeout:   20 * time.Millisecond,
	}
	limiter := providerAdmissionLimiterFor("live-key", policy)
	if _, _, err := limiter.acquire(context.Background(), providerAdmissionSlotMeta{RequestID: "req-live"}); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// Age the slot deterministically past the stale threshold (1.5x) while its
	// context stays live.
	ageSlotForTest(limiter, 3*policy.StaleTimeout/2)

	// Past the stale threshold but with a live context: not reclaimed yet.
	if reclaimed := limiter.sweepStaleSlots(time.Now()); reclaimed != 0 {
		t.Fatalf("sweep reclaimed %d live slots, want 0", reclaimed)
	}
	if got := limiter.snapshot().InFlight; got != 1 {
		t.Fatalf("in_flight after sweep = %d, want the live slot kept", got)
	}

	// Past twice the threshold with still no completed context: this is the
	// shape of the ci#799 leak (a stream reader that never returns), so the
	// watchdog finally reclaims it.
	ageSlotForTest(limiter, 3*policy.StaleTimeout)
	if reclaimed := limiter.sweepStaleSlots(time.Now()); reclaimed != 1 {
		t.Fatalf("sweep reclaimed %d slots, want the twice-threshold slot reclaimed", reclaimed)
	}
	if got := limiter.snapshot().InFlight; got != 0 {
		t.Fatalf("in_flight after second sweep = %d, want 0", got)
	}
}

// ageSlotForTest backdates every tracked slot by by, so the watchdog threshold
// can be exercised deterministically instead of sleeping for the real provider
// client timeout.
func ageSlotForTest(limiter *providerAdmissionLimiter, by time.Duration) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	for _, slot := range limiter.slots {
		slot.acquiredAt = slot.acquiredAt.Add(-by)
	}
}

// TestAdmissionSlotsDoNotPinCapAcrossCancels reproduces the ticket's end-to-end
// symptom at the limiter level: many requests are admitted and then abandoned
// without a release, and the effective cap must not stay pinned. With the old
// behaviour the accumulated leaks kept in_flight at max_concurrency and the
// next acquire timed out (`no slot within 15m0s`).
func TestAdmissionSlotsDoNotPinCapAcrossCancels(t *testing.T) {
	resetProviderAdmissionForTest()
	policy := ProviderAdmissionConfig{MaxConcurrency: 3, Source: "test", QueueTimeout: 200 * time.Millisecond}
	limiter := providerAdmissionLimiterFor("pin-key", policy)

	for round := 0; round < 3; round++ {
		cancelable := make([]context.Context, 0, policy.MaxConcurrency)
		cancels := make([]context.CancelFunc, 0, policy.MaxConcurrency)
		for i := 0; i < policy.MaxConcurrency; i++ {
			ctx, cancel := context.WithCancel(context.Background())
			if _, _, err := limiter.acquire(ctx, providerAdmissionSlotMeta{RequestID: "abandoned", ChatID: "chat-lead", Attempt: round + 1}); err != nil {
				t.Fatalf("acquire round %d: %v", round, err)
			}
			cancelable = append(cancelable, ctx)
			cancels = append(cancels, cancel)
		}
		if got := limiter.snapshot().InFlight; got != policy.MaxConcurrency {
			t.Fatalf("round %d: in_flight = %d, want %d", round, got, policy.MaxConcurrency)
		}
		// Every request is abandoned without reaching its `defer release()`.
		for _, cancel := range cancels {
			cancel()
		}
		_ = cancelable
		waitForInFlight(t, limiter, 0)
	}

	// The cap is free again: the next acquire (what the development lead does)
	// must succeed without a backend restart.
	release, _, err := limiter.acquire(context.Background(), providerAdmissionSlotMeta{RequestID: "lead-turn", ChatID: "chat-lead", Attempt: 1})
	if err != nil {
		t.Fatalf("the cap stayed pinned after abandoned slots: %v", err)
	}
	release()
}

// waitForInFlight polls a limiter until its in-flight count reaches want or the
// deadline passes. The defensive release runs on a goroutine, so the test needs
// a short wait rather than an immediate assertion.
func waitForInFlight(t *testing.T, limiter *providerAdmissionLimiter, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if limiter.snapshot().InFlight == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// slotForTest resolves the tracked slot with the given id, so a test can
// simulate a leaked slot (context done, still counted) instead of only the
// clean release path.
func (l *providerAdmissionLimiter) slotForTest(id uint64) *providerAdmissionSlot {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.slots[id]
}
