package msgmate

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"backend/logging"
)

// This file is the **core-side** half of ci#620: the per-key provider
// admission control the native `msgmate_coder` runtime needs so a shared
// provider key (typically OpenRouter) is never asked for more concurrent
// requests than it will accept. A 402 "This request would exceed your
// available credits given your current in-flight requests" is an *in-flight*
// cap, not an account-credit cap: the answer is to queue the extra requests
// instead of erroring.
//
// Why the effective cap was stuck at the literal 3
// ------------------------------------------------
//
// The admission policy was resolved per agent from the shared config key
// `coder_provider_max_concurrency`, and the integration's shared-config default
// merge materialises that key with the value **3** into every native coder
// agent (`coderDefaultProviderMaxConcurrency = 3`, mirroring the loop
// controller's MAX_CONCURRENT default). The deployment lever
// `OCI_MSGMATE_CODER_PROVIDER_MAX_CONCURRENCY` was only consulted when the
// config key was *absent* — which it never is, because the default merge wrote
// a literal 3 into the config. The env var therefore never reached the
// resolved policy and the fleet was pinned at 3 concurrent provider requests no
// matter what the operator configured. The limiter itself compounded it: it
// only resized while nothing was in flight, so a busier fleet could never pick
// up a raised cap without a restart.
//
// The rules implemented here:
//
//  1. The **deployment env wins** over the per-agent key. The per-agent key is
//     routinely the materialised default, which is indistinguishable from an
//     operator's explicit choice, so it must not be able to shadow the
//     deployment lever. An environment that does not set the env key keeps the
//     per-agent value (and, failing that, the default).
//  2. The cap is bounded by the pooled sandbox bound
//     (`coder_sandbox_max_agents_per_pool`, ci#602) when that is smaller: a
//     fleet can never have more provider requests in flight than agents the
//     pool would run at once.
//  3. The limiter **grows immediately** on a raised cap (growing never cuts an
//     in-flight request off) and defers a lowered cap until in-flight drains
//     below it, so a config change takes effect without a redeploy.
//  4. The effective cap and the current in-flight/queue counts are logged and
//     exposed (`ProviderAdmissionStatus`), so the loop can self-throttle and an
//     operator can see what actually took effect.
//  5. A saturation message names the bound that **actually decided** the cap
//     (`ProviderSaturationError.Advice`, `LimitingSource` on the policy and the
//     snapshot), not just the env lever: when the ci#602 pool bound is what
//     clamped the cap down, raising the env var can never help and the message
//     has to say so (ci#620 follow-up).

const (
	// DefaultProviderMaxConcurrency is the in-flight cap used when neither the
	// deployment env nor the agent config names one. It mirrors the
	// development-loop controller's MAX_CONCURRENT default of 3 (ci#592).
	DefaultProviderMaxConcurrency = 3

	// ProviderMaxConcurrencyEnvKey is the deployment-wide provider in-flight cap.
	ProviderMaxConcurrencyEnvKey = "OCI_MSGMATE_CODER_PROVIDER_MAX_CONCURRENCY"

	// ProviderQueueTimeoutEnvKey is the deployment-wide admission queue timeout.
	ProviderQueueTimeoutEnvKey = "OCI_MSGMATE_CODER_PROVIDER_QUEUE_TIMEOUT_MS"

	// providerMaxConcurrencyConfigKey is the per-agent shared-config key.
	providerMaxConcurrencyConfigKey = "coder_provider_max_concurrency"

	// providerQueueTimeoutConfigKey is the per-agent queue timeout key.
	providerQueueTimeoutConfigKey = "coder_provider_queue_timeout_ms"

	// providerPoolBoundConfigKey is the pooled sandbox bound of ci#602.
	providerPoolBoundConfigKey = "coder_sandbox_max_agents_per_pool"

	// DefaultProviderQueueTimeout is how long an admission request waits for a
	// slot before it is reported as saturated. It is deliberately longer than a
	// typical provider completion so queued work still succeeds rather than
	// being dropped.
	DefaultProviderQueueTimeout = 120 * time.Second

	// ProviderRequestClientTimeout is the HTTP client timeout of one provider
	// request (`net/http` covers the entire exchange — response headers *and*
	// the body read — with Client.Timeout). It is the floor of the ci#799
	// stale-slot watchdog: a slot whose owning request context is already done
	// and that is older than this can no longer belong to a live generation.
	ProviderRequestClientTimeout = 300 * time.Second

	// ProviderAdmissionStaleTimeoutEnvKey overrides
	// ProviderRequestClientTimeout as the stale-slot threshold (milliseconds).
	ProviderAdmissionStaleTimeoutEnvKey = "OCI_MSGMATE_CODER_PROVIDER_ADMISSION_STALE_TIMEOUT_MS"

	// ProviderAdmissionWatchdogEnvKey overrides how often the per-key
	// stale-slot watchdog sweeps a limiter (milliseconds).
	ProviderAdmissionWatchdogEnvKey = "OCI_MSGMATE_CODER_PROVIDER_ADMISSION_WATCHDOG_MS"

	// DefaultProviderAdmissionWatchdogInterval is the sweep period of the
	// stale-slot watchdog.
	DefaultProviderAdmissionWatchdogInterval = 30 * time.Second
)

