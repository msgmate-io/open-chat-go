// Pure helpers for the chat list (ci#579). Kept out of the component so the
// pagination and polling decisions can be unit-tested without a browser.

// Both halves of the sidebar pagination (ci#579):
// - `CHAT_LIST_FETCH_BATCH_SIZE` is the `limit` of a single list request, so a
//   long history is loaded in small, incremental batches (newest first).
// - `CHAT_LIST_PAGE_SIZE` is how many chats are painted before the visible
//   window starts growing: the first few batches fill it up, so ~20 chats are
//   on screen immediately without fetching the whole history.
export const CHAT_LIST_FETCH_BATCH_SIZE = 5
export const CHAT_LIST_PAGE_SIZE = 20

// Batches that make up the initial painted window.
export const CHAT_LIST_INITIAL_BATCHES = Math.max(
  1,
  Math.ceil(CHAT_LIST_PAGE_SIZE / CHAT_LIST_FETCH_BATCH_SIZE),
)

// Hard cap mirroring the batch chat-state endpoint (`chatStatesMaxChats` in
// backend/api/chats/ChatStates.go). The sidebar only asks for the states of the
// chats it can actually resolve, instead of silently dropping the tail.
export const CHAT_STATES_MAX_CHATS = 100

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

// Truncates the uuids sent to the batch state endpoint to what it can resolve.
// The set is built from the newest chats first, so the oldest (least
// interesting) entries are the ones dropped.
export function chatStatesRequestUuids(
  uuids: Iterable<string>,
  max: number = CHAT_STATES_MAX_CHATS,
): string[] {
  const out: string[] = []
  if (max <= 0) {
    return out
  }
  for (const uuid of uuids) {
    if (out.length >= max) {
      break
    }
    out.push(uuid)
  }
  return out
}

// One page of the paginated chat-list API.
export type ChatListPage<T> = {
  rows?: T[]
  total?: number
  limit?: number
  page?: number
}

// Flattens the pages the sidebar fetched (newest first) into a single row list.
// The pinned top-up on page one and the offset windows of later pages can
// overlap, so rows are de-duplicated by uuid while keeping their first
// (newest) position.
export function mergeChatPages<T extends { uuid?: string }>(
  pages: Array<ChatListPage<T>> | undefined,
): { rows: T[]; total?: number } {
  if (!pages || pages.length === 0) {
    return { rows: [], total: undefined }
  }
  const seen = new Set<string>()
  const rows: T[] = []
  for (const page of pages) {
    for (const row of page?.rows ?? []) {
      const uuid = row?.uuid
      if (uuid) {
        if (seen.has(uuid)) {
          continue
        }
        seen.add(uuid)
      }
      rows.push(row)
    }
  }
  const firstTotal = pages[0]?.total
  return { rows, total: typeof firstTotal === "number" ? firstTotal : undefined }
}

// Applies an optimistic pin/unpin to a cached chat-list value, whatever shape
// it holds: the paginated sidebar (`useSWRInfinite`) caches an array of pages,
// while other callers cache a single page object. Patching both keeps the
// optimistic reorder (and the pin icon) immediate without waiting for the
// refetch, and is a no-op for values of any other shape (ci#579/#512).
export function applyPinnedToChatListValue<T>(
  value: T,
  chatUUID: string,
  pinned: boolean,
): T {
  if (!value) {
    return value
  }
  if (Array.isArray(value)) {
    return value.map((page) => applyPinnedToChatListValue(page, chatUUID, pinned)) as unknown as T
  }
  if (typeof value !== "object") {
    return value
  }
  const page = value as { rows?: Array<{ uuid?: string; pinned?: boolean }> }
  if (!Array.isArray(page.rows)) {
    return value
  }
  return {
    ...(value as object),
    rows: page.rows.map((row) =>
      row?.uuid === chatUUID ? { ...row, pinned } : row,
    ),
  } as T
}

// Frontend pagination: how many rows are painted for a given number of painted
// pages. It grows by one page per "load more", so ~20 chats paint first and
// older rows are appended a page at a time (ci#579).
export function paintedRowBudget(paintedPages: number): number {
  return Math.max(paintedPages, 1) * CHAT_LIST_PAGE_SIZE
}

// How many fetch batches are needed to back `paintedPages` painted pages. The
// batches are small (`CHAT_LIST_FETCH_BATCH_SIZE`), so a long history is pulled
// in several incremental requests, newest first (ci#579).
export function batchesForPaintedPages(
  paintedPages: number,
  pageSize: number = CHAT_LIST_PAGE_SIZE,
  batchSize: number = CHAT_LIST_FETCH_BATCH_SIZE,
): number {
  const safeBatchSize = Math.max(batchSize, 1)
  return Math.max(1, Math.ceil((Math.max(paintedPages, 1) * pageSize) / safeBatchSize))
}

// One batch request of the chat-list API. Every field becomes a query parameter
// of the `useSWRInfinite` key, except the ones that are empty/absent: those must
// stay out of the URL so an unfiltered request looks exactly like it did before.
export type ChatListQuery = {
  page: number
  limit: number
  chatTypes?: string
  search?: string
  tags?: string[]
  excludeTags?: string[]
  // The interaction state filter, applied server-side (ci#761). It has to be
  // part of the key: a key that ignored it would keep serving the previously
  // loaded page instead of refetching the matching rows.
  state?: string | null
  timeFrom?: string
  timeTo?: string
  scope?: string
}

// Serializes one list request. Kept out of the component so the "is the state
// filter part of the SWR key?" behaviour is unit-testable without a browser.
export function buildChatListQuery(query: ChatListQuery): string {
  const params = new URLSearchParams()
  if (query.chatTypes) {
    params.set("chat_types", query.chatTypes)
  }
  params.set("page", String(query.page))
  params.set("limit", String(query.limit))
  if (query.search) {
    params.set("q", query.search)
  }
  if (query.tags && query.tags.length > 0) {
    params.set("tags", query.tags.join(","))
  }
  if (query.excludeTags && query.excludeTags.length > 0) {
    params.set("exclude_tags", query.excludeTags.join(","))
  }
  if (query.timeFrom) {
    params.set("time_from", query.timeFrom)
  }
  if (query.timeTo) {
    params.set("time_to", query.timeTo)
  }
  if (query.state) {
    params.set("state", query.state)
  }
  if (query.scope) {
    params.set("scope", query.scope)
  }
  return params.toString()
}

// The full URL (and thereby the SWR key) of one chat-list batch.
export function buildChatListUrl(query: ChatListQuery): string {
  return `/api/v1/chats/list?${buildChatListQuery(query)}`
}
