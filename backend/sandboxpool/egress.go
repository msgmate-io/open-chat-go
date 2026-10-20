// Package sandboxpool implements the pool-level DNS/egress readiness gate of
// ci#613, the follow-up of the ci#546 shared-sandbox networking design.
//
// A *pooled* backing sandbox is provisioned once and then accepts leases from
// many chats. Nothing else verifies that the pool pod's DNS and egress behave
// like a dedicated pod: a pool with broken egress fails every agent's first
// network call, and that failure surfaces as a mysterious per-chat (per-agent)
// timeout instead of "this pool is unhealthy".
//
// A pool therefore has to pass this gate before it accepts leases:
//
//   - resolve cluster DNS and dial the GitHub/API endpoints the credential flow
//     (ci#497) and `gh` need, from *inside* the pool pod;
//   - mark the pool unhealthy when the probe fails, request its recycling and
//     refuse to hand out leases, mirroring the existing sandbox readiness
//     handling of a dedicated pod;
//   - expose the probe result so the pool's failure is diagnosable.
//
// The probe itself runs inside the pool pod (the pod is the only place whose DNS
// and egress behaviour matters), so the authoritative input is the JSON report
// emitted by development/scripts/sandbox-pool-egress-gate.sh, which the pool
// start path (or an operator/CI step) feeds back via Admit / the admin
// ingestion endpoint. When the pool is reachable directly (and explicitly
// configured with EnvProbeLocally), the gate can also probe in-process.
//
// Fail-closed rules: a fresh failing report always blocks leases and keeps
// blocking while the report stays failing, even after it goes stale; a stale but
// last-known-good report keeps handing out leases (the staleness is reported)
// unless the gate is armed in strict mode, where a missing report blocks too.
//
// Nothing in this package logs or returns credential material: only target host
// names, per-target errors and their remediation.
package sandboxpool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Probe kinds. A dns probe resolves a name, a tcp probe dials host:port.
const (
	KindDNS = "dns"
	KindTCP = "tcp"
)

// Deployment levers. All of them are optional: with nothing configured the gate
// follows DefaultEgressTargets, relies on admitted probe reports and is
// advisory until a report exists.
const (
	// EnvTargets overrides the probed endpoints, as a comma-separated list of
	// "dns:<name>" / "tcp:<host>:<port>" entries.
	EnvTargets = "OCI_MSGMATE_CODER_SANDBOX_POOL_EGRESS_TARGETS"
	// EnvDisabled turns the gate off entirely ("1"/"true").
	EnvDisabled = "OCI_MSGMATE_CODER_SANDBOX_POOL_EGRESS_GATE_DISABLED"
	// EnvStrict makes a *missing* probe report block leases as well (fail
	// closed) instead of leaving the pool ungated.
	EnvStrict = "OCI_MSGMATE_CODER_SANDBOX_POOL_EGRESS_GATE_STRICT"
	// EnvProbeLocally lets the gate probe from the backend process itself, for
	// deployments where the backend and the pool share the pod network.
	EnvProbeLocally = "OCI_MSGMATE_CODER_SANDBOX_POOL_EGRESS_PROBE_LOCALLY"
	// EnvProbeTimeoutMS bounds a single probe attempt.
	EnvProbeTimeoutMS = "OCI_MSGMATE_CODER_SANDBOX_POOL_EGRESS_PROBE_TIMEOUT_MS"
	// EnvReportTTLMS is how long an admitted report is considered fresh.
	EnvReportTTLMS = "OCI_MSGMATE_CODER_SANDBOX_POOL_EGRESS_REPORT_TTL_MS"
	// EnvRecycleAfterFails is how many consecutive failing reports request the
	// recycling of the pool.
	EnvRecycleAfterFails = "OCI_MSGMATE_CODER_SANDBOX_POOL_EGRESS_RECYCLE_AFTER_FAILURES"
)

// Defaults for the gate policy.
const (
	// DefaultProbeTimeout keeps the whole gate cheap: four probes with a 5s
	// budget at pool start and on demand.
	DefaultProbeTimeout = 5 * time.Second
	// DefaultReportTTL is how long a probe report is trusted before the gate
	// wants a fresh one.
	DefaultReportTTL = 10 * time.Minute
	// DefaultRecycleAfterFailures is the failure streak after which the pool is
	// reported as needing a recycle.
	DefaultRecycleAfterFailures = 3
)