// Release reasons of an admission slot. They are recorded on the operator line
// a non-explicit release emits so a leaked slot is distinguishable from a
// normal request teardown (ci#799).
const (
	// releaseReasonExplicit is the caller's `defer release()`.
	releaseReasonExplicit = "explicit"
	// releaseReasonContextDone is the defensive release that fires when the
	// request context is cancelled, times out or the owning task is abandoned.
	releaseReasonContextDone = "context_done"
	// releaseReasonWatchdogStale is the watchdog recycling a slot that outlived
	// the provider client timeout with no live generation behind it.
	releaseReasonWatchdogStale = "watchdog_stale"
)

// ProviderAdmissionConfig is the resolved admission policy of one chat (or of
// the deployment, when no per-agent value exists).
type ProviderAdmissionConfig struct {
	// MaxConcurrency is the effective per-key in-flight cap. Zero or less means
	// "admit everything" (admission disabled).
	MaxConcurrency int
	// QueueTimeout bounds how long a request waits for a slot.
	QueueTimeout time.Duration
	// Source names where the effective cap came from, so a status line can say
	// which config value actually won ("env", "agent", "pool", "default").
	Source string
	// StaleTimeout is how old a slot whose request context is already done may
	// get before the ci#799 watchdog reclaims it. Zero falls back to
	// ProviderRequestClientTimeout.
	StaleTimeout time.Duration
	// WatchdogInterval is how often the stale-slot watchdog sweeps a limiter.
	// Zero falls back to DefaultProviderAdmissionWatchdogInterval.
	WatchdogInterval time.Duration
}

// String renders the policy as one operator-readable line.
func (c ProviderAdmissionConfig) String() string {
	return fmt.Sprintf("max_concurrency=%d source=%s queue_timeout=%s stale_timeout=%s",
		c.MaxConcurrency, c.Source, c.QueueTimeout.Round(time.Millisecond),
		c.StaleTimeoutValue().Round(time.Millisecond))
}

// StaleTimeoutValue is the effective ci#799 stale-slot threshold: the resolved
// policy value when the operator set one, the provider client timeout
// otherwise. It is what the watchdog compares a slot's age against.
func (c ProviderAdmissionConfig) StaleTimeoutValue() time.Duration {
	if c.StaleTimeout > 0 {
		return c.StaleTimeout
	}
	return ProviderRequestClientTimeout
}

// WatchdogIntervalValue is the effective sweep period of the stale-slot
// watchdog.
func (c ProviderAdmissionConfig) WatchdogIntervalValue() time.Duration {
	if c.WatchdogInterval > 0 {
		return c.WatchdogInterval
	}
	return DefaultProviderAdmissionWatchdogInterval
}

// LimitingSource returns the single bound that decided MaxConcurrency, in the
// same vocabulary as Source's tokens (`env:KEY`, `agent:KEY`, `pool:KEY`) or
// "default" when nothing was configured. ResolveProviderAdmissionConfig appends
// a clamp to Source whenever a bound lowers the cap, so the bound that won is
// always the last token — `env:OCI_…+pool:coder_sandbox_max_agents_per_pool`
// means the pool bound won and the env lever is *not* the one to raise.
func (c ProviderAdmissionConfig) LimitingSource() string {
	return providerAdmissionLimitingSource(c.Source)
}

// providerAdmissionLimitingSource extracts the winning bound from a resolved
// policy source (see ProviderAdmissionConfig.LimitingSource). It returns "" for
// an empty source so callers can fall back to the deployment lever.
func providerAdmissionLimitingSource(source string) string {
	if index := strings.LastIndex(source, "+"); index >= 0 {
		source = source[index+1:]
	}
	return strings.TrimSpace(source)
}

// ProviderSaturationError reports that no admission slot became available within
// the queue timeout. It is a distinct type so callers can tell back-pressure
// from a provider failure.
type ProviderSaturationError struct {
	Key            string
	MaxConcurrency int
	InFlight       int
	QueueWaiting   int
	Waited         time.Duration
	// Source is the resolved policy source the limiter was running with (eg
	// "env:OCI_…+pool:coder_sandbox_max_agents_per_pool"). It is what lets a
	// saturation message name the bound that actually decided the cap instead of
	// always pointing at the env lever. Empty for a hand-built policy.
	Source string
}

func (e *ProviderSaturationError) Error() string {
	return fmt.Sprintf(
		"provider %s is saturated: %d in-flight request(s) at max_concurrency=%d, %d queued, no slot within %s — %s",
		e.Key, e.InFlight, e.MaxConcurrency, e.QueueWaiting, e.Waited.Round(time.Millisecond),
		e.Advice(),
	)
}

// LimitingSource returns the single bound that decided the effective cap
// (`env:KEY`, `agent:KEY`, `pool:KEY` or "default"), or "" when the error was
// built without a resolved policy.
func (e *ProviderSaturationError) LimitingSource() string {
	return providerAdmissionLimitingSource(e.Source)
}

