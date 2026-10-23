import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

// ChatDetailsPanel imports from the @open-chat-go/ui barrel, which pulls in
// Vike via the integration surface. Stub Vike's Node-version-gated client entry
// points so the pure Node unit project can render the panel.
vi.mock("vike-react/usePageContext", () => ({ usePageContext: () => ({}) }));
vi.mock("vike/client/router", () => ({ navigate: vi.fn() }));

import { ChatDetailsPanel, CHAT_DETAILS_TABS, normalizeToolName, toolTitle } from "./ChatDetailsPanel";

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