// DefaultEgressTargets are the endpoints a pooled sandbox must reach to behave
// like a dedicated pod: cluster DNS (which the kubelet/CoreDNS of the pool
// namespace answers) plus the GitHub and GitHub API endpoints the credential
// flow of ci#497 and `gh` need (github.com for git, api.github.com for the API,
// uploads.github.com for release/asset uploads).
var DefaultEgressTargets = []Target{
	{Name: "cluster-dns", Kind: KindDNS, Host: "kubernetes.default.svc.cluster.local"},
	{Name: "github", Kind: KindTCP, Host: "github.com", Port: 443},
	{Name: "github-api", Kind: KindTCP, Host: "api.github.com", Port: 443},
	{Name: "github-uploads", Kind: KindTCP, Host: "uploads.github.com", Port: 443},
}

// Sentinel errors callers can match with errors.Is.
var (
	// ErrPoolEgressUnhealthy means the pool failed the gate (or, in strict
	// mode, never proved that it passes it): no lease may be handed out.
	ErrPoolEgressUnhealthy = errors.New("sandbox pool egress gate: pool is not egress-ready")
	// ErrPoolEgressInvalidReport means a probe report could not be used.
	ErrPoolEgressInvalidReport = errors.New("sandbox pool egress gate: invalid probe report")
)

// errNoAddresses is reported when a resolver answers successfully but empty.
var errNoAddresses = errors.New("resolver returned no addresses")

// IsPoolEgressFailure reports whether err came from this gate.
func IsPoolEgressFailure(err error) bool {
	return errors.Is(err, ErrPoolEgressUnhealthy) || errors.Is(err, ErrPoolEgressInvalidReport)
}

// Target is one probed endpoint.
type Target struct {
	// Name identifies the target in the report ("cluster-dns", "github", ...).
	Name string `json:"name"`
	// Kind is KindDNS or KindTCP.
	Kind string `json:"kind"`
	// Host is the resolved name or the dialed host.
	Host string `json:"host"`
	// Port is the dialed port (tcp targets only).
	Port int `json:"port,omitempty"`
}

// Addr renders the dial target.
func (t Target) Addr() string {
	if t.Port > 0 {
		return net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	}
	return t.Host
}

// Describe renders the target for reports and error messages.
func (t Target) Describe() string {
	if t.Kind == KindDNS {
		return "dns:" + t.Host
	}
	return "tcp:" + t.Addr()
}

// ProbeResult is the outcome of one target probe. It never carries credential
// material: only the target, the resolved addresses and the error text.
type ProbeResult struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Target      string   `json:"target"`
	OK          bool     `json:"ok"`
	DurationMS  int64    `json:"duration_ms"`
	Resolved    []string `json:"resolved,omitempty"`
	Error       string   `json:"error,omitempty"`
	Remediation string   `json:"remediation,omitempty"`
}

// Report is one pool-level egress probe run, produced inside the pool pod.
type Report struct {
	// Pool names the pooled backing sandbox the report belongs to.
	Pool string `json:"pool,omitempty"`
	// Source names where the probe ran ("pool-pod" for the in-pod script).
	Source string `json:"source,omitempty"`
	// CheckedAt is when the probe finished.
	CheckedAt time.Time `json:"checked_at"`
	// DurationMS is the wall time of the whole probe.
	DurationMS int64         `json:"duration_ms"`
	Targets    []ProbeResult `json:"targets"`
}

// Healthy reports whether every probe passed. An empty report is never healthy:
// a pool cannot prove its egress with no probes.
func (r Report) Healthy() bool { return len(r.Failures()) == 0 && len(r.Targets) > 0 }

// Failures returns the failing probes, in report order.
func (r Report) Failures() []ProbeResult {
	failures := make([]ProbeResult, 0, len(r.Targets))
	for _, target := range r.Targets {
		if !target.OK {
			failures = append(failures, target)
		}
	}
	return failures
}