// Advice is the "turn this lever" half of the message. It names the bound that
// actually limited the cap: a ci#602 pool clamp or a per-agent key means the
// deployment env lever is *not* what an operator should raise, because raising
// it would change nothing.
func (e *ProviderSaturationError) Advice() string {
	kind, key, hasKey := strings.Cut(e.LimitingSource(), ":")
	if !hasKey || strings.TrimSpace(key) == "" {
		// No resolved source (or the plain "default"): name the deployment lever.
		return fmt.Sprintf("raise %s or reduce concurrent agents", ProviderMaxConcurrencyEnvKey)
	}
	key = strings.TrimSpace(key)
	switch kind {
	case "pool":
		return fmt.Sprintf(
			"effective_max_concurrency=%d is clamped by the pooled sandbox bound %s (ci#602) — raising %s has no effect until %s is raised; raise %s or reduce concurrent agents",
			e.MaxConcurrency, key, ProviderMaxConcurrencyEnvKey, key, key)
	case "agent":
		return fmt.Sprintf(
			"effective_max_concurrency=%d comes from the agent config key %s (the deployment lever %s is unset); raise %s or reduce concurrent agents",
			e.MaxConcurrency, key, ProviderMaxConcurrencyEnvKey, key)
	default: // "env:<key>": the deployment lever is the bound to turn.
		return fmt.Sprintf("raise %s (effective_max_concurrency=%d) or reduce concurrent agents", key, e.MaxConcurrency)
	}
}

// providerAdmissionConfigInt reads a positive integer from a shared config,
// accepting the JSON number forms and the string form a hand-written config may
// carry.
func providerAdmissionConfigInt(config map[string]interface{}, key string) int {
	if config == nil {
		return 0
	}
	switch value := config[key].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float32:
		return int(value)
	case float64:
		return int(value)
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			return parsed
		}
	}
	return 0
}

// providerAdmissionEnvInt reads a positive integer from the process environment.
func providerAdmissionEnvInt(key string) (int, bool) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 {
		return 0, false
	}
	return parsed, true
}

// ResolveProviderAdmissionConfig resolves the effective admission policy for a
// chat's shared config.
//
// Precedence (and the ci#620 fix): deployment env → per-agent config key →
// pooled sandbox bound → default. The env deliberately wins, because the
// shared-config default merge writes the literal default into every native
// coder agent's config, which used to shadow the deployment lever forever.
func ResolveProviderAdmissionConfig(config map[string]interface{}) ProviderAdmissionConfig {
	resolved := ProviderAdmissionConfig{
		MaxConcurrency: DefaultProviderMaxConcurrency,
		QueueTimeout:   DefaultProviderQueueTimeout,
		Source:         "default",
	}

	if envMax, ok := providerAdmissionEnvInt(ProviderMaxConcurrencyEnvKey); ok {
		resolved.MaxConcurrency = envMax
		resolved.Source = "env:" + ProviderMaxConcurrencyEnvKey
	} else if agentMax := providerAdmissionConfigInt(config, providerMaxConcurrencyConfigKey); agentMax > 0 {
		resolved.MaxConcurrency = agentMax
		resolved.Source = "agent:" + providerMaxConcurrencyConfigKey
	}

	// ci#602 alignment: never allow more provider requests in flight than agents
	// packed onto one pool pod.
	if poolBound := providerAdmissionConfigInt(config, providerPoolBoundConfigKey); poolBound > 0 && poolBound < resolved.MaxConcurrency {
		resolved.MaxConcurrency = poolBound
		resolved.Source += "+pool:" + providerPoolBoundConfigKey
	}

	if ms := providerAdmissionConfigInt(config, providerQueueTimeoutConfigKey); ms > 0 {
		resolved.QueueTimeout = time.Duration(ms) * time.Millisecond
	} else if ms, ok := providerAdmissionEnvInt(ProviderQueueTimeoutEnvKey); ok && ms > 0 {
		resolved.QueueTimeout = time.Duration(ms) * time.Millisecond
	}

	// ci#799: the stale-slot watchdog thresholds are deployment levers too. The
	// stale threshold defaults to the provider client timeout (300s): a slot
	// older than that whose request context is already done cannot belong to a
	// live generation any more.
	if ms, ok := providerAdmissionEnvInt(ProviderAdmissionStaleTimeoutEnvKey); ok && ms > 0 {
		resolved.StaleTimeout = time.Duration(ms) * time.Millisecond
	}
	if ms, ok := providerAdmissionEnvInt(ProviderAdmissionWatchdogEnvKey); ok && ms > 0 {
		resolved.WatchdogInterval = time.Duration(ms) * time.Millisecond
	}
	return resolved
}

