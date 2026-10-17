import { describe, expect, it } from "vitest";
import {
  CHAT_STATE_TONE_DOT_CLASS,
  resolveChatStateIndicator,
} from "./chat-state";

// The compact chat-state chip next to the coder label (ci#488) and the core
// header badge must agree on what the interaction is doing. This pins the
// label/tone mapping the coder widget renders.
describe("resolveChatStateIndicator", () => {
  it("returns null for a missing or empty state", () => {
    expect(resolveChatStateIndicator()).toBeNull();
    expect(resolveChatStateIndicator(null)).toBeNull();
    expect(resolveChatStateIndicator("")).toBeNull();
    expect(resolveChatStateIndicator("   ")).toBeNull();
  });

  it("maps the known interaction states to their compact chip", () => {
    expect(resolveChatStateIndicator("active")).toEqual({ label: "working", tone: "active" });
    expect(resolveChatStateIndicator("finished")).toEqual({ label: "done", tone: "success" });
    expect(resolveChatStateIndicator("failed")).toEqual({ label: "failed", tone: "danger" });
    expect(resolveChatStateIndicator("needs_confirmation")).toEqual({
      label: "needs confirmation",
      tone: "attention",
    });
    expect(resolveChatStateIndicator("idle")).toEqual({ label: "idle", tone: "muted" });
  });

  it("humanises an unknown state instead of dropping it", () => {
    expect(resolveChatStateIndicator("awaiting_review")).toEqual({
      label: "awaiting review",
      tone: "muted",
    });
  });

  it("provides a dot class for every tone", () => {
    expect(CHAT_STATE_TONE_DOT_CLASS.active).toContain("animate-pulse");
    expect(CHAT_STATE_TONE_DOT_CLASS.success).toContain("emerald");
    expect(CHAT_STATE_TONE_DOT_CLASS.danger).toContain("rose");
    expect(CHAT_STATE_TONE_DOT_CLASS.attention).toContain("animate-pulse");
    expect(CHAT_STATE_TONE_DOT_CLASS.muted).toContain("muted-foreground");
  });
});
