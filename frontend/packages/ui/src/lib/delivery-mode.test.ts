import { describe, expect, it } from "vitest";
import {
  BOT_REPLY_DELIVERY_MODES,
  QUEUED_REPLY_HINT_LABEL,
  SEND_NOW_LABEL,
  buildSendMessageBody,
  isBotReplyDeliveryMode,
  queuedReplyHintLabel,
  shouldOfferSendNow,
} from "./delivery-mode";

describe("isBotReplyDeliveryMode", () => {
  it("accepts exactly the modes the server validates", () => {
    expect(BOT_REPLY_DELIVERY_MODES).toEqual(["queued", "interrupt"]);
    expect(isBotReplyDeliveryMode("queued")).toBe(true);
    expect(isBotReplyDeliveryMode("interrupt")).toBe(true);
  });

  it("rejects anything else, including non-strings", () => {
    expect(isBotReplyDeliveryMode("immediate")).toBe(false);
    expect(isBotReplyDeliveryMode("")).toBe(false);
    expect(isBotReplyDeliveryMode(undefined)).toBe(false);
    expect(isBotReplyDeliveryMode(null)).toBe(false);
    expect(isBotReplyDeliveryMode(1)).toBe(false);
  });
});

describe("buildSendMessageBody", () => {
  it("keeps the legacy payload shape when nothing is sendable", () => {
    expect(buildSendMessageBody({})).toEqual({});
    expect(buildSendMessageBody({ text: "" })).toEqual({});
  });

  it("omits empty attachments and empty toolInit", () => {
    expect(
      buildSendMessageBody({ text: "hi", attachments: [], toolInit: {} }),
    ).toEqual({
      text: "hi",
    });
  });

  it("carries text, attachments and toolInit when present", () => {
    const attachments = [{ file_id: "f1", display_name: "a.txt" }];
    const body = buildSendMessageBody({
      text: "hi",
      attachments,
      toolInit: { web: true },
    });
    expect(body).toEqual({ text: "hi", attachments, tool_init: { web: true } });
  });

  it("only sets delivery_mode for a valid mode", () => {
    expect(buildSendMessageBody({ text: "hi" }).delivery_mode).toBeUndefined();
    expect(
      buildSendMessageBody({ text: "hi", deliveryMode: "queued" })
        .delivery_mode,
    ).toBe("queued");
    expect(
      buildSendMessageBody({ text: "hi", deliveryMode: "interrupt" })
        .delivery_mode,
    ).toBe("interrupt");
    // Unknown values are dropped rather than forwarded, so the server never
    // answers 400 Invalid delivery_mode for a stale client.
    expect(buildSendMessageBody({ text: "hi", deliveryMode: "nope" })).toEqual({
      text: "hi",
    });
  });
});

describe("shouldOfferSendNow", () => {
  it("needs both a live turn and sendable content", () => {
    expect(
      shouldOfferSendNow({ isBotResponding: true, hasSendableContent: true }),
    ).toBe(true);
    expect(
      shouldOfferSendNow({ isBotResponding: false, hasSendableContent: true }),
    ).toBe(false);
    expect(
      shouldOfferSendNow({ isBotResponding: true, hasSendableContent: false }),
    ).toBe(false);
  });
});

describe("queuedReplyHintLabel", () => {
  it("labels a message the transcript treats as pending", () => {
    expect(queuedReplyHintLabel(true)).toBe(QUEUED_REPLY_HINT_LABEL);
  });

  it("stays quiet for delivered messages", () => {
    expect(queuedReplyHintLabel(false)).toBeNull();
  });
});

describe("labels", () => {
  it("exposes non-empty user facing strings", () => {
    expect(SEND_NOW_LABEL.trim().length).toBeGreaterThan(0);
    expect(QUEUED_REPLY_HINT_LABEL.trim().length).toBeGreaterThan(0);
  });
});
