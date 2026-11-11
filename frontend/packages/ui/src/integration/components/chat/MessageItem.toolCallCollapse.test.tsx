import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ToolCallCard } from "./MessageItem";
import type { ToolCallLike } from "./toolCallSummary";

type MessageToolCall = ToolCallLike;

/*
 * ci#1031 item 6: a *finished* tool result renders collapsed once a newer
 * message/step supersedes it, so a long coding turn does not grow the
 * transcript without bound. Failures/errors and still-running calls always
 * stay open, and a collapsed card keeps an explicit expand affordance.
 */
function render(call: Partial<MessageToolCall>, props: { isSuperseded?: boolean; terminal?: boolean } = {}) {
  return renderToStaticMarkup(
    <ToolCallCard
      messageUUID="msg-1"
      toolCall={{ name: "shell", ...call } as MessageToolCall}
      index={0}
      isSuperseded={props.isSuperseded}
      terminal={props.terminal}
    />,
  );
}

function expanded(html: string): boolean {
  return html.includes('aria-expanded="true"');
}

describe("ToolCallCard collapse policy (ci#1031)", () => {
  it("keeps a running call expanded", () => {
    expect(expanded(render({ status: "ongoing" }, { isSuperseded: true }))).toBe(true);
  });

  it("keeps a failed call expanded even when superseded", () => {
    expect(expanded(render({ status: "failed" }, { isSuperseded: true }))).toBe(true);
    expect(expanded(render({ error: "boom" }, { isSuperseded: true }))).toBe(true);
  });

  it("keeps a succeeded call expanded while it is the newest step", () => {
    expect(expanded(render({ status: "succeeded", result: "ok" }, { isSuperseded: false }))).toBe(true);
  });

  it("collapses a superseded succeeded call but keeps an expand affordance", () => {
    const html = render({ status: "succeeded", result: "ok" }, { isSuperseded: true });
    expect(expanded(html)).toBe(false);
    // the trigger is still there, labelled for expanding, so the result is one
    // click away instead of gone
    expect(html).toContain('data-testid="tool-call-card-trigger"');
    expect(html).toContain("expand");
    expect(html).not.toContain("Parameters");
  });

  it("keys the card by the backend tool_call id, not by position", () => {
    const first = render({ id: "call-a", status: "succeeded", result: "ok" }, { isSuperseded: true });
    const second = render({ id: "call-b", status: "succeeded", result: "ok" }, { isSuperseded: true });
    expect(first).toContain("tool-call-card-call-a");
    expect(second).toContain("tool-call-card-call-b");
  });

  it("falls back to the position when the backend sent no call id", () => {
    const html = render({ status: "succeeded", result: "ok" }, { isSuperseded: true });
    expect(html).toContain("tool-call-card-msg-1-tool-call-0");
  });
});
