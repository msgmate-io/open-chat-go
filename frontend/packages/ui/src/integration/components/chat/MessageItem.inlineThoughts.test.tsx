import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { registerChatUIExtension } from "./chat-ui/registry";
import { BotMessageItem } from "./MessageItem";

// ci#852: once the backend records the turn's reasoning in `meta_data.timeline`,
// an integration body that interleaves those blocks with its own tool-call rows
// would render the turn's thoughts twice — its own rows *and* the core's grouped
// thoughts block. `inlineThoughts` hands the thoughts over to the body.
function ExtensionBody() {
  return <div data-testid="ext-message-body">EXT</div>;
}

// A body that does not own the thoughts: the core keeps rendering them.
registerChatUIExtension({
  chatBackend: "grouped_thoughts_test",
  MessageBody: ExtensionBody,
});

// A body that renders the thoughts itself and therefore opts in.
registerChatUIExtension({
  chatBackend: "inline_thoughts_test",
  MessageBody: ExtensionBody,
  inlineThoughts: true,
});

const REASONING = "I should read the migration first.";

function render(chatBackend: string, message: Record<string, unknown>) {
  return renderToStaticMarkup(
    <BotMessageItem
      message={message as never}
      chat={{ partner: { name: "Bot", is_bot: true }, config: { chat_backend: chatBackend } } as never}
    />,
  );
}

const liveTurn = {
  uuid: "m-live",
  text: "checking the migration",
  thoughts: [REASONING],
  meta_data: {
    finished: false,
    timeline: [{ kind: "thought", text: REASONING, streaming: true }],
  },
};

const finishedTurn = {
  uuid: "m-done",
  text: "done",
  thoughts: [REASONING],
  meta_data: {
    finished: true,
    timeline: [{ kind: "thought", text: REASONING }],
  },
};

describe("BotMessageItem inlineThoughts contract (ci#852)", () => {
  it("keeps the core's live thoughts block for a body without the flag", () => {
    const html = render("grouped_thoughts_test", liveTurn);
    expect(html).toContain('data-testid="ext-message-body"');
    expect(html).toContain(REASONING);
  });

  it("suppresses the live thoughts block when the body opts in", () => {
    const html = render("inline_thoughts_test", liveTurn);
    expect(html).toContain('data-testid="ext-message-body"');
    expect(html).not.toContain(REASONING);
  });

  it("keeps the core's finished thoughts block for a body without the flag", () => {
    const html = render("grouped_thoughts_test", finishedTurn);
    expect(html).toContain(REASONING);
  });

  it("suppresses the finished thoughts block when the body opts in", () => {
    const html = render("inline_thoughts_test", finishedTurn);
    expect(html).toContain('data-testid="ext-message-body"');
    expect(html).not.toContain(REASONING);
  });

  it("ignores the flag without a MessageBody (core keeps the thoughts)", () => {
    registerChatUIExtension({ chatBackend: "no_body_test", inlineThoughts: true });
    const html = render("no_body_test", finishedTurn);
    expect(html).not.toContain('data-testid="ext-message-body"');
    expect(html).toContain(REASONING);
  });
});
