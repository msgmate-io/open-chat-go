# Sandbox networking & isolation for shared/pooled coding agents (ci#546)

Sibling ticket: the shared/pooled sandbox mode (one pod, several agents, a random
workspace path each). This note is the deliberate design for the consequences of
that sharing, plus the minimal implementation that makes the decision explicit
and testable.

Implementation: `backend/sandboxnetwork` (`policy.go`, `policy_test.go`).

## The problem in one line

A pod is the isolation unit for the **network namespace**. Two workspaces in one
pod that both bind `:3000` collide, and `localhost:<port>` stops meaning
"my workspace's server". Both failures are silent at bind time and surface as an
unrelated flaky test later.

## Decision: isolate first, bound second, fail open into dedicated-pod semantics

The resolver (`sandboxnetwork.Resolve`) is total and deterministic. It can only
produce one of three verdicts:

| `coder_sandbox_network_mode` | `netns` for the workspace | `localhost` | Ports |
| --- | --- | --- | --- |
| `isolated` (**default**) | private (own netns / user-netns) | `127.0.0.1` | conventional ports unchanged, nothing allocated |
| `loopback_alias` | shared (`shared` pod netns) | `127.0.0.<slot+2>` | conventional ports unchanged, one address per slot |
| `shared_ports` | shared | `127.0.0.1` (shared) | one deterministic port slice per slot |

Rules that make this safe by construction:

1. **No shared pool is left unallocated.** If the pooled mode is requested
   (`coder_sandbox_mode: shared|pooled|shared-pod`) without an explicit network
   mode, the resolver materialises `shared_ports` and records a warning. The
   worst case is therefore a *bounded, visible* policy, never a runtime
   collision.
2. **Isolated means nothing changes.** Dedicated pods keep their own netns, so
   the default mode is byte-for-byte the current behaviour and no port is ever
   allocated (`PortFor(3000) == 3000`, no port window).
