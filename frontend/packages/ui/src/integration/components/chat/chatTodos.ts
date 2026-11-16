import { asRecord, resultRecord, shortToolName, type ToolCallLike } from "./toolCallSummary";

/**
 * Agent todos (ci#1100). The msgmate coder keeps its plan in the coder todo
 * tools (`todo_write` / `todo_read`), which write the list into the tool call
 * of the turn that changed it. The chat details surface therefore does not need
 * a new endpoint: the newest todo list is the last todo tool call in the
 * transcript, so the `todos` tab folds the message list into one snapshot.
 */

export type ChatTodoStatus = "pending" | "in_progress" | "completed" | "cancelled";

export interface ChatTodoItem {
  /** Stable per-item key when the tool emits one, otherwise the index. */
  id?: string;
  /** What has to be done, in imperative form. */
  content: string;
  /** Present-continuous form the tool may carry for an in-progress item. */
  activeForm?: string;
  status: ChatTodoStatus;
}

export interface ChatTodoSnapshot {
  todos: ChatTodoItem[];
  /** Short tool name that produced the snapshot, eg `todo_write`. */
  toolName: string;
  /** UUID of the message the snapshot was read from, when known. */
  messageUUID?: string;
  /** Send timestamp of that message, when known. */
  sendAt?: string;
}

/** One row of `/api/v1/chats/{uuid}/messages/list`. */
export interface ChatTodoMessageRow {
  uuid?: string;
  send_at?: string;
  tool_calls?: unknown;
}

// Tool names are matched after `shortToolName` stripped the `coder_` namespace
// and lowercased them, so `coder_todo_write`, `todo_write` and the older
// `todowrite` spelling all land on the same entry (ci#1100).
const TODO_WRITE_TOOLS = new Set(["todo_write", "todowrite", "write_todos", "todos_write"]);
const TODO_READ_TOOLS = new Set(["todo_read", "todoread", "read_todos", "todos", "todo"]);

/** True for every coder todo tool, reader and writer alike. */
export function isTodoToolName(name: string | undefined): boolean {
  const short = shortToolName(name);
  return TODO_WRITE_TOOLS.has(short) || TODO_READ_TOOLS.has(short);
}

/** True only for the tool that rewrites the list (the authoritative source). */
export function isTodoWriteToolName(name: string | undefined): boolean {
  return TODO_WRITE_TOOLS.has(shortToolName(name));
}

const STATUS_ALIASES: Record<string, ChatTodoStatus> = {
  pending: "pending",
  open: "pending",
  todo: "pending",
  not_started: "pending",
  "not started": "pending",
  queued: "pending",
  in_progress: "in_progress",
  "in-progress": "in_progress",
  inprogress: "in_progress",
  running: "in_progress",
  active: "in_progress",
  doing: "in_progress",
  started: "in_progress",
  completed: "completed",
  complete: "completed",
  done: "completed",
  finished: "completed",
  closed: "completed",
  cancelled: "cancelled",
  canceled: "cancelled",
  aborted: "cancelled",
  skipped: "cancelled",
  blocked: "cancelled",
};

/** Normalize the many status spellings tools use to the four we render. */
export function normalizeTodoStatus(raw: unknown): ChatTodoStatus {
  if (typeof raw !== "string") return "pending";
  const key = raw.trim().toLowerCase().replace(/\s+/g, " ");
  return STATUS_ALIASES[key] ?? STATUS_ALIASES[key.replace(/ /g, "_")] ?? "pending";
}

/** Human label for a todo status. */
export function todoStatusLabel(status: ChatTodoStatus): string {
  switch (status) {
    case "in_progress":
      return "In progress";
    case "completed":
      return "Completed";
    case "cancelled":
      return "Cancelled";
    default:
      return "Pending";
  }
}

export interface ChatTodoCounts {
  total: number;
  pending: number;
  inProgress: number;
  completed: number;
  cancelled: number;
}

/** Per-status counts of a snapshot, used for the summary and the progress line. */
export function todoCounts(todos: ChatTodoItem[]): ChatTodoCounts {
  const counts: ChatTodoCounts = {
    total: todos.length,
    pending: 0,
    inProgress: 0,
    completed: 0,
    cancelled: 0,
  };
  todos.forEach((todo) => {
    if (todo.status === "in_progress") counts.inProgress += 1;
    else if (todo.status === "completed") counts.completed += 1;
    else if (todo.status === "cancelled") counts.cancelled += 1;
    else counts.pending += 1;
  });
  return counts;
}

/** `3 of 7 done` style progress line. */
export function todoProgressLabel(todos: ChatTodoItem[]): string {
  const counts = todoCounts(todos);
  if (counts.total === 0) return "no todos";
  return `${counts.completed} of ${counts.total} done`;
}

// Tool arguments and results reach the UI either as objects or as the JSON
// string the bridge marshals, so both shapes are decoded here.
function parseMaybeJson(value: unknown): unknown {
  if (typeof value !== "string") return value;
  const trimmed = value.trim();
  if (!trimmed.startsWith("{") && !trimmed.startsWith("[")) return value;
  try {
    return JSON.parse(trimmed);
  } catch {
    return value;
  }
}

