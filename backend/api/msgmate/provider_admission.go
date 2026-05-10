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
}

// String renders the policy as one operator-readable line.
func (c ProviderAdmissionConfig) String() string {
	return fmt.Sprintf("max_concurrency=%d source=%s queue_timeout=%s",
		c.MaxConcurrency, c.Source, c.QueueTimeout.Round(time.Millisecond))
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
	return resolved
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

	admittedTotal  int64
	waitedTotal    int64
	queueWaitMax   time.Duration
	saturatedTotal int64
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
		limiter = &providerAdmissionLimiter{key: key, changed: make(chan struct{})}
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
	if changed {
		// Wake the waiters so a grown cap is picked up right away.
		close(l.changed)
		l.changed = make(chan struct{})
	}
}

// acquire waits for an admission slot. It returns a release function plus the
// time spent queueing. A non-positive cap admits immediately. On queue timeout
// it returns a *ProviderSaturationError instead of dropping the request.
func (l *providerAdmissionLimiter) acquire(ctx context.Context) (func(), time.Duration, error) {
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
			waited := time.Since(start)
			if waited > l.queueWaitMax {
				l.queueWaitMax = waited
			}
			l.waitedTotal += int64(waited)
			l.mu.Unlock()
			return l.releaseFunc(), waited, nil
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

// releaseFunc returns the idempotent release of one admission slot.
func (l *providerAdmissionLimiter) releaseFunc() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			if l.inFlight > 0 {
				l.inFlight--
			}
			// Wake every waiter: a freed slot may be usable by any of them.
			close(l.changed)
			l.changed = make(chan struct{})
			l.mu.Unlock()
		})
	}
}

// snapshot reports the current state of the limiter.
func (l *providerAdmissionLimiter) snapshot() ProviderAdmissionSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	return ProviderAdmissionSnapshot{
		Key:            l.key,
		MaxConcurrency: l.max,
		InFlight:       l.inFlight,
		QueueWaiting:   l.waiting,
		Source:         l.source,
		QueueTimeout:   l.queueLimit,
		AdmittedTotal:  l.admittedTotal,
		WaitedTotal:    l.waitedTotal,
		QueueWaitMax:   l.queueWaitMax,
		SaturatedTotal: l.saturatedTotal,
		Saturated:      l.max > 0 && l.inFlight >= l.max,
		LimitingSource: providerAdmissionLimitingSource(l.source),
	}
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
		logging.Debugf("[provider_admission] key=%s effective_max_concurrency=%d in_flight=%d queue_waiting=%d source=%s limiting_source=%s queue_timeout=%s (ci#620)",
			snapshot.Key, snapshot.MaxConcurrency, snapshot.InFlight, snapshot.QueueWaiting, snapshot.Source,
			snapshot.LimitingSource, snapshot.QueueTimeout.Round(time.Millisecond))
		return
	}
	if snapshot.Saturated {
		logging.Warnf("[provider_admission] key=%s saturated: in_flight=%d at effective_max_concurrency=%d limiting_source=%s queue_waiting=%d (ci#620)",
			snapshot.Key, snapshot.InFlight, snapshot.MaxConcurrency, snapshot.LimitingSource, snapshot.QueueWaiting)
	}
}

// admitProviderRequest acquires an admission slot for one provider request of a
// chat's plan. It returns the release function (never nil when err is nil) and
// logs the effective cap/in-flight state so the admission decision is
// observable. A nil policy disables admission.
func admitProviderRequest(ctx context.Context, key string, policy ProviderAdmissionConfig) (func(), error) {
	if policy.MaxConcurrency <= 0 {
		return func() {}, nil
	}
	limiter := providerAdmissionLimiterFor(key, policy)
	logProviderAdmissionState(limiter.snapshot())
	release, _, err := limiter.acquire(ctx)
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