// ProviderAdmissionSlotSnapshot is the secret-free state of one granted
// admission slot (ci#799). The limiter used to expose only the aggregate
// in_flight count, so a slot leaked by a cancelled/abandoned request was
// indistinguishable from a genuinely long request: the key stayed pinned at its
// cap with no live generation behind it and only a backend restart cleared it.
// Every slot therefore carries its acquisition time, the request/chat identity
// it belongs to and whether its context is already done, so a stale slot is
// visible (and attributable) in the admin snapshot instead of only in the log.
type ProviderAdmissionSlotSnapshot struct {
	// SlotID is a monotonic per-key slot identifier (1-based).
	SlotID uint64 `json:"slot_id"`
	// RequestID identifies the provider request/attempt that owns the slot.
	RequestID string `json:"request_id,omitempty"`
	// ChatID is the chat the request belongs to, when known.
	ChatID string `json:"chat_id,omitempty"`
	// Attempt is the 1-based attempt number of the request within its round.
	Attempt int `json:"attempt,omitempty"`
	// AcquiredAt is the RFC3339 acquisition time of the slot.
	AcquiredAt string `json:"acquired_at"`
	// Age is how long the slot has been held.
	Age time.Duration `json:"age"`
	// ContextDone reports whether the owning request context is already
	// cancelled/timed out. A slot with context_done=true and an age past the
	// stale timeout is a leak the watchdog reclaims.
	ContextDone bool `json:"context_done"`
	// Stale reports whether the slot exceeded the stale timeout with no live
	// generation behind it.
	Stale bool `json:"stale"`
}

// providerAdmissionSlot is one granted admission slot tracked by a limiter.
type providerAdmissionSlot struct {
	id         uint64
	acquiredAt time.Time
	requestID  string
	chatID     string
	attempt    int
	// contextDone is set by the context watcher once ctx.Done() fires, so the
	// watchdog can tell a leaked slot from a live long request without touching
	// the context again.
	contextDone bool
	// released is closed exactly once when the slot is given back. The context
	// watcher goroutine selects on it, so no goroutine outlives its slot.
	released chan struct{}
	// releaseCause records why the slot was released, for the operator line.
	releaseCause string
}

// ProviderAdmissionSnapshot is the secret-free state of one admission key: the
// effective cap, the current in-flight count and the queue depth. It is what an
// operator line and the development loop read to see the real admission state.
type ProviderAdmissionSnapshot struct {
	Key            string        `json:"key"`
	MaxConcurrency int           `json:"max_concurrency"`
	InFlight       int           `json:"in_flight"`
	QueueWaiting   int           `json:"queue_waiting"`
	Source         string        `json:"config_source"`
	QueueTimeout   time.Duration `json:"queue_timeout"`
	AdmittedTotal  int64         `json:"admitted_total"`
	WaitedTotal    int64         `json:"waited_total"`
	QueueWaitMax   time.Duration `json:"queue_wait_max"`
	SaturatedTotal int64         `json:"saturated_total"`
	Saturated      bool          `json:"saturated"`
	// LimitingSource is the single bound that decided MaxConcurrency
	// (`env:KEY`, `agent:KEY`, `pool:KEY` or "default"), ie the token of Source
	// that actually won. Config_source keeps the full provenance
	// (`env:…+pool:…`); this is the machine-readable answer to "which lever
	// does an operator raise?" (ci#620 follow-up).
	LimitingSource string `json:"limiting_source"`
	// Slots is the per-slot detail of the in-flight requests (ci#799): the
	// acquisition time/age and the request/chat identity of every granted slot.
	// It lets an operator see *which* holder pins the cap instead of only the
	// aggregate count, and it is what makes a stalled lead diagnosable without
	// a restart.
	Slots []ProviderAdmissionSlotSnapshot `json:"slots,omitempty"`
	// InFlightMaxAge is the age of the oldest in-flight slot; zero when nothing
	// is in flight. A value past the provider client timeout with no live
	// generation is the exact ci#799 symptom.
	InFlightMaxAge time.Duration `json:"in_flight_max_age"`
	// StaleSlots is how many in-flight slots exceed the stale timeout while
	// their request context is already done, ie the leaks the watchdog reclaims.
	StaleSlots int `json:"stale_slots"`
	// StaleTimeout is the effective stale-slot threshold the watchdog applies.
	StaleTimeout time.Duration `json:"stale_timeout"`
	// ReclaimedStaleTotal counts the slots the watchdog has reclaimed since
	// process start (ci#799): a non-zero value means the counter did leak and
	// was self-healed rather than needing a restart.
	ReclaimedStaleTotal int64 `json:"reclaimed_stale_total"`
}

// providerAdmissionLimiter is a per-key in-flight limiter that can be resized at
// runtime. Unlike a channel-backed semaphore it grows immediately (growing can
// never cut an in-flight request off) and applies a shrink lazily as slots free
// up, which is what "the configured concurrency takes effect without a
// redeploy" requires.
type providerAdmissionLimiter struct {
	key string

	mu         sync.Mutex
	max        int
	inFlight   int
	waiting    int
	source     string
	queueLimit time.Duration
	// changed is closed and replaced whenever a slot frees or the cap changes,
	// waking every waiter to re-check the condition under the lock.
	changed chan struct{}

	// ci#799 per-slot tracking: every granted slot is recorded so a leak is
	// attributable and reclaimable. nextSlotID is monotonic; slots is keyed by
	// it. maxStale is the stale threshold; watchdogEvery/lastSweep drive the
	// background sweeper.
	nextSlotID      uint64
	slots           map[uint64]*providerAdmissionSlot
	maxStale        time.Duration
	watchdogEvery   time.Duration
	lastSweep       time.Time
	reclaimedTotal  int64
	watchdogStarted bool

	admittedTotal  int64
	waitedTotal    int64
	queueWaitMax   time.Duration
	saturatedTotal int64
}

