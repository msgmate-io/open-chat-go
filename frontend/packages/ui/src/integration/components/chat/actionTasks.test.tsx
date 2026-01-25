import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

// The widget/helper import from the @open-chat-go/ui barrel, which re-exports
// the integration surface (and therefore Vike). Stub Vike's Node-version-gated
// client entry points so the pure Node unit project can render the component.
vi.mock("vike-react/usePageContext", () => ({ usePageContext: () => ({}) }));
vi.mock("vike/client/router", () => ({ navigate: vi.fn() }));

import { ConfirmableActionWidget } from "./ConfirmableActionWidget";
import { extractConfirmableActionsFromMeta, mergeConfirmableAction } from "./actionTasks";

// Regression coverage for ci#489: the actions-required preview rebuilt the
// widget action from the compact action-task summary only, dropping `input`, so
// the edit text / tool args rendered as "No params"/"{}". The full proposal is
// persisted in the message meta_data and must be merged back by action_id.
describe("mergeConfirmableAction", () => {
  it("restores the proposed params from the message meta_data", () => {
    const meta = extractConfirmableActionsFromMeta({
      confirmable_actions: [
        {
          action_id: "tc-1",
          target_tool_name: "coder_write",
          status: "pending",
          input: { path: "development/scripts/send_slack_message.py", content: "print('hi')" },
          confirm_label: "Write file",
        },
      ],
    });

    const action = mergeConfirmableAction(
      { action_id: "tc-1", target_tool_name: "coder_write", kind: "confirmable_action" },
      meta,
    );

    expect(action).not.toBeNull();
    expect(action?.input).toEqual({
      path: "development/scripts/send_slack_message.py",
      content: "print('hi')",
    });
    expect(action?.confirm_label).toBe("Write file");
  });

  it("falls back to the summary params when the meta has no matching action", () => {
    const action = mergeConfirmableAction(
      {
        action_id: "tc-2",
        target_tool_name: "coder_bash",
        input: { command: "rm -rf build" },
      },
      extractConfirmableActionsFromMeta({ confirmable_actions: [] }),
    );

    expect(action?.input).toEqual({ command: "rm -rf build" });
    expect(action?.status).toBe("pending");
  });

  it("returns null when the summary lacks an action id or target tool", () => {
    expect(mergeConfirmableAction({ kind: "confirmable_action" })).toBeNull();
    expect(mergeConfirmableAction({ action_id: "tc-3", kind: "confirmable_action" })).toBeNull();
  });
});

describe("actions-required widget rendering", () => {
  it("renders the edit path/content and proposed params after merging", () => {
    const meta = extractConfirmableActionsFromMeta({
      confirmable_actions: [
        {
          action_id: "tc-9",
          target_tool_name: "coder_write",
          status: "pending",
          input: { path: "src/app.ts", content: "export const answer = 42;" },
        },
      ],
    });
    const action = mergeConfirmableAction(
      { action_id: "tc-9", target_tool_name: "coder_write", kind: "confirmable_action" },
      meta,
    );
    if (!action) {
      throw new Error("expected a merged action");
    }

    const html = renderToStaticMarkup(
      <ConfirmableActionWidget chatUUID="chat-1" messageUUID="msg-1" action={action} />,
    );

    // The overwrite preview names the file and shows its content, and the
    // proposed params are no longer empty.
    expect(html).toContain("src/app.ts");
    expect(html).toContain("export const answer = 42;");
    expect(html).not.toContain("No params");
    expect(html).not.toContain("{}");
  });
});
