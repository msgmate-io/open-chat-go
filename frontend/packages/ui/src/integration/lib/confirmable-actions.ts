// Shared selection rules for the confirmable-action widgets.

// A confirmable action as far as selecting the blocking one is concerned. The
// full action type lives with the widget; this keeps the rule importable without
// pulling the component (and its Vike-dependent barrel) into a plain unit test.
export type PendingActionLike = {
  action_id?: string;
  status?: string;
};

// latestPendingConfirmableActionId returns the id of the last still-unconfirmed
// action in document order. That action is what blocks the chat, so the core and
// the msgmate coder renderer auto-expand its widget (the user may still collapse
// it manually). An action without a status is treated as pending; executed,
// failed, rejected and cancelled actions are ignored. The input is never
// mutated.
export function latestPendingConfirmableActionId(
  actions: ReadonlyArray<PendingActionLike> | null | undefined,
): string | undefined {
  if (!actions || actions.length === 0) {
    return undefined;
  }
  for (let index = actions.length - 1; index >= 0; index -= 1) {
    const action = actions[index];
    if (action && (action.status ?? "pending") === "pending") {
      return action.action_id;
    }
  }
  return undefined;
}
