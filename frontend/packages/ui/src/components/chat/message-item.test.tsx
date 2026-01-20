import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { BotMessageShell, MessageRow, UserMessageShell } from "./message-item";

describe("chat message shells on small screens", () => {
  it("uses tighter row gutters below sm and the roomier desktop spacing at md", () => {
    const html = renderToStaticMarkup(
      <MessageRow>
        <span>hi</span>
      </MessageRow>,
    );
    const tokens = html.split(/["\s]+/);
    expect(tokens).toContain("gap-2");
    expect(tokens).toContain("px-2");
    expect(tokens).toContain("sm:gap-3");
    expect(tokens).toContain("md:px-4");
  });

  it("gives outgoing bubbles more width on small screens", () => {
    const html = renderToStaticMarkup(
      <UserMessageShell senderLabel="You" align="end">
        hi
      </UserMessageShell>,
    );
    expect(html).toContain("max-w-[92%]");
    expect(html).toContain("sm:max-w-[85%]");
  });

  it("keeps bot bubbles full-width (no fixed max width) on small screens", () => {
    const html = renderToStaticMarkup(
      <BotMessageShell senderLabel="Bot">
        hi
      </BotMessageShell>,
    );
    expect(html).toContain("min-w-0");
    expect(html).toContain("flex-1");
    expect(html).not.toContain("max-w-[85%]");
  });
});