// providerAdmissionSlotMeta describes the request a slot is granted to. It is
// threaded from the provider request so the slot is attributable (request id,
// chat id, attempt) in the admin snapshot (ci#799).
type providerAdmissionSlotMeta struct {
	RequestID string
	ChatID    string
	Attempt   int
}

var (
	providerAdmissionMu       sync.Mutex
	providerAdmissionLimiters = map[string]*providerAdmissionLimiter{}
	providerAdmissionLogged   = map[string]string{}
)

// ProviderAdmissionKeyFor is the per-key admission identity of a provider
// target. It contains the backend and endpoint plus a non-reversible length hint
// of the key so two different keys against the same provider are controlled
// independently — and no key material is ever logged or exposed.
func ProviderAdmissionKeyFor(backend, endpoint, apiKey string) string {
	backend = strings.ToLower(strings.TrimSpace(backend))
	endpoint = strings.ToLower(strings.TrimRight(strings.TrimSpace(endpoint), "/"))
	identity := "nokey"
	if trimmed := strings.TrimSpace(apiKey); trimmed != "" {
		identity = fmt.Sprintf("key-len%d", len(trimmed))
	}
	return backend + "|" + endpoint + "|" + identity
}

// providerAdmissionLimiterFor returns the limiter for an admission key,
// creating it on first use and applying the resolved policy to it. An already
// existing limiter is resized in place (preserving its counters and its
// in-flight requests), so a config change takes effect without a restart.
func providerAdmissionLimiterFor(key string, policy ProviderAdmissionConfig) *providerAdmissionLimiter {
	providerAdmissionMu.Lock()
	defer providerAdmissionMu.Unlock()
	limiter, ok := providerAdmissionLimiters[key]
	if !ok {
		limiter = &providerAdmissionLimiter{
			key:     key,
			changed: make(chan struct{}),
			slots:   map[uint64]*providerAdmissionSlot{},
		}
		providerAdmissionLimiters[key] = limiter
	}
	limiter.apply(policy)
	return limiter
}

// apply reconfigures the limiter in place. Growing is immediate; shrinking only
// lowers the cap (new admissions wait) — the requests already in flight are
// never cut off.
func (l *providerAdmissionLimiter) apply(policy ProviderAdmissionConfig) {
	l.mu.Lock()
	defer l.mu.Unlock()
	changed := l.max != policy.MaxConcurrency || l.source != policy.Source || l.queueLimit != policy.QueueTimeout
	l.max = policy.MaxConcurrency
	l.source = policy.Source
	l.queueLimit = policy.QueueTimeout
	// ci#799: the stale threshold follows the configured provider client
	// timeout, so a deployment with a longer provider timeout does not have its
	// long-but-live requests reclaimed.
	l.maxStale = policy.StaleTimeoutValue()
	l.watchdogEvery = policy.WatchdogIntervalValue()
	if changed {
		// Wake the waiters so a grown cap is picked up right away.
		close(l.changed)
		l.changed = make(chan struct{})
	}
}

// acquire waits for an admission slot. It returns a release function plus the
// time spent queueing. A non-positive cap admits immediately. On queue timeout
// it returns a *ProviderSaturationError instead of dropping the request.
//
// ci#799: a granted slot is recorded with its acquisition time and the request
// identity it belongs to, and the release is defensive — besides the caller's
// `defer release()` the slot is also given back as soon as ctx is done, so a
// cancelled turn, a timed-out request or a retry-exhausted task can never pin
// the counter. The background stale-slot watchdog is started here, on the first
// admission of a key.
func (l *providerAdmissionLimiter) acquire(ctx context.Context, meta providerAdmissionSlotMeta) (func(), time.Duration, error) {
	start := time.Now()

	l.mu.Lock()
	max := l.max
	timeout := l.queueLimit
	l.mu.Unlock()

	if max <= 0 {
		return func() {}, 0, nil
	}

	var timerC <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timerC = timer.C
	}

	for {
		l.mu.Lock()
		if l.max <= 0 || l.inFlight < l.max {
			l.inFlight++
			l.admittedTotal++
			slot := l.registerSlotLocked(meta)
			waited := time.Since(start)
			if waited > l.queueWaitMax {
				l.queueWaitMax = waited
			}
			l.waitedTotal += int64(waited)
			l.mu.Unlock()
			// Defensive release on ctx.Done(): the caller may never reach its
			// `defer` (a cancelled turn, an abandoned task, a stream reader that
			// never returns), which is exactly how the ci#799 counter leaked.
			go l.watchContext(ctx, slot)
			l.startWatchdogOnce()
			return l.releaseFunc(slot), waited, nil
		}
		waitCh := l.changed
		l.waiting++
		l.mu.Unlock()

		select {
		case <-waitCh:
			l.mu.Lock()
			l.waiting--
			l.mu.Unlock()
		case <-timerC:
			waited := time.Since(start)
			l.mu.Lock()
			l.waiting--
			l.saturatedTotal++
			inFlight := l.inFlight
			waiting := l.waiting
			cap := l.max
			source := l.source
			l.mu.Unlock()
			return nil, waited, &ProviderSaturationError{
				Key:            l.key,
				MaxConcurrency: cap,
				InFlight:       inFlight,
				QueueWaiting:   waiting,
				Waited:         waited,
				Source:         source,
			}
		case <-ctx.Done():
			l.mu.Lock()
			l.waiting--
			l.mu.Unlock()
			return nil, time.Since(start), ctx.Err()
		}
	}
}