function asRecordValue(value: unknown): Record<string, unknown> | null {
  return asRecord(parseMaybeJson(value));
}

const TODO_ARRAY_KEYS = ["todos", "items", "tasks"] as const;
const TODO_CONTENT_KEYS = ["content", "task", "text", "title", "description", "name"] as const;
const TODO_ACTIVE_FORM_KEYS = ["activeForm", "active_form", "activeform"] as const;

function normalizeTodoItem(raw: unknown): ChatTodoItem | null {
  if (typeof raw === "string") {
    const content = raw.trim();
    return content ? { content, status: "pending" } : null;
  }

  const record = asRecord(raw);
  if (!record) return null;

  let content = "";
  for (const key of TODO_CONTENT_KEYS) {
    const value = record[key];
    if (typeof value === "string" && value.trim()) {
      content = value.trim();
      break;
    }
  }
  if (!content) return null;

  let activeForm: string | undefined;
  for (const key of TODO_ACTIVE_FORM_KEYS) {
    const value = record[key];
    if (typeof value === "string" && value.trim()) {
      activeForm = value.trim();
      break;
    }
  }

  const id = typeof record.id === "string" && record.id.trim() ? record.id.trim() : undefined;
  const status = normalizeTodoStatus(record.status ?? record.state);

  return { ...(id ? { id } : {}), content, ...(activeForm ? { activeForm } : {}), status };
}

function todosFromRecord(record: Record<string, unknown> | null): ChatTodoItem[] {
  if (!record) return [];
  for (const key of TODO_ARRAY_KEYS) {
    const raw = record[key];
    if (!Array.isArray(raw)) continue;
    const todos = raw
      .map(normalizeTodoItem)
      .filter((todo): todo is ChatTodoItem => todo !== null);
    if (todos.length > 0 || raw.length === 0) return todos;
  }
  return [];
}

/**
 * Read the todo list out of a single tool call. The arguments carry the list a
 * `todo_write` is writing; the result carries the list the tool echoed back, so
 * both are tried and the arguments win (they are what the agent decided).
 */
export function todosFromToolCall(call: ToolCallLike): ChatTodoItem[] {
  if (!isTodoToolName(call.name)) return [];

  const fromArgs = todosFromRecord(asRecordValue(call.arguments));
  if (fromArgs.length > 0) return fromArgs;

  const fromResult = todosFromRecord(resultRecord(call) ?? asRecordValue(call.result));
  if (fromResult.length > 0) return fromResult;

  // An explicit empty write clears the list, so an empty `todos: []` argument
  // has to be honoured instead of being treated as "no data" and skipped.
  const args = asRecordValue(call.arguments);
  if (args && Array.isArray(args.todos) && args.todos.length === 0) return [];
  const result = resultRecord(call) ?? asRecordValue(call.result);
  if (result && Array.isArray(result.todos) && result.todos.length === 0) return [];

  return [];
}

function callsFromRow(row: ChatTodoMessageRow): ToolCallLike[] {
  const raw = parseMaybeJson(row.tool_calls);
  if (!Array.isArray(raw)) return [];
  return raw
    .map((entry) => asRecordValue(entry))
    .filter((entry): entry is Record<string, unknown> => entry !== null)
    .map((entry) => ({
      id: typeof entry.id === "string" ? entry.id : undefined,
      name: typeof entry.name === "string" ? entry.name : undefined,
      arguments: entry.arguments,
      result: entry.result,
      status: typeof entry.status === "string" ? entry.status : undefined,
      error: typeof entry.error === "string" ? entry.error : undefined,
    }));
}

/**
 * Fold a message page into the newest agent todo list.
 *
 * The message-list endpoint returns rows newest first, so walking the rows from
 * the oldest to the newest and keeping the last todo tool call that carried a
 * list yields the current state of the plan without a dedicated endpoint
 * (ci#1100). A message whose todo call carries no readable list (an older
 * `todo_read` result without items, say) is skipped instead of clearing the
 * snapshot.
 */
export function extractLatestTodoSnapshot(
  messages: ChatTodoMessageRow[] | undefined | null,
): ChatTodoSnapshot | null {
  if (!Array.isArray(messages) || messages.length === 0) return null;

  let snapshot: ChatTodoSnapshot | null = null;

  for (let i = messages.length - 1; i >= 0; i -= 1) {
    const row = messages[i];
    if (!row) continue;
    const calls = callsFromRow(row);
    for (const call of calls) {
      if (!isTodoToolName(call.name)) continue;
      const todos = todosFromToolCall(call);
      const args = asRecordValue(call.arguments);
      const result = resultRecord(call) ?? asRecordValue(call.result);
      const clearsList =
        (args && Array.isArray(args.todos) && args.todos.length === 0) ||
        (result && Array.isArray(result.todos) && result.todos.length === 0);
      if (todos.length === 0 && !clearsList) continue;
      snapshot = {
        todos,
        toolName: shortToolName(call.name),
        ...(row.uuid ? { messageUUID: row.uuid } : {}),
        ...(row.send_at ? { sendAt: row.send_at } : {}),
      };
    }
  }

  return snapshot;
}
