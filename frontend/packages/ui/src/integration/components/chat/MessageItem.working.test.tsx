import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { BotMessageItem } from "./MessageItem";

const chat = { partner: { name: "Bot", is_bot: true } };

function render(message: Record<string, unknown>) {
  return renderToStaticMarkup(
    <BotMessageItem message={message as never} chat={chat as never} />,
  );
}

describe("BotMessageItem running affordances", () => {
  it("shows a working footer while an in-flight turn has content", () => {
    const html = render({
      uuid: "m1",
      text: "working on it",
      meta_data: { finished: false },
    });
    expect(html).toContain("Working");
    expect(html).toContain("animate-spin");
  });

  it("does not show the working footer once the turn is finished", () => {
    const html = render({
      uuid: "m2",
      text: "all done",
      meta_data: { finished: true },
    });
    expect(html).not.toContain("Working");
  });

  it("renders a per-tool summary in the core tool-call group", () => {
    const html = render({
      uuid: "m3",
      text: "running a command",
      meta_data: { finished: false },
      tool_calls: [
        {
          name: "bash",
          arguments: { command: "npm run build" },
          result: { exit_code: 0 },
          status: "succeeded",
        },
      ],
    });
    expect(html).toContain("Shell");
    expect(html).toContain("npm run build");
    expect(html).toContain("exit 0");
  });
});