3. **Ports are a slice, not a guess.** `port_base + slot*stride .. +stride-1`
   with `DefaultPortBase=31000`, `DefaultPortStride=200`,
   `DefaultSlotCount=4` (matches the ci#602 "small pool" bound). `PortFor(p)`
   maps a conventional port into the slot's slice, stably and injectively.
4. **Env injection is the contract.** The workspace gets
   `OPENCHAT_SANDBOX_NETWORK_MODE/SLOT/NETNS/LOOPBACK/PORT_BASE/PORT_MIN/PORT_MAX`
   (and `STRIDE`), so a dev server reads its allocation instead of hard-coding
   3000. `Policy.EnvFor(base)` never overwrites an explicitly set value.
5. **Precedence is env → agent shared config → default**, for the same reason as
   ci#620/ci#662: the shared config merge materialises a literal into every agent
   config and would shadow a deployment lever forever.
6. **Clamps and warnings, never silent degradation.** `slot_count` is capped at
   `MaxSlotCount` (253: one `/24` of loopback aliases), `port_base` at `MinPort`
   (1024, the non-root floor), and a workspace without a stable key gets
   `UnassignedSlot` plus a warning instead of being handed slot 0 of somebody
   else.
 7. **Numeric levers are range-checked before the math.** Every env/config lever
    is parsed through a guard that only accepts `[0, MaxConfigInt]` (`1<<24`, one
    digit wider than the largest legal port); anything else falls back to the
    documented default. A typo such as
    `OPENCHAT_SANDBOX_PORT_STRIDE=99999999999` therefore cannot overflow the port
    window arithmetic — it is ignored instead of being narrowed into a
    caller-chosen value.

Loopback aliases start at `127.0.0.2` on purpose: `127.0.0.1` stays the pod's
plain loopback and is never an *exclusive* alias an agent could mistake for its
own.

## localhost semantics

`localhost` is only "mine" in `isolated` mode. In the two shared modes the
workspace is told, in env, what its `localhost` is:

- `loopback_alias`: bind/connect `$OPENCHAT_SANDBOX_LOOPBACK:<port>`; the
  conventional port number keeps working, which is the least surprising result
  for code the agent did not write.
- `shared_ports`: `$OPENCHAT_SANDBOX_LOOPBACK` is the shared `127.0.0.1` and the
  *port* is what distinguishes the workspaces — use
  `PortFor(conventional)` / stay inside `PORT_MIN..PORT_MAX`.

Design note for the agent prompt/tool docs: an agent must never assume
`localhost:3000` is its own in a pooled sandbox; the injected env is the
authoritative answer, and the pool reports `IsolationVerdict()`
(`own-netns` / `shared-netns-own-loopback` / `shared-netns-shared-localhost`) so
the transcript and the admin status can say which world it is in.

## DNS & egress

- **No change for isolated**: a dedicated pod's DNS/egress is what we already
  have, including the GitHub/API reachability the credential provisioning
  (ci#497) depends on.
- **Shared pool**: DNS and egress are pod-wide, not per-workspace — that is a
  *feature* for reachability (one warm DNS/egress path) and a *risk* for
  attribution (the pool's egress is shared). Credential provisioning still
  injects per-session, per-workspace tokens; the policy deliberately handles
  only public values (slots, ports, addresses) and never a credential, so a
  policy line in a log cannot leak one.
- A per-workspace netns (`slirp4netns`/user-netns) restores a private
  `localhost` **and** private egress, but each pair costs a userspace network
  stack; that is why it is the `isolated` option rather than the pool default.

## Service discovery of the shared sandbox

The coder target binding is the **pool pod**, not the chat: `EnvTargetAddr`
(`OPENCHAT_SANDBOX_TARGET_ADDR`) is the pool's SSH/target address, and the
**workspace path differentiates the chat**. Consequences to keep in mind:

- do not key a cache, a session or a host-key trust entry on the target address
  alone (that would alias N chats onto one entry) — key it on
  target + workspace key;
- do not health-check the pool per chat; the pool either serves all its slots or
  none, and a per-chat probe turns one outage into N;
- `SandboxName`/`SandboxRef` stay per-chat even though the address is shared, so
  a lease release must not tear down the pool.

## Isolation & security gating

Sharing a netns means one agent can see another agent's processes and traffic.
That is acceptable **only** for the ephemeral dev mode and **never** for a
sensitive repository. Gate it by repo/scope, and make the gate deny-by-default
for anything that does not look like a known repository slug:

- `coder_sandbox_require_network_isolation: true` — forces isolated; a
  shared/pooled request becomes a configuration error with a reason instead of a
  silently weakened boundary.
- `coder_sandbox_isolated_repos: "owner/a,owner/b"` — a repo allowlist that may
  never share a netns.
- an unknown/empty repository scope is **not** permitted to share a netns (a
  missing scope must fail closed), and a raw git remote is not a scope.

`SharedNetnsPermitted` returns `(allowed, reason)` so a caller can log *why* it
refused without echoing the repository URL beyond its slug.

## Resource contention

N agents in one pod contend for CPU, memory and disk. This package only carries
the *network* half, but the pool must be bounded by the same bound the port
allocation is derived from (`coder_sandbox_max_agents_per_pool` /
`..._max_shared_workspaces`, default 4): the slot count **is** the max-agents
bound, so a pool cannot be sized for 20 agents and allocate 4 slots. The memory
half lives in the sibling budget resolver (ci#662).

## Resource bound (the second half of `MaxWorkspaces`)

`MaxWorkspaces` bounds *how many* agents share one pod, not *how much* one of
them may consume; a single agent compiling a large tree can still starve the
others. `cgroupguard.go` is that second bound as policy, in the same
"policy + validation first, provisioning later" shape as the network half:

- one cgroup v2 slice per workspace under a configurable slice root
  (`open-chat-pool-workspace-<uuid>.slice`), with per-workspace `cpu.max`,
  `memory.max` and `pids.max` (defaults: half a core, 2 GiB, 512 pids);
- the pod budget is what the slices are carved from, and an enabled guard whose
  per-workspace limits would oversubscribe it is **refused** (`ErrInvalidCgroupPolicy`)
  rather than silently overcommitting the node;
- a pool without cgroup v2 delegation fails closed
  (`ErrWorkspaceCgroupUnavailable`) instead of running unbounded; the guard is on
  by default and the opt-out is the explicit lever
  `OCI_MSGMATE_CODER_SANDBOX_CGROUP_GUARD=0`;
- the two bounds agree at the call site: `EffectiveMaxWorkspaces` takes the
  smaller of the network bound and the resource bound, as this note requires;
- a full guard is a named signal (`CgroupExhaustedError` /
  `IsCgroupExhausted`, `pool_cgroup_exhausted`) that reuses the same remedy list
  (spill to another pool pod, then a dedicated sandbox).

The guard is process-local bookkeeping (`CgroupGuard`, mirroring
`PortAllocator`: idempotent per workspace, released on lease teardown); writing
the slices themselves and injecting the returned env belong to the shared
sandbox provisioning path. Per-workspace **disk** quota remains a follow-up.

## Failure modes (and what the operator sees)

| Failure | Behaviour | Visibility |
| --- | --- | --- |
| shared pool, no network mode set | `shared_ports` defaults are materialised | warning in `ReportLines()` |
| no workspace key | `UnassignedSlot`, ports/loopback not exclusive | warning |
| `slot_count > 253` | clamped to 253 | warning "split the pool into several pools" |
| `port_base < 1024` | clamped to 1024 | warning |
| overlapping port slices | impossible by construction; `Overlaps()` detects a misconfiguration | `Windows()` / `Overlaps()` for tests & audits |
| sensitive repo on a shared pool | denied by the repo gate with a reason | `SharedNetnsPermitted` reason |
| N agents, one node saturated | the ci#691 capacity cooldown applies (unchanged) | capacity error placeholder |
| all port blocks of a pool in use | the lease is refused with `ErrPoolNetworkCapacity`; the lease path may spill to another pool pod or fall back to a dedicated sandbox | `PoolNetworkExhaustedError` / `IsPoolNetworkExhausted`, and `exhaustion_signal` in the admin plan status |
| pool without cgroup v2 delegation | the resource guard **fails closed**: no unbounded workspace is provisioned | `ErrWorkspaceCgroupUnavailable`, reported by `CgroupGuard.ReportLines()` |
| per-workspace limits over the pod budget | the policy is refused with both numbers named instead of overcommitting the node | `ErrInvalidCgroupPolicy` |
| every resource slice of a pool in use | the lease is refused; same remedy list as the network bound | `CgroupExhaustedError` / `IsCgroupExhausted`, signal `pool_cgroup_exhausted` |
| guard disabled by lever | workspaces run unbounded (explicit, recorded opt-out) | `CgroupGuard.ReportLines()` says "unbounded" |

## Follow-ups (ticket, do not implement here)

1. **Implement `loopback_alias` in the sandbox integration** — a
   `127.0.0.0/8` alias per slot needs `CAP_NET_ADMIN` (or a pre-configured
   `lo` range) at pod start; if that is not available, only `shared_ports`
   remains and the resolver's default already covers it.
2. **Per-workspace netns provider (`slirp4netns`/user-netns)** — the true
   isolation option for a shared pod, with the `loopback_alias` fallback.
3. **Per-workspace disk quota** in the pool — the CPU/memory/pids half is
   implemented as policy in `cgroupguard.go` (see "Resource bound" above);
   disk is still unbounded and needs the same fail-closed treatment.
4. **Inject the policy env in the coder tool path** — `Policy.EnvFor` is ready;
   the wiring into workspace provisioning belongs to the shared-sandbox ticket's
   provisioning code (ci#602), not to this design note.
5. **Review the loopback alias range** against the sandbox image's `/etc/hosts`
   and any proxy that assumes `127.0.0.1`.