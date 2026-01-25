import { describe, expect, it } from "vitest";
import {
  registerChatUIExtension,
  resolveChatUIExtension,
  resolveChatUIExtensionForChat,
} from "./registry";
import type { ChatUIExtensionMatchContext } from "./types";

// The native coder's chats never set a chat_backend key, so its extension is
// matched by predicate on the chat partner name. The bot's username is
// hyphenated ("msgmate-coder-bot") but the underscore form must keep working
// too. This mirrors the predicate in
// open-chat-go-msgmate-coder/frontend/pages/msgmate-coder-extension.tsx so a
// regression in the identity contract is caught here (ci#478, refs ci#394).
function matchMsgmateCoder(ctx: ChatUIExtensionMatchContext): boolean {
  const chatBackend = (ctx.chatBackend || "").trim().toLowerCase();
  if (chatBackend === "msgmate_coder") {
    return true;
  }
  const partnerName = (ctx.chat?.partner?.name || "").trim().toLowerCase();
  if (partnerName === "msgmate_coder_bot" || partnerName === "msgmate-coder-bot") {
    return true;
  }
  return (ctx.toolCalls || []).some((call) => (call?.name || "").startsWith("coder_"));
}

registerChatUIExtension({ chatBackend: "msgmate_coder", match: matchMsgmateCoder });

describe("chat UI extension coder identity matching", () => {
  it("resolves by the explicit msgmate_coder chat backend", () => {
    expect(resolveChatUIExtension("msgmate_coder")).not.toBeNull();
    expect(resolveChatUIExtensionForChat({ chatBackend: "msgmate_coder" })?.chatBackend).toBe("msgmate_coder");
  });

  it("matches the hyphenated coder bot partner name", () => {
    const ext = resolveChatUIExtensionForChat({ chat: { partner: { name: "msgmate-coder-bot" } } });
    expect(ext?.chatBackend).toBe("msgmate_coder");
  });

  it("matches the underscored coder bot partner name", () => {
    const ext = resolveChatUIExtensionForChat({ chat: { partner: { name: "msgmate_coder_bot" } } });
    expect(ext?.chatBackend).toBe("msgmate_coder");
  });

  it("matches case-insensitively and ignores surrounding whitespace", () => {
    const ext = resolveChatUIExtensionForChat({ chat: { partner: { name: "  MsgMate-Coder-Bot " } } });
    expect(ext?.chatBackend).toBe("msgmate_coder");
  });

  it("matches coder_* tool calls for a chat without a coder partner name", () => {
    const ext = resolveChatUIExtensionForChat({
      chat: { partner: { name: "some-bot" } },
      toolCalls: [{ name: "coder_read" }],
    });
    expect(ext?.chatBackend).toBe("msgmate_coder");
  });

  it("does not match unrelated bots or non-coder tool calls", () => {
    expect(resolveChatUIExtensionForChat({ chat: { partner: { name: "some-other-bot" } } })).toBeNull();
    expect(
      resolveChatUIExtensionForChat({
        chat: { partner: { name: "some-other-bot" } },
        toolCalls: [{ name: "shell_exec" }],
      }),
    ).toBeNull();
  });
});
