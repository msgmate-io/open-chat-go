import { describe, expect, it } from "vitest";

import {
  extractLatestTodoSnapshot,
  isTodoToolName,
  isTodoWriteToolName,
  normalizeTodoStatus,
  todoCounts,
  todoProgressLabel,
  todoStatusLabel,
  todosFromToolCall,
} from "./chatTodos";

// ci#1100 asks for a `todos` tab in the chat details surface that shows the
// agent's current todo list. The list is only ever recorded in the coder todo
// tool calls, so this module folds the persisted tool calls of a chat into the
// newest snapshot; these tests pin the folding rules and the status mapping.
describe("chatTodos", () => {
  describe("tool name matching", () => {
    it("recognizes the coder todo tools across namespacing and casing", () => {
      expect(isTodoToolName("todo_write")).toBe(true);
      expect(isTodoToolName("coder_todo_write")).toBe(true);
      expect(isTodoToolName("TodoWrite")).toBe(true);
      expect(isTodoToolName("todo_read")).toBe(true);
      expect(isTodoToolName("todos")).toBe(true);
      expect(isTodoToolName("coder_shell")).toBe(false);
      expect(isTodoToolName(undefined)).toBe(false);
    });

    it("separates the writing tool from the reading tool", () => {
      expect(isTodoWriteToolName("coder_todo_write")).toBe(true);
      expect(isTodoWriteToolName("todo_read")).toBe(false);
    });
  });

  describe("normalizeTodoStatus", () => {
    it("maps the common status spellings onto the four rendered states", () => {
      expect(normalizeTodoStatus("in_progress")).toBe("in_progress");
      expect(normalizeTodoStatus("In Progress")).toBe("in_progress");
      expect(normalizeTodoStatus("running")).toBe("in_progress");
      expect(normalizeTodoStatus("done")).toBe("completed");
      expect(normalizeTodoStatus("canceled")).toBe("cancelled");
      expect(normalizeTodoStatus("open")).toBe("pending");
    });

    it("defaults unknown and missing statuses to pending", () => {
      expect(normalizeTodoStatus("who knows")).toBe("pending");
      expect(normalizeTodoStatus(undefined)).toBe("pending");
      expect(normalizeTodoStatus(7)).toBe("pending");
    });

    it("labels every status", () => {
      expect(todoStatusLabel("pending")).toBe("Pending");
      expect(todoStatusLabel("in_progress")).toBe("In progress");
      expect(todoStatusLabel("completed")).toBe("Completed");
      expect(todoStatusLabel("cancelled")).toBe("Cancelled");
    });
  });

  describe("todosFromToolCall", () => {
    it("reads the list from the tool arguments", () => {
      const todos = todosFromToolCall({
        name: "coder_todo_write",
        arguments: {
          todos: [
            { content: "Read the ticket", status: "completed" },
            { content: "Implement the tab", status: "in_progress", activeForm: "Implementing the tab" },
          ],
        },
      });

      expect(todos).toHaveLength(2);
      expect(todos[0].status).toBe("completed");
      expect(todos[1].activeForm).toBe("Implementing the tab");
    });

    it("decodes JSON string arguments as the bridge marshals them", () => {
      const todos = todosFromToolCall({
        name: "todo_write",
        arguments: JSON.stringify({ todos: [{ content: "Ship it", status: "pending" }] }),
      });

      expect(todos).toEqual([{ content: "Ship it", status: "pending" }]);
    });

    it("falls back to the tool result when the arguments carry no list", () => {
      const todos = todosFromToolCall({
        name: "todo_read",
        arguments: {},
        result: JSON.stringify({ todos: [{ task: "Only in the result", status: "pending" }] }),
      });

      expect(todos).toHaveLength(1);
      expect(todos[0].content).toBe("Only in the result");
    });

    it("accepts plain string entries and alternate content keys", () => {
      const todos = todosFromToolCall({
        name: "todos",
        arguments: { todos: ["bare string", { title: "titled item" }] },
      });

      expect(todos.map((todo) => todo.content)).toEqual(["bare string", "titled item"]);
    });

    it("keeps a stable id when the tool emits one", () => {
      const todos = todosFromToolCall({
        name: "todo_write",
        arguments: { todos: [{ id: "t-1", content: "Has an id" }] },
      });

      expect(todos[0].id).toBe("t-1");
    });

    it("ignores calls that are not todo tools", () => {
      expect(todosFromToolCall({ name: "shell", arguments: { todos: ["nope"] } })).toEqual([]);
    });
  });

  describe("extractLatestTodoSnapshot", () => {
    const olderWrite = {
      uuid: "msg-older",
      send_at: "2026-01-01T10:00:00Z",
      tool_calls: [
        {
          id: "call-1",
          name: "coder_todo_write",
          arguments: { todos: [{ content: "Older item", status: "pending" }] },
        },
      ],
    };

    const newerWrite = {
      uuid: "msg-newer",
      send_at: "2026-01-01T11:00:00Z",
      tool_calls: JSON.stringify([
        {
          id: "call-2",
          name: "coder_todo_write",
          arguments: {
            todos: [
              { content: "Done item", status: "completed" },
              { content: "Active item", status: "in_progress" },
            ],
          },
        },
      ]),
    };

    it("returns null without any todo tool call", () => {
      expect(extractLatestTodoSnapshot(undefined)).toBeNull();
      expect(extractLatestTodoSnapshot([])).toBeNull();
      expect(
        extractLatestTodoSnapshot([{ uuid: "m", tool_calls: [{ name: "shell", arguments: {} }] }]),
      ).toBeNull();
    });

    it("prefers the newest todo call because the endpoint lists newest first", () => {
      const snapshot = extractLatestTodoSnapshot([newerWrite, olderWrite]);

      expect(snapshot?.messageUUID).toBe("msg-newer");
      expect(snapshot?.toolName).toBe("todo_write");
      expect(snapshot?.sendAt).toBe("2026-01-01T11:00:00Z");
      expect(snapshot?.todos.map((todo) => todo.content)).toEqual(["Done item", "Active item"]);
    });

    it("skips a later todo call that carries no readable list", () => {
      const emptyRead = {
        uuid: "msg-read",
        tool_calls: [{ name: "coder_todo_read", arguments: {}, result: { todos: [] } }],
      };

      // An empty result clears nothing: only an explicit empty write would, so
      // the previous list stays visible instead of the tab going blank.
      expect(extractLatestTodoSnapshot([emptyRead, olderWrite])?.messageUUID).toBe("msg-older");
    });

    it("honours an explicit empty write as clearing the list", () => {
      const clear = {
        uuid: "msg-clear",
        send_at: "2026-01-01T12:00:00Z",
        tool_calls: [{ name: "coder_todo_write", arguments: { todos: [] } }],
      };

      const snapshot = extractLatestTodoSnapshot([clear, olderWrite]);
      expect(snapshot).not.toBeNull();
      expect(snapshot?.todos).toEqual([]);
      expect(snapshot?.messageUUID).toBe("msg-clear");
    });

    it("survives malformed tool_calls payloads", () => {
      const snapshot = extractLatestTodoSnapshot([
        { uuid: "msg-broken", tool_calls: "{not json" },
        olderWrite,
      ]);

      expect(snapshot?.messageUUID).toBe("msg-older");
    });
  });

  describe("counts and progress", () => {
    const todos = [
      { content: "a", status: "pending" as const },
      { content: "b", status: "in_progress" as const },
      { content: "c", status: "completed" as const },
      { content: "d", status: "completed" as const },
      { content: "e", status: "cancelled" as const },
    ];

    it("counts every status", () => {
      expect(todoCounts(todos)).toEqual({
        total: 5,
        pending: 1,
        inProgress: 1,
        completed: 2,
        cancelled: 1,
      });
    });

    it("renders a short progress line", () => {
      expect(todoProgressLabel(todos)).toBe("2 of 5 done");
      expect(todoProgressLabel([])).toBe("no todos");
    });
  });
});