// Summary is the one-line verdict used in logs and tool errors.
func (r Report) Summary() string {
	failures := r.Failures()
	if len(failures) == 0 {
		return fmt.Sprintf("all %d egress probes passed", len(r.Targets))
	}
	parts := make([]string, 0, len(failures))
	for _, failure := range failures {
		detail := failure.Error
		if detail == "" {
			detail = "failed"
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", failure.Target, detail))
	}
	return fmt.Sprintf("%d of %d egress probes failed: %s", len(failures), len(r.Targets), strings.Join(parts, "; "))
}

// Remediation returns the deduplicated, sorted remediation hints of a report.
func (r Report) Remediation() []string {
	seen := map[string]bool{}
	hints := []string{}
	for _, failure := range r.Failures() {
		if failure.Remediation == "" || seen[failure.Remediation] {
			continue
		}
		seen[failure.Remediation] = true
		hints = append(hints, failure.Remediation)
	}
	sort.Strings(hints)
	return hints
}

// ParseReport decodes the JSON report the in-pod probe script prints.
func ParseReport(data []byte) (Report, error) {
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		return Report{}, fmt.Errorf("%w: %v", ErrPoolEgressInvalidReport, err)
	}
	if len(report.Targets) == 0 {
		return Report{}, fmt.Errorf("%w: report contains no probes", ErrPoolEgressInvalidReport)
	}
	return report, nil
}

// GateStatus is the operator view of the pool gate (admin endpoint / logs).
type GateStatus struct {
	GeneratedAt string `json:"generated_at"`
	Disabled    bool   `json:"disabled"`
	Strict      bool   `json:"strict"`
	LocalProbe  bool   `json:"local_probe"`
	// Armed reports whether a probe report was admitted at least once: before
	// that the gate cannot say anything about the pool's egress.
	Armed   bool `json:"armed"`
	Healthy bool `json:"healthy"`
	// State is healthy, unhealthy or unknown.
	State             string   `json:"state"`
	HealthySince      string   `json:"healthy_since,omitempty"`
	ReportAgeMS       int64    `json:"report_age_ms"`
	ReportStale       bool     `json:"report_stale"`
	Report            *Report  `json:"report,omitempty"`
	ReportRemediation []string `json:"report_remediation,omitempty"`
	ConsecutiveFailed int      `json:"consecutive_failures"`
	RecycleRequested  bool     `json:"recycle_requested"`
	AdmittedLeases    int64    `json:"admitted_leases"`
	BlockedLeases     int64    `json:"blocked_leases"`
	LastError         string   `json:"last_error,omitempty"`
	// PortAudit is the per-workspace port-block audit of the pooled sandboxes
	// (ci#616). It is present only once a port auditor is attached, is best
	// effort and never changes the gate's state: drift is a diagnosable
	// condition, not a reason to refuse a lease.
	PortAudit      *PortAuditStatus `json:"port_audit,omitempty"`
	Targets        []string         `json:"targets"`
	ProbeTimeoutMS int64            `json:"probe_timeout_ms"`
	ReportTTLMS    int64            `json:"report_ttl_ms"`
}

// Gate is the process-wide (per-pool) readiness gate. All methods are safe for
// concurrent use.
type Gate struct {
	mu sync.Mutex

	targets      []Target
	timeout      time.Duration
	ttl          time.Duration
	recycleAfter int
	disabled     bool
	strict       bool
	localProbe   bool

	hasReport           bool
	report              Report
	healthy             bool
	healthySince        time.Time
	consecutiveFailures int
	recycleRequested    bool
	admittedLeases      int64
	blockedLeases       int64
	lastError           string

	now    func() time.Time
	lookup func(ctx context.Context, host string) ([]string, error)
	dial   func(ctx context.Context, network, addr string) (net.Conn, error)

	// portAuditor, when set, contributes the ci#616 per-workspace port-block
	// audit to the gate status. It is advisory only and never gates a lease.
	portAuditor *PortAuditor
}

var (
	defaultGateOnce sync.Once
	defaultGate     *Gate
)

