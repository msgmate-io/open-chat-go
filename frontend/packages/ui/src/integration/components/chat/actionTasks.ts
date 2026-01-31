import type { ConfirmableAction } from "./ConfirmableActionWidget";

/**
 * ActionTaskAction is the compact summary the action-tasks API returns for each
 * pending action. It is intentionally minimal (kind + identity) because the
 * full proposal lives in the message meta_data; the fields below are the ones
 * the preview widget also needs, mirrored so older/reduced payloads still work.
 */
export type ActionTaskActionSummary = {
  action_id?: string;
  target_tool_name?: string;
  title?: string;
  description?: string;
  danger_level?: string;
  input?: Record<string, unknown>;
  confirm_label?: string;
  continue_after_execute?: boolean;
  status?: ConfirmableAction["status"];
};

/**
 * extractConfirmableActionsFromMeta reads the persisted `confirmable_actions`
 * array out of a raw message meta_data object. It tolerates a missing or
 * malformed value and only returns well-formed actions.
 */
export function extractConfirmableActionsFromMeta(
  meta: Record<string, unknown> | undefined | null,
): ConfirmableAction[] {
  const raw = meta?.confirmable_actions;
  if (!Array.isArray(raw)) {
    return [];
  }
  return raw.filter(
    (action): action is ConfirmableAction =>
      !!action && typeof action === "object" && typeof (action as ConfirmableAction).action_id === "string",
  );
}

/**
 * mergeConfirmableAction builds the full widget action for the
 * actions-required preview. The action-tasks summary alone drops the proposed
 * parameters, which made the widget render "No params"/"{}" and an empty edit
 * preview. The complete proposal (edit text, tool args, context) is persisted in
 * the message meta_data, so it is merged in by action_id; the summary is then
 * used as a fallback for servers that already carry the params (ci#489).
 */
export function mergeConfirmableAction(
  task: ActionTaskActionSummary,
  metaActions?: ReadonlyArray<ConfirmableAction> | null,
): ConfirmableAction | null {
  if (!task?.action_id || !task?.target_tool_name) {
    return null;
  }
  const match = (metaActions ?? []).find((action) => action?.action_id === task.action_id);
  return {
    ...match,
    action_id: task.action_id,
    target_tool_name: match?.target_tool_name || task.target_tool_name,
    title: match?.title ?? task.title,
    description: match?.description ?? task.description,
    danger_level: match?.danger_level ?? task.danger_level,
    input: match?.input ?? task.input,
    confirm_label: match?.confirm_label ?? task.confirm_label,
    continue_after_execute: match?.continue_after_execute ?? task.continue_after_execute,
    status: match?.status ?? task.status ?? "pending",
  };
}
