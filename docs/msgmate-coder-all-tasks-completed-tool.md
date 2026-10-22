# Native msgmate coder tool: `all_tasks_completed` (ci#809)

Status: **plan only — implementation pending** (investigation done in the prior sandbox turn;
no code was committed there, so this draft PR carries the contract + wiring plan).

## Goal
Give the native msgmate coder an explicit, machine-readable way to declare that the
whole assigned task is finished, so the in-product auto-continuation loop stops
deterministically instead of relying on a visible text sentinel.

## Existing hooks this tool plugs into
* `backend/api/chats/AutoContinue.go` already defines the completion convention:
  * `autoContinueTaskCompletedKey = "task_completed"` — message-metadata flag that
declares the whole task complete (machine-readable counterpart of the marker).
  * `DefaultAutoContinueCompletionMarker = "<<TASK_COMPLETE>>"` — text sentinel.
  * `DefaultAutoContinueHumanMarker = "<<NEED_HUMAN>>"` — human-decision sentinel.
  * `turnEndedMetadataKey = "turn_ended"` — records why a native coder turn ended.
* Native coder tools live in `backend/api/msgmate/tools/` (`coder_*_tool.go`) and are
exposed/constructed through the msgmate tool registry (`NewCoder*Tool` constructors).

## Tool contract
`all_tasks_completed`

* Description: declares the assigned task complete; the auto-continue loop must not
  schedule another turn.
* Parameters:
  * `summary` (string, required) — short human-readable summary of what was done.
  * `blocked` (bool, optional) — set when the task is finished only because it is
    blocked and needs a human/next step (routes to the human marker path, not the
    clean-completion path).
* Behaviour: annotate the final message with `task_completed = true` metadata (and,
  for blocked runs, the human marker), then let the existing auto-continue decision
  path stop the loop with the corresponding stop reason.
* Idempotent: calling it twice must not double-stop or emit a second summary.

## Implementation steps
1. Add `backend/api/msgmate/tools/all_tasks_completed_tool.go` mirroring the existing
   `coder_context_tool.go` / `coder_schedule_tool.go` shape (tool definition var +
   runner), returning a structured result and setting the completion metadata.
2. Register the constructor alongside the other `NewCoder*Tool` constructors so the
   native coder tool list exposes it.
3. Add unit tests next to the tool (`*_tool_test.go`): success path sets
   `task_completed`, `blocked=true` routes to the human path, double invocation is
   idempotent, and the auto-continue resolver reads the metadata flag.
4. Keep the text sentinel support intact for back-compat.

## Verification
* `gofmt` + targeted `go test ./backend/api/msgmate/tools/...` and
  `go test ./backend/api/chats/...` (auto-continue decision path).
