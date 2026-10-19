import { mutate } from "swr";

// Centralised SWR cache keys/invalidation for chat-derived state.
//
// The chat sidebar keys its list request with `chat_types`/`scope` query params
// and derives a separate batch request for its state dots, so a bare-key
// `mutate("/api/v1/chats/list")` would silently miss the mounted hooks. All
// invalidation below therefore matches by prefix, which reliably hits every
// variant of a key that is currently in the cache.

const CHAT_LIST_PREFIX = "/api/v1/chats/list";
const CHAT_STATES_PREFIX = "/api/v1/chats/states";
const CHAT_ACTION_TASKS_PREFIX = "/api/v1/chats/action-tasks";

// `useSWRInfinite` (the paginated chat list, ci#579) stores its aggregated key
// as `$inf$<first page key>`. Prefix checks must strip that marker, otherwise
// invalidating the chat list silently skips the mounted infinite hook and new
// chats/previews only appear on the next poll.
const SWR_INFINITE_PREFIX = "$inf$";

const isStringKey = (key: unknown): key is string => typeof key === "string";

// The cache key without SWR's infinite marker, or null for a non-string key.
export const normalizeCacheKey = (key: unknown): string | null => {
  if (!isStringKey(key)) {
    return null;
  }
  return key.startsWith(SWR_INFINITE_PREFIX)
    ? key.slice(SWR_INFINITE_PREFIX.length)
    : key;
};

const hasPrefix = (key: unknown, prefix: string): boolean => {
  const normalized = normalizeCacheKey(key);
  return normalized !== null && normalized.startsWith(prefix);
};

export const isChatListKey = (key: unknown): boolean =>
  hasPrefix(key, CHAT_LIST_PREFIX);

export const isChatStatesKey = (key: unknown): boolean =>
  hasPrefix(key, CHAT_STATES_PREFIX);

export const isActionTasksKey = (key: unknown): boolean =>
  hasPrefix(key, CHAT_ACTION_TASKS_PREFIX);

// The sidebar badge and the full action-task view share the same endpoint but
// different query params: the compact `?count_only=1` feed only carries `{count}`,
// the plain one carries `{count, rows}`. Invalidation can treat them together,
// but an optimistic edit has to know which shape it is patching.
const ACTION_TASKS_COUNT_ONLY_PARAM = "count_only=1";

export const isActionTasksCountKey = (key: unknown): boolean => {
  const normalized = normalizeCacheKey(key);
  return (
    normalized !== null &&
    normalized.startsWith(CHAT_ACTION_TASKS_PREFIX) &&
    normalized.includes(ACTION_TASKS_COUNT_ONLY_PARAM)
  );
};

type ActionTaskCacheRow = { task_key?: string };

type ActionTasksCacheValue =
  | {
      count?: number;
      rows?: ActionTaskCacheRow[];
    }
  | undefined;

const countFromFeed = (current: NonNullable<ActionTasksCacheValue>, rows: ActionTaskCacheRow[]): number =>
  typeof current.count === "number" ? Math.max(0, current.count - 1) : rows.length;

// Pure updaters, exported so the optimistic cache maths is unit-testable
// without a browser or a mounted SWR cache.

// Drop one resolved task from the full feed (the action-task view).
export function applyActionTaskResolution(
  current: ActionTasksCacheValue,
  taskKey?: string | null,
): ActionTasksCacheValue {
  if (!current || !Array.isArray(current.rows) || !taskKey) {
    return current;
  }
  if (!current.rows.some((row) => row?.task_key === taskKey)) {
    return current;
  }
  const rows = current.rows.filter((row) => row?.task_key !== taskKey);
  return { ...current, rows, count: countFromFeed(current, rows) };
}

// Decrement the compact count the sidebar badge renders.
export function applyActionTaskCountDecrement(current: ActionTasksCacheValue): ActionTasksCacheValue {
  if (!current || typeof current.count !== "number") {
    return current;
  }
  return { ...current, count: Math.max(0, current.count - 1) };
}

