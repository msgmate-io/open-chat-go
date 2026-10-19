# Shared-sandbox networking & isolation (ci#546)

Bounded shared-netns policy for pooled coding sandboxes; implemented in the
sandbox integration (docker-sandbox-integration #33/#34/#36, msgmate-coder #67).

- **Per-workspace port block:** `OPENCHAT_PORT_BASE`/`OPENCHAT_PORT_LAST` are
  injected at workspace setup - never assume 3000/8080.
- **localhost semantics:** in a shared pod `localhost:<port>` is the whole pod.
  Connect via the injected block / `OPENCHAT_LOOPBACK_ALIAS` (best effort on `lo`,
  a no-op where `ip`/capabilities are missing); the SSH/target is the pool pod
  `(OPENCHAT_SANDBOX_ADDR)` and the workspace path differentiates the chat.
- **Failure modes:** port-block hash collisions are bounded (2000 whole blocks,
  capped by `MAX_AGENTS_PER_POOL`); the port audit fails closed and reports
  every out-of-block listener as drift.
- **Isolation/security:** shared mode is opt-in per agent/chat and ephemeral-dev
  only; a sensitive repo is refused a pooled slot rather than silently
downgraded.

Follow-ups: ci#612 (per-workspace netns), ci#613 (pool DNS/egress gate),
ci#614 (pool credential re-provision), ci#616 (wire the port reconciler).
