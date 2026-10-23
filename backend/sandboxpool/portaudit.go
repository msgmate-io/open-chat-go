package sandboxpool

// Per-workspace port-block audit for pooled/shared sandboxes (ci#616, the
// follow-up of the ci#546 shared-sandbox networking design).
//
// The reconciler helpers were pure and unit-tested but never *called* by a
// reconcile/health path, so a workspace that binds outside its reserved block
// was still discovered late, as `address already in use`. This file is the
// core-side health-path wiring:
//
//   - it resolves the workspace's reserved block with the same key/fallback
//     resolution the allocator injects under (`OPENCHAT_PORT_BASE` /
//     `OPENCHAT_PORT_LAST` are authoritative; a host whose allocator uses
//     another base/span aligns the fallback with SetPortBlockFallback),
//   - it runs the in-pod collector (WorkspacePortAuditScript) through the
//     caller-supplied runner and classifies the pod's listeners into the
//     workspace's reserved block vs the out-of-block drift,
//   - it logs drift as a diagnosable condition, surfaces a workspace that keeps
//     binding outside its allocation (PortDriftWarnStreak) and exposes a
//     secret-free status view (PortAuditor.Status) for the pool health path,
//   - and it stays cheap and best effort: a pod without `ss`/`netstat`, a
//     missing exec seam or a failed scan yields Available=false with no
//     findings and *no error*, so the audit is a no-op there and can never
//     refuse a lease.

import (
	"context"
	"hash/fnv"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// EnvWorkspacePortBase and EnvWorkspacePortLast are the env keys the
	// workspace allocation is injected under (ci#546). They are the
	// authoritative source of a workspace's reserved block: when present the
	// audit uses exactly the block the workspace was handed.
	EnvWorkspacePortBase = "OPENCHAT_PORT_BASE"
	EnvWorkspacePortLast = "OPENCHAT_PORT_LAST"

	// PortAuditNoToolingMarker is printed by WorkspacePortAuditScript when the
	// pod has neither `ss` nor `netstat`. It is the explicit "no signal"
	// signal: the audit reports no findings and no error.
	PortAuditNoToolingMarker = "OPENCHAT_PORT_AUDIT_NO_TOOLING"

	// DefaultWorkspacePortBase and DefaultWorkspacePortSize are the fallback
	// allocation used for a workspace without injected env. They mirror the
	// allocator's base/span defaults and are overridable per host.
	DefaultWorkspacePortBase = 20000
	DefaultWorkspacePortSize = 16

	// DefaultPortDriftWarnStreak is how many consecutive audits must find the
	// same workspace outside its block before it is surfaced as recurring.
	DefaultPortDriftWarnStreak = 3

	// DefaultPortAuditInterval is the periodic sweep interval.
	DefaultPortAuditInterval = 5 * time.Minute
)

// WorkspacePortAuditScript collects the pod's listening TCP ports. It prefers
// `ss`, falls back to `netstat` and prints PortAuditNoToolingMarker when
// neither exists, so the caller can distinguish "no listeners" from "no
// signal". It never fails the shell: the collector is best effort.
const WorkspacePortAuditScript = `if command -v ss >/dev/null 2>&1; then
  ss -ltnH 2>/dev/null || ss -ltn 2>/dev/null
elif command -v netstat >/dev/null 2>&1; then
  netstat -ltn 2>/dev/null
else
  echo OPENCHAT_PORT_AUDIT_NO_TOOLING
fi
`

// Fixed, secret-free reason tokens. They are the only thing a degraded audit
// ever reports, so the status view can never leak scan output.
const (
	PortAuditReasonNoBlock       = "no-port-block"
	PortAuditReasonNoCollector   = "no-collector"
	PortAuditReasonUnavailable   = "collector-unavailable"
	PortAuditReasonNoTooling     = "no-port-tooling"
	PortAuditReasonNoListeners   = "no-listeners"
	PortAuditReasonOutsideBlock  = "out-of-block-listeners"
	PortAuditReasonWithinBlock   = "within-block"
	PortAuditReasonCtxCancelled  = "cancelled"
	PortAuditReasonBlockResolved = "block-resolved"
)

// PortBlock is a workspace's reserved (inclusive) port range.
type PortBlock struct {
	Base int
	Last int
}

// Valid reports whether the block is a usable, ordered range.
func (b PortBlock) Valid() bool { return b.Base > 0 && b.Last >= b.Base }

// Size is the number of ports in the block.
func (b PortBlock) Size() int {
	if !b.Valid() {
		return 0
	}
	return b.Last - b.Base + 1
}

// Contains reports whether port is inside the reserved block.
func (b PortBlock) Contains(port int) bool { return b.Valid() && port >= b.Base && port <= b.Last }

