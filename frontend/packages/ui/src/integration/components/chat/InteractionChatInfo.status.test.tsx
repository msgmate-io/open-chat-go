import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

// InteractionChatInfo imports from the @open-chat-go/ui barrel, which pulls in
// Vike via the integration surface. Stub Vike's Node-version-gated client entry
// points so the pure Node unit project can render the header.
vi.mock("vike-react/usePageContext", () => ({ usePageContext: () => ({}) }));
vi.mock("vike/client/router", () => ({ navigate: vi.fn() }));

import { InteractionChatInfo } from "./InteractionChatInfo";

const interactionChat = {
  chat_type: "interaction",
  partner: { name: "msgmate-coder-bot" },
  config: { model: "gpt-4o" },
};

function renderStatus(status: unknown) {
  return renderToStaticMarkup(
    <InteractionChatInfo chat={{ ...interactionChat, status }} />,
  );
}

// The header used to show a static "Active" badge. It now reflects the live
// interaction state (ci#488) so the header and the compact coder widget agree on
// what the chat is doing.
describe("InteractionChatInfo live chat-state badge", () => {
  it("shows Active while the interaction is running", () => {
    expect(renderStatus({ is_active: true })).toContain("Active");
    expect(renderStatus({ state: "active" })).toContain("Active");
  });

  it("shows Needs action while a tool call awaits confirmation", () => {
    expect(renderStatus({ state: "needs_confirmation" })).toContain("Needs action");
  });

  it("shows Finished, Failed and Idle for the terminal states", () => {
    expect(renderStatus({ state: "finished" })).toContain("Finished");
    expect(renderStatus({ state: "failed" })).toContain("Failed");
    expect(renderStatus({ state: "idle" })).toContain("Idle");
  });

  it("falls back to Shared for a shared view without a status", () => {
    const html = renderToStaticMarkup(
      <InteractionChatInfo chat={interactionChat} isSharedView />,
    );
    expect(html).toContain("Shared");
  });
});
