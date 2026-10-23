import { describe, expect, it } from "vitest";
import { latestPendingConfirmableActionId } from "./confirmable-actions";

// The auto-expand rule for the confirmable-action widget (ci#488): exactly the
// latest still-unconfirmed ("blocking") action is opened automatically, so the
// chat is waiting on the control the user actually has to act on.
describe("latestPendingConfirmableActionId", () => {
  it("returns undefined for an empty or missing list", () => {
    expect(latestPendingConfirmableActionId([])).toBeUndefined();
    expect(latestPendingConfirmableActionId(null)).toBeUndefined();
    expect(latestPendingConfirmableActionId(undefined)).toBeUndefined();
  });

  it("returns undefined when no action is pending", () => {
    expect(
      latestPendingConfirmableActionId([
        { action_id: "a1", status: "executed" },
        { action_id: "a2", status: "rejected" },
        { action_id: "a3", status: "cancelled" },
        { action_id: "a4", status: "failed" },
      ]),
    ).toBeUndefined();
  });

  it("selects the last pending action in document order", () => {
    expect(
      latestPendingConfirmableActionId([
        { action_id: "a1", status: "pending" },
        { action_id: "a2", status: "executed" },
        { action_id: "a3", status: "pending" },
      ]),
    ).toBe("a3");
  });

  it("treats a missing status as pending", () => {
    expect(
      latestPendingConfirmableActionId([
        { action_id: "a1" },
        { action_id: "a2", status: "executed" },
      ]),
    ).toBe("a1");
  });

  it("does not mutate the input array", () => {
    const actions = [
      { action_id: "a1", status: "pending" },
      { action_id: "a2", status: "pending" },
    ];
    const snapshot = [...actions];
    latestPendingConfirmableActionId(actions);
    expect(actions).toEqual(snapshot);
    expect(actions[0].action_id).toBe("a1");
  });
});
