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
  status: { state: "idle" },
};

// The coder integration floats its workspace Shell and Open-Chat TUI launchers
// into the shared header via this slot (ci#466). These tests pin the slot
// contract: provided controls render as a compact, right-aligned row, and no
// empty row is drawn when an integration supplies nothing.
describe("InteractionChatInfo headerActions slot", () => {
  it("renders supplied header actions in a right-aligned row", () => {
    const html = renderToStaticMarkup(
      <InteractionChatInfo
        chat={interactionChat}
        headerActions={
          <>
            <button type="button">Shell</button>
            <button type="button">TUI</button>
          </>
        }
      />,
    );

    expect(html).toContain("justify-end");
    const slot = html.slice(html.indexOf("justify-end"));
    expect(slot).toContain("Shell");
    expect(slot).toContain("TUI");
    // The row uses the compact flex layout the header expects.
    expect(html).toContain("flex flex-wrap items-center justify-end gap-1");
  });

  it("does not render the header actions row when no actions are supplied", () => {
    const html = renderToStaticMarkup(<InteractionChatInfo chat={interactionChat} />);

    expect(html).not.toContain("justify-end");
  });

  it("renders nothing for a non-interaction chat the user cannot inspect", () => {
    const html = renderToStaticMarkup(
      <InteractionChatInfo
        chat={{ ...interactionChat, chat_type: "chat" }}
        headerActions={<button type="button">Shell</button>}
      />,
    );

    expect(html).toBe("");
  });
});

// The desktop header used to draw the model indicator as its own oversized row;
// it is now merged inline with the chat title as a compact chip (ci#488).
describe("InteractionChatInfo merged model chip", () => {
  it("renders the model chip inline with the chat title", () => {
    const html = renderToStaticMarkup(<InteractionChatInfo chat={interactionChat} />);

    expect(html).toContain("gpt-4o");
    // The title and model chip share one flex row.
    expect(html).toContain("flex min-w-0 items-center gap-2");
  });

  it("hides the model chip when an integration renders it in its own card", () => {
    const html = renderToStaticMarkup(<InteractionChatInfo chat={interactionChat} hideModelDisplay />);

    expect(html).not.toContain("gpt-4o");
  });
});