// The backend keys a pending action by `<chat_uuid>:<message_uuid>` (lowercased,
// see backend/api/chats/ActionTasks.go `actionTaskKey`). The optimistic updaters
// need the same key to drop the right row, so it is derived here from the ids the
// confirmable-action widget already holds.
export const actionTaskKey = (chatUUID?: string | null, messageUUID?: string | null): string | null => {
  const chat = (chatUUID ?? "").trim().toLowerCase();
  const message = (messageUUID ?? "").trim().toLowerCase();
  if (!chat || !message) {
    return null;
  }
  return `${chat}:${message}`;
};

// Optimistically resolve an action task everywhere it is rendered, then
// revalidate both feeds in the background.
//
// Without this a confirmation only disappears after the next poll, so the
// action-task card lingered (and reappeared on an unrelated sidebar click)
// while the sidebar count badge stayed behind the card view (ci#726).
export function optimisticResolveActionTask(chatUUID?: string | null, messageUUID?: string | null) {
  const taskKey = actionTaskKey(chatUUID, messageUUID);
  if (!taskKey) {
    return Promise.resolve([]);
  }
  return mutate(
    (key: unknown) => isActionTasksKey(key),
    (current: ActionTasksCacheValue) => patchActionTasksFeed(current, taskKey),
    { revalidate: false },
  ).then(() => revalidateActionTasks());
}

// Drop a task from the feeds by explicit task key (the action-task view already
// holds the backend `task_key`, so it does not need to re-derive one).
export function optimisticResolveActionTaskKey(taskKey?: string | null) {
  if (!taskKey) {
    return Promise.resolve([]);
  }
  return mutate(
    (key: unknown) => isActionTasksKey(key),
    (current: ActionTasksCacheValue) => patchActionTasksFeed(current, taskKey),
    { revalidate: false },
  ).then(() => revalidateActionTasks());
}

// `mutate` hands the *same* updater every matched key, so the shape decides
// which optimistic edit applies: the count-only feed has no `rows`.
export function patchActionTasksFeed(
  current: ActionTasksCacheValue,
  taskKey?: string | null,
): ActionTasksCacheValue {
  if (!current) {
    return current;
  }
  if (Array.isArray(current.rows)) {
    return applyActionTaskResolution(current, taskKey);
  }
  return applyActionTaskCountDecrement(current);
}

const isChatScopedKey = (chatUUID: string) => {
  const base = `/api/v1/chats/${chatUUID}`;
  return (key: unknown): boolean => {
    const normalized = normalizeCacheKey(key);
    return (
      normalized !== null &&
      (normalized === base ||
        normalized.startsWith(`${base}/`) ||
        normalized.startsWith(`${base}?`))
    );
  };
};

// Revalidate the chat list (all filter/scope variants) so previews, unread
// badges and newly created chats appear without a manual reload.
export function revalidateChatsList() {
  return mutate(isChatListKey);
}

// Revalidate the batch chat-state endpoint that drives the sidebar state dots.
export function revalidateChatStates() {
  return mutate(isChatStatesKey);
}

// Revalidate the pending action-task feeds (compact count + full view).
export function revalidateActionTasks() {
  return mutate(isActionTasksKey);
}

// Revalidate one chat's detail, message list and interaction status.
export function revalidateChat(chatUUID?: string | null) {
  if (!chatUUID) {
    return Promise.resolve([]);
  }
  return mutate(isChatScopedKey(chatUUID));
}

// Revalidate every chat-derived cache after something may have changed backend
// state (new message, bot activity, action resolution, message send, ...).
export function revalidateChatData(chatUUID?: string | null) {
  return Promise.all([
    revalidateChatsList(),
    revalidateChatStates(),
    revalidateActionTasks(),
    revalidateChat(chatUUID),
  ]);
}
