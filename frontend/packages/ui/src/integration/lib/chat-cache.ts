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

const isStringKey = (key: unknown): key is string => typeof key === "string";

export const isChatListKey = (key: unknown): boolean =>
  isStringKey(key) && key.startsWith(CHAT_LIST_PREFIX);

export const isChatStatesKey = (key: unknown): boolean =>
  isStringKey(key) && key.startsWith(CHAT_STATES_PREFIX);

export const isActionTasksKey = (key: unknown): boolean =>
  isStringKey(key) && key.startsWith(CHAT_ACTION_TASKS_PREFIX);

const isChatScopedKey = (chatUUID: string) => {
  const base = `/api/v1/chats/${chatUUID}`;
  return (key: unknown): boolean =>
    isStringKey(key) &&
    (key === base || key.startsWith(`${base}/`) || key.startsWith(`${base}?`));
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