// NewGate builds a gate for the given targets, configured from the environment.
func NewGate(targets []Target) *Gate {
	if len(targets) == 0 {
		targets = DefaultEgressTargets
	}
	gate := &Gate{
		targets:      append([]Target(nil), targets...),
		timeout:      DefaultProbeTimeout,
		ttl:          DefaultReportTTL,
		recycleAfter: DefaultRecycleAfterFailures,
		now:          time.Now,
		lookup:       defaultLookup,
		dial:         defaultDial,
		disabled:     envBool(EnvDisabled, false),
		strict:       envBool(EnvStrict, false),
		localProbe:   envBool(EnvProbeLocally, false),
	}
	if timeout := envDurationMS(EnvProbeTimeoutMS); timeout > 0 {
		gate.timeout = timeout
	}
	if ttl := envDurationMS(EnvReportTTLMS); ttl > 0 {
		gate.ttl = ttl
	}
	if after := envInt(EnvRecycleAfterFails); after > 0 {
		gate.recycleAfter = after
	}
	if targets := TargetsFromEnv(os.Getenv(EnvTargets)); len(targets) > 0 {
		gate.targets = targets
	}
	return gate
}

// DefaultGate is the process-wide gate used on the lease path.
func DefaultGate() *Gate {
	defaultGateOnce.Do(func() { defaultGate = NewGate(DefaultEgressTargets) })
	return defaultGate
}

// TargetsFromEnv parses a comma-separated "dns:<name>" / "tcp:<host>:<port>"
// list. It returns nil when the value is empty or unparsable.
func TargetsFromEnv(value string) []Target {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	targets := []Target{}
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		kind, rest, ok := strings.Cut(entry, ":")
		if !ok {
			continue
		}
		kind = strings.ToLower(strings.TrimSpace(kind))
		rest = strings.TrimSpace(rest)
		if rest == "" {
			continue
		}
		switch kind {
		case KindDNS:
			targets = append(targets, Target{Name: rest, Kind: KindDNS, Host: rest})
		case KindTCP:
			host, port, err := net.SplitHostPort(rest)
			if err != nil {
				// Bare host: default to the HTTPS port the credential flow and
				// `gh` use.
				targets = append(targets, Target{Name: rest, Kind: KindTCP, Host: rest, Port: 443})
				continue
			}
			parsed, err := strconv.Atoi(port)
			if err != nil {
				continue
			}
			targets = append(targets, Target{Name: host, Kind: KindTCP, Host: host, Port: parsed})
		}
	}
	if len(targets) == 0 {
		return nil
	}
	return targets
}

// Targets returns a copy of the probed targets.
func (g *Gate) Targets() []Target {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Target(nil), g.targets...)
}

// SetProbers replaces the resolver and dialer (tests, and callers that need to
// probe through a specific network path).
func (g *Gate) SetProbers(lookup func(ctx context.Context, host string) ([]string, error), dial func(ctx context.Context, network, addr string) (net.Conn, error)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if lookup != nil {
		g.lookup = lookup
	}
	if dial != nil {
		g.dial = dial
	}
}

// SetNow replaces the clock (tests).
func (g *Gate) SetNow(now func() time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now != nil {
		g.now = now
	}
}

// SetReportTTL sets how long an admitted report stays fresh (<=0: never stale).
func (g *Gate) SetReportTTL(ttl time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ttl = ttl
}

// SetProbeTimeout bounds a single probe attempt.
func (g *Gate) SetProbeTimeout(timeout time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if timeout > 0 {
		g.timeout = timeout
	}
}

// SetRecycleAfterFailures sets the failing-report streak that requests a pool
// recycle.
func (g *Gate) SetRecycleAfterFailures(after int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.recycleAfter = after
}

// SetDisabled turns the gate off (no lease is ever blocked).
func (g *Gate) SetDisabled(disabled bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.disabled = disabled
}

// SetStrict makes a missing probe report block leases (fail closed).
func (g *Gate) SetStrict(strict bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.strict = strict
}

// SetLocalProbe enables in-process probing (see EnvProbeLocally).
func (g *Gate) SetLocalProbe(local bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.localProbe = local
}

