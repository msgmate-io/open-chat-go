import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

// ChatDetailsPanel imports from the @open-chat-go/ui barrel, which pulls in
// Vike via the integration surface. Stub Vike's Node-version-gated client entry
// points so the pure Node unit project can render the panel.
vi.mock("vike-react/usePageContext", () => ({ usePageContext: () => ({}) }));
vi.mock("vike/client/router", () => ({ navigate: vi.fn() }));

import {
  ChatDetailsPanel,
  CHAT_DETAILS_TABS,
  chatTodoItems,
  normalizeTodoStatus,
  normalizeToolName,
  toolTitle,
} from "./ChatDetailsPanel";

const interactionChat = {
  chat_type: "interaction",
  partner: { name: "msgmate-coder-bot" },
  status: { state: "active" },
  settings: { title: "Fix the flaky test" },
  config: {
    model: "gpt-4o",
    backend: "msgmate",
    max_tokens: 4096,
    temperature: 0.3,
    system_prompt: "You are a helpful coder.",
    tools: ["integration:signal_send_message"],
    tool_init: { signal_send_message: { recipient: "+491234" } },
  },
};

function renderPanel(
  props: Partial<React.ComponentProps<typeof ChatDetailsPanel>> = {},
) {
  return renderToStaticMarkup(
    <ChatDetailsPanel
      chat={interactionChat}
      user={{ username: "tbscode" }}
      activeTab="overview"
      onTabChange={() => undefined}
      {...props}
    />,
  );
}

// ci#749 turns the chat header details into a shared, tabbed surface instead of
// a stack of ad-hoc dialogs. These tests pin the tab list and the section
// content so the header and the panel stay in sync.
describe("ChatDetailsPanel", () => {
  it("renders every details tab with the active one selected", () => {
    const html = renderPanel({ activeTab: "tools" });

    CHAT_DETAILS_TABS.forEach((tab) => {
      expect(html).toContain(tab.label);
    });
    expect(html).toContain('role="tablist"');
    expect(html).toContain('aria-selected="true"');
    expect(html).toContain("chat-details-tab-tools");
    expect(html).toContain("chat-details-panel-tools");
  });

  it("shows the model and chat metadata in the overview tab", () => {
    const html = renderPanel();

    expect(html).toContain("gpt-4o");
    expect(html).toContain("Fix the flaky test");
    expect(html).toContain("msgmate-coder-bot");
    expect(html).toContain("tbscode");
    expect(html).toContain("You are a helpful coder.");
  });

  it("renders the tool init payload per normalized tool name", () => {
    const html = renderPanel({ activeTab: "init" });

    expect(html).toContain("Tool init");
    expect(html).toContain("signal_send_message");
    expect(html).toContain("+491234");
  });

  it("withholds tool init data in shared views", () => {
    const html = renderPanel({ activeTab: "init", isSharedView: true });

    expect(html).toContain("Tool init data is not available in shared views.");
    expect(html).not.toContain("+491234");
  });

  it("lists the chat configured tools in the tools tab", () => {
    const html = renderPanel({ activeTab: "tools" });

    expect(html).toContain("integration:signal_send_message");
    expect(html).toContain("Signal Send Message");
  });

  it("explains an empty tool init payload instead of rendering an empty block", () => {
    const html = renderPanel({
      activeTab: "init",
      chat: { ...interactionChat, config: { model: "gpt-4o" } },
    });

    expect(html).toContain("No tool init configured for this chat.");
  });

  // ci#1100: the chat details list grows a read-only "todos" tab that renders
  // the todo state the coder agent maintains for the interaction run.
  it("lists the todos tab next to the other details tabs", () => {
    expect(CHAT_DETAILS_TABS.map((tab) => tab.id)).toContain("todos");

    const html = renderPanel({ activeTab: "todos" });

    expect(html).toContain("chat-details-tab-todos");
    expect(html).toContain("chat-details-panel-todos");
  });

  it("renders the agent todo list from the interaction status", () => {
    const html = renderPanel({
      activeTab: "todos",
      chat: {
        ...interactionChat,
        status: {
          state: "active",
          todos: [
            { text: "Read the plan comment", status: "completed" },
            { text: "Add the todos tab", status: "in_progress" },
            { text: "Open the PR", status: "pending" },
            { text: "Dropped idea", status: "cancelled" },
          ],
        },
      },
    });

    expect(html).toContain("Read the plan comment");
    expect(html).toContain("Add the todos tab");
    expect(html).toContain("Open the PR");
    expect(html).toContain("Dropped idea");
    expect(html).toContain("1/4 completed");
    expect(html).toContain('data-todo-status="completed"');
    expect(html).toContain('data-todo-status="in_progress"');
    expect(html).toContain('data-todo-status="pending"');
    expect(html).toContain('data-todo-status="cancelled"');
    expect(html).toContain("In progress");
  });

  it("explains an absent todo list instead of rendering an empty block", () => {
    const html = renderPanel({ activeTab: "todos" });

    expect(html).toContain("This interaction has no todo list yet.");
    expect(html).not.toContain('data-todo-status=');
  });
});

describe("chat todo helpers", () => {
  it("normalizes raw todo statuses into the rendered states", () => {
    expect(normalizeTodoStatus("in-progress")).toBe("in_progress");
    expect(normalizeTodoStatus("Done")).toBe("completed");
    expect(normalizeTodoStatus("canceled")).toBe("cancelled");
    expect(normalizeTodoStatus(undefined)).toBe("pending");
    expect(normalizeTodoStatus("whatever")).toBe("pending");
  });

  it("drops text-less todo entries and tolerates a missing list", () => {
    expect(chatTodoItems(undefined)).toEqual([]);
    expect(chatTodoItems("nope")).toEqual([]);
    expect(
      chatTodoItems([
        { text: " keep me ", status: "pending" },
        { text: "   ", status: "pending" },
        { status: "pending" },
        { content: "content fallback", status: "completed" },
        null,
      ]),
    ).toEqual([
      { text: "keep me", status: "pending" },
      { text: "content fallback", status: "completed" },
    ]);
  });
});

describe("tool helpers", () => {
  it("normalizes namespaced tool slugs to their short name", () => {
    expect(normalizeToolName("integration:signal_send_message")).toBe("signal_send_message");
    expect(normalizeToolName("read_file")).toBe("read_file");
  });

  it("prettifies tool slugs into readable titles", () => {
    expect(toolTitle("integration:signal_send_message")).toBe("Signal Send Message");
    expect(toolTitle({ name: "read_file", function_name: "read_file" })).toBe("Read File");
  });
});
