// Pure helpers for the chat list (ci#579). Kept out of the component so the
// pagination and polling decisions can be unit-tested without a browser.

// Initial number of chats fetched and rendered by the sidebar.
export const CHAT_LIST_PAGE_SIZE = 20

// How often the batch chat-state endpoint is polled. While an interaction is
// actively running (or waiting on the user) the indicator should feel live; when
// every listed interaction is idle/finished/failed there is no reason to hammer
// the queue inspector, so the interval backs off (ci#579).
export const CHAT_STATES_ACTIVE_REFRESH_MS = 3000
export const CHAT_STATES_IDLE_REFRESH_MS = 12000

// Whether the list should offer to fetch older chats. `total` is the number of
// chats matching the active filters (returned by the list API); when it is
// unknown (older backend) fall back to "the last page was full".
export function hasMoreChats({
  total,
  rowCount,
  limit,
}: {
  total?: number
  rowCount: number
  limit: number
}): boolean {
  if (typeof total === "number" && Number.isFinite(total)) {
    return rowCount < total
  }
  return rowCount >= limit
}

type ChatStateLike = { state?: string } | null | undefined

// The chat-state poll interval, given the latest batch response. Hidden tabs
// and idle lists poll far less often than a list with a live interaction.
export function chatStatesRefreshInterval(
  states: ChatStateLike[] | undefined,
  hidden: boolean,
): number {
  if (hidden) {
    return 0
  }
  const hasLiveInteraction = (states ?? []).some(
    (row) => row?.state === "active" || row?.state === "needs_confirmation",
  )
  return hasLiveInteraction ? CHAT_STATES_ACTIVE_REFRESH_MS : CHAT_STATES_IDLE_REFRESH_MS
}

// Resolves which chat UUIDs currently have a state indicator slot. Every
// automated chat gets one (even while its state is still loading or resolves to
// idle) so the row never shifts sideways when the batch state request lands.
export function collectAutomatedChatUuids(
  rows: Array<{ uuid?: string; partner?: { is_automated?: boolean } }> | undefined,
): Set<string> {
  const uuids = new Set<string>()
  for (const row of rows ?? []) {
    if (row?.uuid && row?.partner?.is_automated) {
      uuids.add(row.uuid)
    }
  }
  return uuids
}