// registerSlotLocked records a newly granted slot. Caller holds l.mu.
func (l *providerAdmissionLimiter) registerSlotLocked(meta providerAdmissionSlotMeta) *providerAdmissionSlot {
	l.nextSlotID++
	slot := &providerAdmissionSlot{
		id:         l.nextSlotID,
		acquiredAt: time.Now(),
		requestID:  strings.TrimSpace(meta.RequestID),
		chatID:     strings.TrimSpace(meta.ChatID),
		attempt:    meta.Attempt,
		released:   make(chan struct{}),
	}
	if l.slots == nil {
		l.slots = map[uint64]*providerAdmissionSlot{}
	}
	l.slots[slot.id] = slot
	return slot
}

// watchContext releases a slot as soon as its request context is done. The
// caller's `defer release()` is not sufficient on its own (ci#799): a cancelled
// turn, a timed-out request or a retry-exhausted task can abort before reaching
// it, and a stream reader that never returns can hold the slot forever. This
// watcher guarantees the slot is given back regardless — it stays idle on the
// released channel when the caller releases normally, so no goroutine outlives
// its slot.
func (l *providerAdmissionLimiter) watchContext(ctx context.Context, slot *providerAdmissionSlot) {
	select {
	case <-ctx.Done():
		// Mark first: if a sweep is racing us, the slot is precisely
		// attributable as a cancelled one.
		l.markContextDone(slot)
		l.releaseSlot(slot, releaseReasonContextDone)
	case <-slot.released:
	}
}

// releaseSlot gives one slot back exactly once, whatever the cause. It is the
// single place the counter is decremented, so the three release paths
// (explicit defer, ctx.Done watcher, stale watchdog) can never double-release.
func (l *providerAdmissionLimiter) releaseSlot(slot *providerAdmissionSlot, cause string) bool {
	l.mu.Lock()
	if slot == nil || slot.released == nil {
		l.mu.Unlock()
		return false
	}
	select {
	case <-slot.released:
		// Already released: nothing to do.
		l.mu.Unlock()
		return false
	default:
	}
	slot.releaseCause = cause
	close(slot.released)
	if l.inFlight > 0 {
		l.inFlight--
	}
	if l.slots != nil {
		delete(l.slots, slot.id)
	}
	// Wake every waiter: a freed slot may be usable by any of them.
	close(l.changed)
	l.changed = make(chan struct{})
	l.mu.Unlock()

	if cause != releaseReasonExplicit {
		// A non-explicit release means the caller did not reach its `defer`:
		// exactly the leak ci#799 had pinned the cap at. Make it visible so an
		// operator can see the self-heal instead of only a restarted counter.
		logging.Warnf("[provider_admission] key=%s reclaimed slot id=%d reason=%s request_id=%s chat_id=%s attempt=%d held=%s (ci#799)",
			l.key, slot.id, cause, slot.requestID, slot.chatID, slot.attempt,
			time.Since(slot.acquiredAt).Round(time.Millisecond))
	}
	return true
}

// releaseFunc returns the idempotent explicit release of one admission slot.
func (l *providerAdmissionLimiter) releaseFunc(slot *providerAdmissionSlot) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			l.releaseSlot(slot, releaseReasonExplicit)
		})
	}
}

// startWatchdogOnce launches the per-key stale-slot watchdog the first time a
// slot is granted. The watchdog is a last-resort safety net for slots that even
// the ctx.Done watcher cannot see — a request that never cancelled its context
// but whose generation is gone (the ci#799 leak). It is a no-op once the
// limiter has nothing in flight.
func (l *providerAdmissionLimiter) startWatchdogOnce() {
	l.mu.Lock()
	if l.watchdogStarted {
		l.mu.Unlock()
		return
	}
	l.watchdogStarted = true
	every := l.watchdogEvery
	if every <= 0 {
		every = DefaultProviderAdmissionWatchdogInterval
	}
	ticker := time.NewTicker(every)
	l.mu.Unlock()
	go func() {
		for range ticker.C {
			l.mu.Lock()
			// Stop once the limiter has been idle for a whole interval, so a
			// quiet key does not keep a goroutine alive forever.
			idle := l.inFlight == 0
			l.mu.Unlock()
			if idle {
				l.mu.Lock()
				l.watchdogStarted = false
				l.mu.Unlock()
				ticker.Stop()
				return
			}
			l.sweepStaleSlots(time.Now())
		}
	}()
}

