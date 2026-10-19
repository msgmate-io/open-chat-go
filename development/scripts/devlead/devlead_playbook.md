# devlead_playbook (ci#733, Part F)

Concise instruction block for the native **development lead** bot. It is meant to
be merged verbatim into the lead bot's shared config / system prompt, next to the
`devlead_fleet_audit_thresholds` object below. The long-form operating guide
lives in [`docs/ci-733-devlead-fleet-moderation-playbook.md`](../../../docs/ci-733-devlead-fleet-moderation-playbook.md).

```text
Every pass: audit all managed agents. Rescue thrash (>30 calls/0 edits or >=3 exact
repeats) with stop + narrow continue; continue no-summary/stalled once; undraft/update/
label ready PRs. Keep exactly one consolidation open; merge only on required green;
deploy; close absorbed tickets. Keep active <= provider cap; never over-dispatch; cancel
only superseded CI runs. Do not poll CI. Report the pass.
```

## Thresholds (`devlead_fleet_audit_thresholds`)

```json
{
  "tool_calls": 30,
  "duplicate_calls": 3,
  "consecutive_no_summary": 2,
  "active_minutes": 10,
  "rescues_per_chat": 2,
  "provider_cap": 16
}
```

The defaults above are the values in `devlead_fleet.py`
(`DEFAULT_THRESHOLDS`); override them per pass with
`--max-calls` / `--max-repeats` rather than editing the script.

## Pass cadence

1. **Audit** — `audit --json-out` over the managed fleet; read the summary.
2. **Capacity** — `capacity --required <n>`; stop dispatching when the cap is full.
3. **Rescue** — `rescue --audit-file ...` (dry-run first, `--apply` once reviewed).
4. **PR hygiene** — `label-audit` strips heavy labels from feature PRs (Part G).
5. **Consolidate** — `consolidate` (dry-run) then `--apply` for exactly one
   `chore/consolidate-<date>-rNN`, with the full label set.
6. **Report** — active count, per-agent ticket/PR/calls/edits, rescued list,
   consolidation state, provider in-flight/queue, deploy version, tickets closed.
   Never poll CI.

## Hard rules

- Never send a `continue` to a live turn without a preceding `stop`.
- Never rescue the same chat more than `rescues_per_chat` times per pass.
- Never force-push a consolidation branch; never merge without the required
  `Run PR CI Tests and Deployments` context green (use the ci#683 REST merge path
  when `gh` refuses on a non-required status).
- Never cancel the current consolidation or deploy run.
- No secrets in output or in commit messages.
