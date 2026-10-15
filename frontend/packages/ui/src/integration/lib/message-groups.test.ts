import { describe, expect, it } from "vitest";
import { buildMessageGroups, type GroupableMessage } from "./message-groups";

function user(uuid: string, overrides: Partial<GroupableMessage> = {}): GroupableMessage {
  return { uuid, sender_uuid: "user-1", sender_is_automated: false, data_type: "text", ...overrides };
}

function bot(uuid: string, overrides: Partial<GroupableMessage> = {}): GroupableMessage {
  return { uuid, sender_uuid: "bot-1", sender_is_automated: true, data_type: "text", ...overrides };
}

describe("buildMessageGroups", () => {
  it("returns single nodes and tracks rerun sources when grouping is off", () => {
    const nodes = buildMessageGroups([user("u1"), bot("b1"), bot("b2")]);

    expect(nodes.map((node) => node.kind)).toEqual(["single", "single", "single"]);
    expect(nodes[0]).toMatchObject({ kind: "single", key: "msg_u1" });
    expect(nodes[1]).toMatchObject({ kind: "single", rerunSourceMessageUUID: "u1" });
    expect(nodes[2]).toMatchObject({ kind: "single", rerunSourceMessageUUID: "u1" });
  });

  it("groups consecutive assistant messages from the same sender", () => {
    const nodes = buildMessageGroups([user("u1"), bot("b1"), bot("b2"), bot("b3")], {
      groupConsecutive: true,
    });

    expect(nodes).toHaveLength(2);
    expect(nodes[0]).toMatchObject({ kind: "single", message: { uuid: "u1" } });
    expect(nodes[1].kind).toBe("group");
    if (nodes[1].kind === "group") {
      expect(nodes[1].key).toBe("group_b1");
      expect(nodes[1].messages.map((message) => message.uuid)).toEqual(["b1", "b2", "b3"]);
      expect(nodes[1].rerunSourceMessageUUID).toBe("u1");
    }
  });

  it("does not group across a user message", () => {
    const nodes = buildMessageGroups([bot("b1"), user("u1"), bot("b2")], {
      groupConsecutive: true,
    });

    expect(nodes.map((node) => node.kind)).toEqual(["single", "single", "single"]);
    expect(nodes[2]).toMatchObject({ rerunSourceMessageUUID: "u1" });
  });

  it("does not group assistant messages from different senders", () => {
    const nodes = buildMessageGroups(
      [bot("b1"), bot("b2", { sender_uuid: "bot-2" }), bot("b3", { sender_uuid: "bot-2" })],
      { groupConsecutive: true },
    );

    expect(nodes.map((node) => node.kind)).toEqual(["single", "group"]);
    if (nodes[1].kind === "group") {
      expect(nodes[1].messages.map((message) => message.uuid)).toEqual(["b2", "b3"]);
    }
  });

  it("breaks the group on a visible event message", () => {
    const nodes = buildMessageGroups(
      [bot("b1"), bot("evt", { data_type: "event" }), bot("b2")],
      { groupConsecutive: true, suppressConfirmableEvents: true },
    );

    expect(nodes.map((node) => node.kind)).toEqual(["single", "single", "single"]);
  });

  it("treats suppressed confirmable-action events as transparent", () => {
    const nodes = buildMessageGroups(
      [
        bot("b1"),
        bot("evt", {
          data_type: "event",
          meta_data: { event_type: "confirmable_action_execute" },
        }),
        bot("b2"),
      ],
      { groupConsecutive: true, suppressConfirmableEvents: true },
    );

    expect(nodes).toHaveLength(1);
    expect(nodes[0].kind).toBe("group");
    if (nodes[0].kind === "group") {
      expect(nodes[0].messages.map((message) => message.uuid)).toEqual(["b1", "b2"]);
    }
  });

  it("keeps confirmable-action events when suppression is off", () => {
    const nodes = buildMessageGroups(
      [
        bot("b1"),
        bot("evt", {
          data_type: "event",
          meta_data: { event_type: "confirmable_action_execute" },
        }),
        bot("b2"),
      ],
      { groupConsecutive: true },
    );

    expect(nodes.map((node) => node.kind)).toEqual(["single", "single", "single"]);
  });

  it("groups a run of consecutive automated bot turns and lets a user turn break it", () => {
    const nodes = buildMessageGroups(
      [user("u1"), bot("b1"), bot("b2"), bot("b3"), user("u2"), bot("b4")],
      { groupConsecutive: true },
    );

    expect(nodes.map((node) => node.kind)).toEqual(["single", "group", "single", "single"]);
    if (nodes[1].kind === "group") {
      expect(nodes[1].messages.map((message) => message.uuid)).toEqual(["b1", "b2", "b3"]);
      expect(nodes[1].rerunSourceMessageUUID).toBe("u1");
    }
    expect(nodes[3]).toMatchObject({
      kind: "single",
      message: { uuid: "b4" },
      rerunSourceMessageUUID: "u2",
    });
  });

  it("only groups messages flagged sender_is_automated", () => {
    const nodes = buildMessageGroups(
      [bot("b1", { sender_is_automated: false }), bot("b2", { sender_is_automated: false })],
      { groupConsecutive: true },
    );

    expect(nodes.map((node) => node.kind)).toEqual(["single", "single"]);
  });

  it("only suppresses confirmable-action events, not other lifecycle events", () => {
    const nodes = buildMessageGroups(
      [
        bot("b1"),
        bot("evt", { data_type: "event", meta_data: { event_type: "provider_retry" } }),
        bot("b2"),
      ],
      { groupConsecutive: true, suppressConfirmableEvents: true },
    );

    expect(nodes.map((node) => node.kind)).toEqual(["single", "single", "single"]);
  });
});