// String renders the block without any workspace-identifying data.
func (b PortBlock) String() string {
	if !b.Valid() {
		return "unallocated"
	}
	return strconv.Itoa(b.Base) + "-" + strconv.Itoa(b.Last)
}

// ResolvePortBlock resolves the reserved block of a workspace: the injected env
// wins (the same keys the allocation travels under), otherwise the fallback
// block derived from the workspace key is used. The second result is false when
// neither is available, in which case the caller reports "no signal".
func ResolvePortBlock(env map[string]string, workspaceKey string) (PortBlock, bool) {
	return resolvePortBlock(env, workspaceKey, DefaultWorkspacePortBase, DefaultWorkspacePortSize)
}

func resolvePortBlock(env map[string]string, workspaceKey string, fallbackBase, fallbackSize int) (PortBlock, bool) {
	base := portAuditEnvInt(env[EnvWorkspacePortBase])
	last := portAuditEnvInt(env[EnvWorkspacePortLast])
	if base > 0 {
		if last >= base {
			return PortBlock{Base: base, Last: last}, true
		}
		// Base without a usable last port: the span of the allocation is
		// implied by the configured size.
		if fallbackSize < 1 {
			fallbackSize = DefaultWorkspacePortSize
		}
		return PortBlock{Base: base, Last: base + fallbackSize - 1}, true
	}
	key := strings.TrimSpace(workspaceKey)
	if key == "" {
		return PortBlock{}, false
	}
	if fallbackBase < 1 {
		fallbackBase = DefaultWorkspacePortBase
	}
	if fallbackSize < 1 {
		fallbackSize = DefaultWorkspacePortSize
	}
	slot := int(workspaceKeyHash(key) % uint32(fallbackSize))
	base = fallbackBase + slot*fallbackSize
	return PortBlock{Base: base, Last: base + fallbackSize - 1}, true
}

// portAuditEnvInt parses a positive env value, returning 0 when absent/invalid.
func portAuditEnvInt(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// workspaceKeyHash is the stable slot key of a workspace.
func workspaceKeyHash(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return h.Sum32()
}

// ParseListeningPorts extracts the listening TCP ports from `ss -ltn` or
// `netstat -ltn` output. Parsing is strict: only a well formed local
// address:port field with a valid port counts, headers and unrelated columns
// are ignored, and the result is sorted and deduplicated.
func ParseListeningPorts(raw string) []int {
	ports := make([]int, 0, 8)
	seen := make(map[int]bool)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, PortAuditNoToolingMarker) {
			continue
		}
		for _, field := range strings.Fields(line) {
			port, ok := listeningPortFromField(field)
			if !ok {
				continue
			}
			if !seen[port] {
				seen[port] = true
				ports = append(ports, port)
			}
			break
		}
	}
	sort.Ints(ports)
	return ports
}

// listeningPortFromField parses one `host:port` token. `ss` and `netstat` both
// print the local address before the peer address, so the first well formed
// token of a line is the listener.
func listeningPortFromField(field string) (int, bool) {
	idx := strings.LastIndex(field, ":")
	if idx <= 0 || idx == len(field)-1 {
		return 0, false
	}
	port, err := strconv.Atoi(field[idx+1:])
	if err != nil || port < 1 || port > 65535 {
		return 0, false
	}
	if strings.TrimSpace(field[:idx]) == "" {
		return 0, false
	}
	return port, true
}

// PortAudit is the result of auditing one workspace. It is JSON ready and
// secret-free: it carries ports, the reserved block and a fixed reason token.
type PortAudit struct {
	WorkspaceKey string `json:"workspace_key"`
	BlockBase    int    `json:"block_base,omitempty"`
	BlockLast    int    `json:"block_last,omitempty"`
	Reserved     []int  `json:"reserved,omitempty"`
	OutOfBlock   []int  `json:"out_of_block,omitempty"`
	// Available is false when the audit produced no signal (no block, no
	// collector, no tooling in the pod, cancelled). A degraded audit is never
	// an error and never blocks a lease.
	Available  bool   `json:"available"`
	Reason     string `json:"reason,omitempty"`
	Recurring  bool   `json:"recurring,omitempty"`
	WarnStreak int    `json:"warn_streak,omitempty"`
}

// Block returns the audited reserved block.
func (p PortAudit) Block() PortBlock { return PortBlock{Base: p.BlockBase, Last: p.BlockLast} }

// Drifted reports whether the workspace holds listeners outside its block.
func (p PortAudit) Drifted() bool { return len(p.OutOfBlock) > 0 }