// sweepStaleSlots reclaims every slot that exceeded the stale threshold while
// its request context is already done. A live long request is never cut off: a
// slot is only reclaimed when its context has fired (contextDone) or when it
// outlived the threshold *and* no live generation claims it. It returns the
// number of slots reclaimed so a caller/test can assert on it.
func (l *providerAdmissionLimiter) sweepStaleSlots(now time.Time) int {
	type reclaim struct {
		slot    *providerAdmissionSlot
		heldFor time.Duration
	}
	var doomed []reclaim

	l.mu.Lock()
	threshold := l.maxStale
	if threshold <= 0 {
		threshold = ProviderRequestClientTimeout
	}
	l.lastSweep = now
	for _, slot := range l.slots {
		age := now.Sub(slot.acquiredAt)
		if age < threshold {
			continue
		}
		// Beyond the provider client timeout with no live generation: the
		// context has already fired, so the owning request cannot still be
		// running. A slot whose context is *not* done and whose age is exactly
		// at the threshold is left alone for one more sweep so a genuinely
		// long-but-live request is not reclaimed.
		if !slot.contextDone && age < 2*threshold {
			continue
		}
		doomed = append(doomed, reclaim{slot: slot, heldFor: age})
	}
	l.reclaimedTotal += int64(len(doomed))
	l.mu.Unlock()

	for _, r := range doomed {
		if l.releaseSlot(r.slot, releaseReasonWatchdogStale) {
			logging.Errorf("[provider_admission] key=%s stale slot id=%d request_id=%s chat_id=%s attempt=%d held=%s exceeded stale_timeout=%s with no live generation; released by watchdog (ci#799)",
				l.key, r.slot.id, r.slot.requestID, r.slot.chatID, r.slot.attempt,
				r.heldFor.Round(time.Millisecond), threshold.Round(time.Millisecond))
		}
	}
	return len(doomed)
}

// markContextDone records that a slot's request context has fired, so the
// watchdog can reclaim it without waiting for a second threshold. It is called
// by the ctx.Done watcher before it releases, so a cancelled slot is precisely
// attributable even if the release races with a sweep.
func (l *providerAdmissionLimiter) markContextDone(slot *providerAdmissionSlot) {
	l.mu.Lock()
	if slot != nil {
		slot.contextDone = true
	}
	l.mu.Unlock()
}

// snapshot reports the current state of the limiter, including the per-slot
// detail (ci#799) so a stalled holder is visible instead of only counted.
func (l *providerAdmissionLimiter) snapshot() ProviderAdmissionSnapshot {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	threshold := l.maxStale
	if threshold <= 0 {
		threshold = ProviderRequestClientTimeout
	}
	snapshot := ProviderAdmissionSnapshot{
		Key:                 l.key,
		MaxConcurrency:      l.max,
		InFlight:            l.inFlight,
		QueueWaiting:        l.waiting,
		Source:              l.source,
		QueueTimeout:        l.queueLimit,
		AdmittedTotal:       l.admittedTotal,
		WaitedTotal:         l.waitedTotal,
		QueueWaitMax:        l.queueWaitMax,
		SaturatedTotal:      l.saturatedTotal,
		Saturated:           l.max > 0 && l.inFlight >= l.max,
		LimitingSource:      providerAdmissionLimitingSource(l.source),
		StaleTimeout:        threshold,
		ReclaimedStaleTotal: l.reclaimedTotal,
	}
	ids := make([]uint64, 0, len(l.slots))
	for id := range l.slots {
		ids = append(ids, id)
	}
	// Ascending slot id, so the oldest holder is first without a sort import.
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
	for _, id := range ids {
		slot := l.slots[id]
		age := now.Sub(slot.acquiredAt)
		if age > snapshot.InFlightMaxAge {
			snapshot.InFlightMaxAge = age
		}
		stale := false
		if age >= threshold && (slot.contextDone || age >= 2*threshold) {
			stale = true
			snapshot.StaleSlots++
		}
		snapshot.Slots = append(snapshot.Slots, ProviderAdmissionSlotSnapshot{
			SlotID:      slot.id,
			RequestID:   slot.requestID,
			ChatID:      slot.chatID,
			Attempt:     slot.attempt,
			AcquiredAt:  slot.acquiredAt.UTC().Format(time.RFC3339),
			Age:         age,
			ContextDone: slot.contextDone,
			Stale:       stale,
		})
	}
	return snapshot
}