// Probe runs every target probe with the configured timeout and returns the
// report. It never mutates the gate: call Admit to feed the verdict back.
func (g *Gate) Probe(ctx context.Context) Report {
	g.mu.Lock()
	targets := append([]Target(nil), g.targets...)
	timeout := g.timeout
	lookup := g.lookup
	dial := g.dial
	now := g.now
	g.mu.Unlock()

	report := Report{Source: "in-process", CheckedAt: now().UTC()}
	started := now()
	for _, target := range targets {
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		result := ProbeResult{Name: target.Name, Kind: target.Kind, Target: target.Describe()}
		probeStarted := now()
		switch target.Kind {
		case KindDNS:
			hosts, err := lookup(probeCtx, target.Host)
			switch {
			case err != nil:
				result.Error = err.Error()
			case len(hosts) == 0:
				err = errNoAddresses
				result.Error = err.Error()
			default:
				result.Resolved = hosts
				result.OK = true
			}
			if !result.OK {
				result.Remediation = remediationFor(target)
			}
		default:
			conn, err := dial(probeCtx, "tcp", target.Addr())
			if err != nil {
				result.Error = err.Error()
				result.Remediation = remediationFor(target)
			} else {
				result.OK = true
				_ = conn.Close()
			}
		}
		cancel()
		result.DurationMS = now().Sub(probeStarted).Milliseconds()
		report.Targets = append(report.Targets, result)
	}
	report.DurationMS = now().Sub(started).Milliseconds()
	return report
}

// ProbeAndAdmit probes in-process and feeds the report back into the gate.
func (g *Gate) ProbeAndAdmit(ctx context.Context) (Report, error) {
	report := g.Probe(ctx)
	return report, g.Admit(report)
}