// AuditWorkspacePorts classifies listening ports against a workspace's block.
// It is the pure core of the reconciler and agrees with the injected env by
// construction: the block is the one resolved from OPENCHAT_PORT_BASE/_LAST.
func AuditWorkspacePorts(workspaceKey string, block PortBlock, listening []int) PortAudit {
	audit := PortAudit{
		WorkspaceKey: workspaceKey,
		BlockBase:    block.Base,
		BlockLast:    block.Last,
		Available:    true,
		Reason:       PortAuditReasonWithinBlock,
	}
	for _, port := range listening {
		if block.Contains(port) {
			audit.Reserved = append(audit.Reserved, port)
			continue
		}
		audit.OutOfBlock = append(audit.OutOfBlock, port)
	}
	sort.Ints(audit.Reserved)
	sort.Ints(audit.OutOfBlock)
	switch {
	case audit.Drifted():
		audit.Reason = PortAuditReasonOutsideBlock
	case len(audit.Reserved) == 0:
		audit.Reason = PortAuditReasonNoListeners
	}
	return audit
}

// PortAuditTarget is one workspace to audit in a sweep: its key and the env the
// allocation was injected under.
type PortAuditTarget struct {
	WorkspaceKey string
	Env          map[string]string
}

// PortAuditor runs the port-block audit for the live pooled workspaces. All
// methods are safe for concurrent use.
type PortAuditor struct {
	mu sync.Mutex

	runner func(ctx context.Context, workspaceKey, script string) (string, error)

	fallbackBase int
	fallbackSize int
	warnAfter    int

	last      map[string]PortAudit
	streaks   map[string]int
	lastError string

	now func() time.Time
}

// NewPortAuditor returns an auditor without a runner: it reports "no-collector"
// until SetRunner wires the in-pod exec seam, i.e. it is inert, not an error.
func NewPortAuditor() *PortAuditor {
	return &PortAuditor{
		fallbackBase: DefaultWorkspacePortBase,
		fallbackSize: DefaultWorkspacePortSize,
		warnAfter:    DefaultPortDriftWarnStreak,
		last:         make(map[string]PortAudit),
		streaks:      make(map[string]int),
	}
}

var (
	defaultPortAuditorOnce sync.Once
	defaultPortAuditor     *PortAuditor
)

// DefaultPortAuditor returns the process-wide auditor the pool health path and
// the periodic sweep share.
func DefaultPortAuditor() *PortAuditor {
	defaultPortAuditorOnce.Do(func() { defaultPortAuditor = NewPortAuditor() })
	return defaultPortAuditor
}

