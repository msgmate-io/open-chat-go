import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { UserMessageItem } from "./MessageItem";
import { QUEUED_REPLY_HINT_LABEL } from "../../../lib/delivery-mode";

const chat = { partner: { name: "Bot", is_bot: true } };

function render(message: Record<string, unknown>) {
  return renderToStaticMarkup(
    <UserMessageItem
      message={message as never}
      chat={chat as never}
      selfIsSender
    />,
  );
}

// ci#656: a message accepted into a chat whose bot turn is still running is
// persisted right away but only consumed once that turn ends. The transcript
// has to say so, otherwise the sender reads silence as "ignored".
describe("UserMessageItem queued delivery hint", () => {
  it("labels a message the server parked behind a live turn", () => {
    const html = render({
      uuid: "m1",
      text: "one more thing",
      meta_data: { queue_state: "queued" },
    });
    expect(html).toContain('data-testid="queued-reply-hint"');
    expect(html).toContain(QUEUED_REPLY_HINT_LABEL);
  });

  it("labels an optimistic message whose uuid is still a queued placeholder", () => {
    const html = render({ uuid: "queued_17_ab12", text: "and this" });
    expect(html).toContain('data-testid="queued-reply-hint"');
  });

  it("stays quiet for a normal message", () => {
    const html = render({ uuid: "m3", text: "hello" });
    expect(html).not.toContain('data-testid="queued-reply-hint"');
  });

  it("stays quiet once the marker is cleared after delivery", () => {
    const html = render({
      uuid: "m4",
      text: "hello",
      meta_data: { queue_state: "delivered" },
    });
    expect(html).not.toContain('data-testid="queued-reply-hint"');
  });
});