// Admit records a probe report (typically the one the in-pod script produced)
// and returns ErrPoolEgressUnhealthy when the pool failed the gate. Repeated
// failing reports request the recycling of the pool.
func (g *Gate) Admit(report Report) error {
	if len(report.Targets) == 0 {
		return fmt.Errorf("%w: report contains no probes", ErrPoolEgressInvalidReport)
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	healthy := report.Healthy()
	if report.CheckedAt.IsZero() {
		report.CheckedAt = g.now().UTC()
	}
	g.hasReport = true
	g.report = report
	if healthy {
		g.healthy = true
		g.healthySince = report.CheckedAt
		g.consecutiveFailures = 0
		g.recycleRequested = false
		g.lastError = ""
		return nil
	}
	g.healthy = false
	g.consecutiveFailures++
	g.lastError = report.Summary()
	if g.recycleAfter > 0 && g.consecutiveFailures >= g.recycleAfter {
		g.recycleRequested = true
	}
	return fmt.Errorf("%w: %s", ErrPoolEgressUnhealthy, report.Summary())
}

// AdmitLease is the gate the lease path calls before a pooled sandbox is handed
// out to a chat. A nil error means "this pool may accept the lease".
func (g *Gate) AdmitLease(ctx context.Context, subject string) error {
	err := g.leaseVerdict(ctx)
	g.mu.Lock()
	if err == nil {
		g.admittedLeases++
	} else {
		g.blockedLeases++
	}
	g.mu.Unlock()
	if err != nil {
		return fmt.Errorf("%w (subject %q)", err, subject)
	}
	return nil
}

// leaseVerdict decides without touching the counters.
func (g *Gate) leaseVerdict(ctx context.Context) error {
	g.mu.Lock()
	if g.disabled {
		g.mu.Unlock()
		return nil
	}
	armed := g.hasReport
	healthy := g.healthy
	report := g.report
	strict := g.strict
	localProbe := g.localProbe
	stale := g.reportStaleLocked()
	g.mu.Unlock()

	if !armed {
		// Nothing proved the pool's egress yet. Probing in-process is only
		// meaningful where the backend shares the pool's network; otherwise the
		// gate stays advisory unless it was armed in strict mode (fail closed).
		if localProbe {
			_, err := g.ProbeAndAdmit(ctx)
			return err
		}
		if strict {
			return fmt.Errorf("%w: no probe report was admitted yet", ErrPoolEgressUnhealthy)
		}
		return nil
	}
	if !healthy {
		// A failing verdict keeps blocking: a stale failing report does not
		// prove the pool recovered.
		if localProbe && stale {
			_, err := g.ProbeAndAdmit(ctx)
			return err
		}
		return fmt.Errorf("%w: %s", ErrPoolEgressUnhealthy, report.Summary())
	}
	if stale && localProbe {
		_, err := g.ProbeAndAdmit(ctx)
		return err
	}
	return nil
}

// reportStaleLocked reports whether the admitted report outlived the TTL.
func (g *Gate) reportStaleLocked() bool {
	if !g.hasReport || g.ttl <= 0 {
		return false
	}
	return g.now().Sub(g.report.CheckedAt) > g.ttl
}

// Status returns the current gate state. With force and local probing enabled,
// it refreshes the report first so an operator sees a live verdict.
func (g *Gate) Status(force bool) GateStatus {
	g.mu.Lock()
	localProbe := g.localProbe
	disabled := g.disabled
	g.mu.Unlock()

	if force && localProbe && !disabled {
		_, _ = g.ProbeAndAdmit(context.Background())
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	status := GateStatus{
		GeneratedAt:       now.UTC().Format(time.RFC3339),
		Disabled:          g.disabled,
		Strict:            g.strict,
		LocalProbe:        g.localProbe,
		Armed:             g.hasReport,
		Healthy:           g.hasReport && g.healthy,
		ConsecutiveFailed: g.consecutiveFailures,
		RecycleRequested:  g.recycleRequested,
		AdmittedLeases:    g.admittedLeases,
		BlockedLeases:     g.blockedLeases,
		LastError:         g.lastError,
		ProbeTimeoutMS:    g.timeout.Milliseconds(),
		ReportTTLMS:       g.ttl.Milliseconds(),
		Targets:           make([]string, 0, len(g.targets)),
	}
	for _, target := range g.targets {
		status.Targets = append(status.Targets, target.Describe())
	}
	switch {
	case !g.hasReport:
		status.State = "unknown"
	case g.healthy:
		status.State = "healthy"
	default:
		status.State = "unhealthy"
	}
	if g.hasReport {
		report := g.report
		status.Report = &report
		status.ReportAgeMS = now.Sub(report.CheckedAt).Milliseconds()
		status.ReportStale = g.reportStaleLocked()
		status.ReportRemediation = report.Remediation()
	}
	if !g.healthySince.IsZero() {
		status.HealthySince = g.healthySince.UTC().Format(time.RFC3339)
	}
	if g.portAuditor != nil {
		// Best effort and advisory: a degraded audit contributes an empty,
		// secret-free view and never turns the gate unhealthy (ci#616).
		portAudit := g.portAuditor.Status()
		status.PortAudit = &portAudit
	}
	return status
}

// SetPortAuditor attaches the per-workspace port-block auditor whose findings
// are surfaced in Status (ci#616). Passing nil detaches it again. The auditor is
// advisory: it is reported next to the gate's state and never influences
// Admit/ProbeAndAdmit, so port drift stays a diagnosable condition instead of a
// lease refusal.
func (g *Gate) SetPortAuditor(auditor *PortAuditor) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.portAuditor = auditor
}

// PortAuditor returns the attached port auditor, if any.
func (g *Gate) PortAuditor() *PortAuditor {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.portAuditor
}

// remediationFor explains how to fix a failed probe. It mentions no credential
// material, only the endpoints and the ci#546/ci#497 levers.
func remediationFor(target Target) string {
	if target.Kind == KindDNS {
		return fmt.Sprintf("cluster DNS cannot resolve %s from inside the pool pod: check the CoreDNS service and the pod/namespace egress DNS rules (ci#546), then recycle the pool", target.Host)
	}
	return fmt.Sprintf("no TCP egress from inside the pool pod to %s: check the NAT/firewall rules of the pool namespace and the credential endpoints of ci#497 (which `gh` needs), then recycle the pool", target.Addr())
}

// defaultLookup uses the pod's resolver, so the answer is the one the pool
// would really get.
func defaultLookup(ctx context.Context, host string) ([]string, error) {
	var resolver net.Resolver
	return resolver.LookupHost(ctx, host)
}

// defaultDial is the plain TCP dialer; a successful :443 dial proves NAT egress.
func defaultDial(ctx context.Context, network, addr string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, addr)
}

func envBool(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}

func envInt(key string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return 0
	}
	return parsed
}

func envDurationMS(key string) time.Duration {
	ms := envInt(key)
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}
