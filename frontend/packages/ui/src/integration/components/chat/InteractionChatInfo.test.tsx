import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

// InteractionChatInfo imports from the @open-chat-go/ui barrel, which pulls in
// Vike via the integration surface. Stub Vike's Node-version-gated client entry
// points so the pure Node unit project can render the header.
vi.mock("vike-react/usePageContext", () => ({ usePageContext: () => ({}) }));
vi.mock("vike/client/router", () => ({ navigate: vi.fn() }));

import {
  ChatDetailsDialog,
  ChatDetailsDialogHeader,
  InteractionChatInfo,
} from "./InteractionChatInfo";
import { Dialog } from "../../../components/dialog";

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
    // ci#831: desktop keeps the controls inline in the header row (md:flex)
    // while small screens fall back to the wrapped row below (md:hidden).
    expect(html).toContain("items-center justify-end gap-1");
    expect(html).toContain("md:flex");
    expect(html).toContain("md:hidden");
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

// ci#788 moves the chat details out of the header into their own modal: the
// header must stay compact (no inline tabbed panel, no expansion toggle) and
// the details surface must render inside a centered dialog.
describe("InteractionChatInfo chat details modal", () => {
  it("keeps the details panel out of the header until the modal is opened", () => {
    const html = renderToStaticMarkup(<InteractionChatInfo chat={interactionChat} />);

    // The tabbed surface only exists inside the (closed) modal.
    expect(html).not.toContain('role="tablist"');
    expect(html).not.toContain("chat-details-panel-overview");
  });

  it("exposes a dialog opener instead of an in-place expansion toggle", () => {
    const html = renderToStaticMarkup(<InteractionChatInfo chat={interactionChat} />);

    expect(html).toContain('aria-haspopup="dialog"');
    expect(html).toContain(`aria-controls="interaction-chat-details"`);
    expect(html).not.toContain('aria-expanded="true"');
  });

  it("renders the modal header with its title and description", () => {
    const html = renderToStaticMarkup(
      <Dialog open>
        <ChatDetailsDialogHeader />
      </Dialog>,
    );

    expect(html).toContain("Chat details");
    expect(html).toContain("Overview of this chat");
  });

  it("describes the shared view as read-only", () => {
    const html = renderToStaticMarkup(
      <Dialog open>
        <ChatDetailsDialogHeader isSharedView />
      </Dialog>,
    );

    expect(html).toContain("Read-only overview of this shared chat.");
  });

  it("renders nothing when there is no chat to describe", () => {
    const html = renderToStaticMarkup(
      <ChatDetailsDialog
        open
        onOpenChange={() => undefined}
        chat={null}
        activeTab="overview"
        onTabChange={() => undefined}
      />,
    );

    expect(html).toBe("");
  });

  // ci#831: the details modal was too small on desktop, forcing a scrollbar per
  // section. It now grows into a taller/wider panel on large screens while
  // keeping the small-screen sheet behaviour.
  it("grows into a taller, wider panel on desktop", () => {
    const html = renderToStaticMarkup(
      <ChatDetailsDialog
        open
        onOpenChange={() => undefined}
        chat={interactionChat}
        activeTab="overview"
        onTabChange={() => undefined}
      />,
    );

    expect(html).toContain("max-h-[90dvh]");
    expect(html).toContain("sm:max-w-[640px]");
    expect(html).toContain("lg:max-w-[80rem]");
    expect(html).toContain("lg:h-[min(88dvh,52rem)]");
    // One shared scroll region instead of nested scrollbars.
    expect(html).toContain("min-h-0 flex-1 overflow-y-auto");
  });
});