// SetRunner wires the in-pod collector (the SSH/kubectl exec seam). It is the
// only requirement for the audit to produce a signal; without it the audit is
// a no-op.
func (a *PortAuditor) SetRunner(runner func(ctx context.Context, workspaceKey, script string) (string, error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.runner = runner
}

// SetPortBlockFallback aligns the key-derived fallback block with the host's
// allocator. The injected OPENCHAT_PORT_BASE/_LAST always win over it.
func (a *PortAuditor) SetPortBlockFallback(base, size int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if base > 0 {
		a.fallbackBase = base
	}
	if size > 0 {
		a.fallbackSize = size
	}
}

// SetPortDriftWarnStreak sets how many consecutive drifting audits surface a
// workspace as recurring.
func (a *PortAuditor) SetPortDriftWarnStreak(streak int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if streak > 0 {
		a.warnAfter = streak
	}
}

// SetNow overrides the clock (tests).
func (a *PortAuditor) SetNow(now func() time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.now = now
}

func (a *PortAuditor) nowTime() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

// Audit resolves the workspace's block, collects the pod's listeners and
// classifies them. It never returns an error: a pod without tooling, a missing
// runner or a failed scan yields Available=false with no findings.
func (a *PortAuditor) Audit(ctx context.Context, workspaceKey string, env map[string]string) PortAudit {
	a.mu.Lock()
	base, size := a.fallbackBase, a.fallbackSize
	runner := a.runner
	a.mu.Unlock()

	block, ok := resolvePortBlock(env, workspaceKey, base, size)
	if !ok {
		return a.record(PortAudit{WorkspaceKey: workspaceKey, Available: false, Reason: PortAuditReasonNoBlock})
	}
	if ctx != nil && ctx.Err() != nil {
		return a.record(PortAudit{WorkspaceKey: workspaceKey, BlockBase: block.Base, BlockLast: block.Last, Available: false, Reason: PortAuditReasonCtxCancelled})
	}
	if runner == nil {
		return a.record(PortAudit{WorkspaceKey: workspaceKey, BlockBase: block.Base, BlockLast: block.Last, Available: false, Reason: PortAuditReasonNoCollector})
	}
	raw, err := runner(ctx, workspaceKey, WorkspacePortAuditScript)
	return a.record(auditRawOutput(workspaceKey, block, raw, err))
}

// auditRawOutput classifies a collector run. Kept separate from Audit so the
// best-effort contract (missing tooling, failed scan => no signal, no error) is
// testable without an exec seam.
func auditRawOutput(workspaceKey string, block PortBlock, raw string, err error) PortAudit {
	if err != nil {
		return PortAudit{WorkspaceKey: workspaceKey, BlockBase: block.Base, BlockLast: block.Last, Available: false, Reason: PortAuditReasonUnavailable}
	}
	if strings.Contains(raw, PortAuditNoToolingMarker) {
		return PortAudit{WorkspaceKey: workspaceKey, BlockBase: block.Base, BlockLast: block.Last, Available: false, Reason: PortAuditReasonNoTooling}
	}
	return AuditWorkspacePorts(workspaceKey, block, ParseListeningPorts(raw))
}

// record stores the audit, maintains the drift streak, logs the diagnosable
// condition and marks a workspace that keeps binding outside its allocation.
func (a *PortAuditor) record(audit PortAudit) PortAudit {
	a.mu.Lock()
	if audit.Drifted() {
		a.streaks[audit.WorkspaceKey]++
	} else {
		delete(a.streaks, audit.WorkspaceKey)
	}
	audit.WarnStreak = a.streaks[audit.WorkspaceKey]
	warnAfter := a.warnAfter
	if warnAfter < 1 {
		warnAfter = DefaultPortDriftWarnStreak
	}
	audit.Recurring = audit.WarnStreak >= warnAfter
	if audit.Available {
		a.lastError = ""
	} else {
		a.lastError = audit.Reason
	}
	a.last[audit.WorkspaceKey] = audit
	a.mu.Unlock()

	if audit.Drifted() {
		log.Printf("sandboxpool: port drift: workspace %q holds %v outside its reserved block %s (streak %d, recurring=%t)",
			audit.WorkspaceKey, audit.OutOfBlock, audit.Block().String(), audit.WarnStreak, audit.Recurring)
	}
	if !audit.Available {
		log.Printf("sandboxpool: port audit for workspace %q has no signal (%s)", audit.WorkspaceKey, audit.Reason)
	}
	return audit
}

// Last returns the most recent audit of a workspace.
func (a *PortAuditor) Last(workspaceKey string) (PortAudit, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	audit, ok := a.last[workspaceKey]
	return audit, ok
}

// Sweep audits every target, stopping early when the context is done.
func (a *PortAuditor) Sweep(ctx context.Context, targets []PortAuditTarget) []PortAudit {
	results := make([]PortAudit, 0, len(targets))
	for _, target := range targets {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		results = append(results, a.Audit(ctx, target.WorkspaceKey, target.Env))
	}
	return results
}

// StartLoop runs the audit periodically (and once immediately) until the
// returned stop function or the context ends. It is best effort: a sweep never
// panics, never blocks a lease and never returns an error.
func (a *PortAuditor) StartLoop(ctx context.Context, interval time.Duration, targets func(context.Context) []PortAuditTarget) func() {
	if targets == nil {
		return func() {}
	}
	if interval <= 0 {
		interval = DefaultPortAuditInterval
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		a.Sweep(loopCtx, targets(loopCtx))
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				a.Sweep(loopCtx, targets(loopCtx))
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// PortAuditStatus is the operator view of the audit: what drifted, what keeps
// drifting and why an audit had no signal. It never contains scan output or
// workspace content.
type PortAuditStatus struct {
	Enabled             bool        `json:"enabled"`
	AuditedWorkspaces   int         `json:"audited_workspaces"`
	Unavailable         int         `json:"unavailable"`
	DriftedWorkspaces   []string    `json:"drifted_workspaces,omitempty"`
	RecurringWorkspaces []string    `json:"recurring_workspaces,omitempty"`
	Findings            []PortAudit `json:"findings,omitempty"`
	WarnStreak          int         `json:"warn_streak"`
	LastError           string      `json:"last_error,omitempty"`
	GeneratedAt         string      `json:"generated_at"`
}

// Status returns the current, secret-free audit view for the pool health path.
// A degraded auditor reports Enabled=false/Unavailable>0 and no findings, i.e.
// it never turns into a failed probe.
func (a *PortAuditor) Status() PortAuditStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	warnAfter := a.warnAfter
	if warnAfter < 1 {
		warnAfter = DefaultPortDriftWarnStreak
	}
	status := PortAuditStatus{
		Enabled:           a.runner != nil,
		WarnStreak:        warnAfter,
		LastError:         a.lastError,
		GeneratedAt:       a.nowTime().UTC().Format(time.RFC3339),
		AuditedWorkspaces: len(a.last),
	}
	keys := make([]string, 0, len(a.last))
	for key := range a.last {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		audit := a.last[key]
		if !audit.Available {
			status.Unavailable++
			continue
		}
		status.Findings = append(status.Findings, audit)
		if audit.Drifted() {
			status.DriftedWorkspaces = append(status.DriftedWorkspaces, key)
		}
		if audit.Recurring {
			status.RecurringWorkspaces = append(status.RecurringWorkspaces, key)
		}
	}
	return status
}