// ProviderAdmissionStatus returns the current admission state of every known
// provider key, ordered for a stable operator view. It is secret-free (keys are
// only referenced by their admission identity).
func ProviderAdmissionStatus() []ProviderAdmissionSnapshot {
	providerAdmissionMu.Lock()
	keys := make([]string, 0, len(providerAdmissionLimiters))
	for key := range providerAdmissionLimiters {
		keys = append(keys, key)
	}
	limiters := make([]*providerAdmissionLimiter, 0, len(keys))
	for _, key := range keys {
		limiters = append(limiters, providerAdmissionLimiters[key])
	}
	providerAdmissionMu.Unlock()

	out := make([]ProviderAdmissionSnapshot, 0, len(limiters))
	for _, limiter := range limiters {
		out = append(out, limiter.snapshot())
	}
	// Stable ordering by key without pulling in sort for one comparison.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Key > out[j].Key; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// logProviderAdmissionState writes one line per (key, effective policy) change
// at debug level, plus one line whenever the key is saturated at warn level, so
// the effective value and the current in-flight count are visible in the backend
// log. Neither line is a failure: the change line is routine bookkeeping and
// stays quiet even at the default info level (the ci#645 "no log heaviness"
// rule), while a genuinely saturated key is worth a warning. It never logs a
// key.
func logProviderAdmissionState(snapshot ProviderAdmissionSnapshot) {
	providerAdmissionMu.Lock()
	signature := fmt.Sprintf("%d|%s", snapshot.MaxConcurrency, snapshot.Source)
	previous, seen := providerAdmissionLogged[snapshot.Key]
	changed := !seen || previous != signature
	providerAdmissionLogged[snapshot.Key] = signature
	providerAdmissionMu.Unlock()

	if changed {
		logging.Debugf("[provider_admission] key=%s effective_max_concurrency=%d in_flight=%d queue_waiting=%d source=%s limiting_source=%s queue_timeout=%s stale_timeout=%s (ci#620,ci#799)",
			snapshot.Key, snapshot.MaxConcurrency, snapshot.InFlight, snapshot.QueueWaiting, snapshot.Source,
			snapshot.LimitingSource, snapshot.QueueTimeout.Round(time.Millisecond),
			snapshot.StaleTimeout.Round(time.Millisecond))
		return
	}
	if snapshot.Saturated {
		// ci#799: a saturated key is only diagnosable if the line says how old
		// the oldest in-flight slot is and who holds it. Include the oldest
		// holder (request/chat id, age) so a stalled lead is visible without a
		// restart, and flag it when it already looks stale.
		logging.Warnf("[provider_admission] key=%s saturated: in_flight=%d at effective_max_concurrency=%d limiting_source=%s queue_waiting=%d in_flight_max_age=%s stale_slots=%d reclaimed_stale_total=%d%s (ci#620,ci#799)",
			snapshot.Key, snapshot.InFlight, snapshot.MaxConcurrency, snapshot.LimitingSource, snapshot.QueueWaiting,
			snapshot.InFlightMaxAge.Round(time.Millisecond), snapshot.StaleSlots, snapshot.ReclaimedStaleTotal,
			providerAdmissionSlotHolderSuffix(snapshot))
	}
}

// providerAdmissionSlotHolderSuffix renders the oldest in-flight slot of a
// snapshot as a log suffix (` oldest_holder=chat/request attempt held=…`), so a
// saturated line names the exact holder that pins the key. It stays empty when
// nothing is in flight. It is secret-free: only the admission/chat identities
// already present on the provider request are shown (ci#799).
func providerAdmissionSlotHolderSuffix(snapshot ProviderAdmissionSnapshot) string {
	if len(snapshot.Slots) == 0 {
		return ""
	}
	oldest := snapshot.Slots[0]
	for _, slot := range snapshot.Slots[1:] {
		if slot.Age > oldest.Age {
			oldest = slot
		}
	}
	return fmt.Sprintf(" oldest_holder chat_id=%s request_id=%s attempt=%d held=%s context_done=%t stale=%t",
		oldest.ChatID, oldest.RequestID, oldest.Attempt,
		oldest.Age.Round(time.Millisecond), oldest.ContextDone, oldest.Stale)
}

// providerAdmissionRequestID is the stable, secret-free identity of one
// provider request attempt. It is used to attribute an admission slot to the
// request that holds it (ci#799) without logging a key or a token.
func providerAdmissionRequestID(chatUUID string, attempt int) string {
	chatUUID = strings.TrimSpace(chatUUID)
	if chatUUID == "" {
		chatUUID = "unknown-chat"
	}
	if attempt <= 0 {
		attempt = 1
	}
	return fmt.Sprintf("provider-request:%s#%d", chatUUID, attempt)
}

// admitProviderRequest acquires an admission slot for one provider request of a
// chat's plan. It returns the release function (never nil when err is nil) and
// logs the effective cap/in-flight state so the admission decision is
// observable. A nil policy disables admission.
//
// ci#799: the slot is acquired with its request/chat identity, so a stalled
// holder is attributable in the admin snapshot, and the returned release is
// defensive — release also happens on ctx.Done()/timeout/task cancellation even
// if the caller never reaches its `defer`.
func admitProviderRequest(ctx context.Context, key string, policy ProviderAdmissionConfig, meta providerAdmissionSlotMeta) (func(), error) {
	if policy.MaxConcurrency <= 0 {
		return func() {}, nil
	}
	limiter := providerAdmissionLimiterFor(key, policy)
	logProviderAdmissionState(limiter.snapshot())
	release, _, err := limiter.acquire(ctx, meta)
	if err != nil {
		logProviderAdmissionState(limiter.snapshot())
		return nil, err
	}
	logProviderAdmissionState(limiter.snapshot())
	return release, nil
}

// resetProviderAdmissionForTest clears the limiter registry and the log-dedup
// state so a test starts from a clean admission view.
func resetProviderAdmissionForTest() {
	providerAdmissionMu.Lock()
	defer providerAdmissionMu.Unlock()
	providerAdmissionLimiters = map[string]*providerAdmissionLimiter{}
	providerAdmissionLogged = map[string]string{}
}
