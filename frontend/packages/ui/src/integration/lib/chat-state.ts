// Shared mapping from an interaction chat's live state to the compact status
// chip the coder widget renders next to its "coder" label. The core header
// badge (InteractionChatInfo) and the msgmate coder widget reflect the same
// interaction state, so keeping the label/tone mapping in one place stops the
// two from drifting apart (ci#488). The chip is deliberately lower-case and
// terse; the header badge keeps its own title-cased wording.

export type ChatStateTone = "active" | "success" | "danger" | "attention" | "muted";

export interface ChatStateIndicator {
  label: string;
  tone: ChatStateTone;
}

// resolveChatStateIndicator maps an interaction status state to the compact
// chip, or null when there is no state to show. Unknown states are humanised
// (underscores to spaces) and shown muted so a new backend state is still
// visible rather than silently dropped.
export function resolveChatStateIndicator(
  state?: string | null,
): ChatStateIndicator | null {
  const normalized = typeof state === "string" ? state.trim() : "";
  if (!normalized) {
    return null;
  }
  switch (normalized) {
    case "active":
      return { label: "working", tone: "active" };
    case "finished":
      return { label: "done", tone: "success" };
    case "failed":
      return { label: "failed", tone: "danger" };
    case "needs_confirmation":
      return { label: "needs confirmation", tone: "attention" };
    case "idle":
      return { label: "idle", tone: "muted" };
    default:
      return { label: normalized.replace(/_/g, " "), tone: "muted" };
  }
}

// CHAT_STATE_TONE_DOT_CLASS is the status-dot colour per tone: amber pulsing for
// an in-flight turn, sky pulsing while a tool call awaits confirmation, green
// for done, rose for failed and muted for idle/unknown.
export const CHAT_STATE_TONE_DOT_CLASS: Record<ChatStateTone, string> = {
  active: "bg-yellow-400 animate-pulse",
  success: "bg-emerald-500",
  danger: "bg-rose-500",
  attention: "bg-sky-500 animate-pulse",
  muted: "bg-muted-foreground/60",
};
