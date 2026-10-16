import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

// The widget imports from the @open-chat-go/ui barrel, which re-exports the
// integration surface (and therefore Vike). Stub Vike's Node-version-gated
// client entry points so the pure Node unit project can render the component.
vi.mock("vike-react/usePageContext", () => ({ usePageContext: () => ({}) }));
vi.mock("vike/client/router", () => ({ navigate: vi.fn() }));

import { ConfirmableActionWidget, type ConfirmableAction } from "./ConfirmableActionWidget";

// The msgmate coder renders its confirmable actions inline (one widget per
// confirmed tool call) instead of letting the core draw a separate widget plus
// the requested/completed/failed/rejected lifecycle bubbles. These tests pin the
// widget's phase rendering (ci#394) so a regression that drops the inline
// execution error is caught without a browser.
function makeAction(overrides: Partial<ConfirmableAction> = {}): ConfirmableAction {
  return {
    action_id: "act-1",
    target_tool_name: "coder_bash",
    status: "pending",
    input: { command: "rm -rf build" },
    ...overrides,
  };
}

const baseProps = { chatUUID: "chat-1", messageUUID: "msg-1" };

describe("ConfirmableActionWidget phase rendering", () => {
  it("renders the confirm/reject controls for a requested (pending) action", () => {
    const html = renderToStaticMarkup(<ConfirmableActionWidget {...baseProps} action={makeAction()} />);

    expect(html).toContain("coder_bash");
    expect(html).toContain("Confirm and execute");
    expect(html).toContain("Reject");
    // The proposed command is surfaced before expanding.
    expect(html).toContain("rm -rf build");
  });

  it("renders a failed execution_error inline while the action is still pending", () => {
    const html = renderToStaticMarkup(
      <ConfirmableActionWidget
        {...baseProps}
        action={makeAction({ execution_error: "bash: command exited with code 1" })}
      />,
    );

    // The error belongs to the widget itself, not a separate lifecycle message.
    expect(html).toContain("bash: command exited with code 1");
    expect(html).toContain("Confirm and execute");
  });

  it("labels a completed action as done and drops the execute controls", () => {
    const html = renderToStaticMarkup(
      <ConfirmableActionWidget {...baseProps} action={makeAction({ status: "executed", result: { exit_code: 0 } })} />,
    );

    expect(html).toContain(">done<");
    expect(html).not.toContain("Confirm and execute");
    expect(html).not.toContain("Reject");
  });

  it("labels a failed action and exposes its execution_error", () => {
    const html = renderToStaticMarkup(
      <ConfirmableActionWidget
        {...baseProps}
        action={makeAction({ status: "failed", execution_error: "patch did not apply" })}
      />,
    );

    expect(html).toContain(">failed<");
    expect(html).not.toContain("Confirm and execute");
  });

  it("labels a rejected action", () => {
    const html = renderToStaticMarkup(
      <ConfirmableActionWidget {...baseProps} action={makeAction({ status: "rejected" })} />,
    );

    expect(html).toContain(">rejected<");
    expect(html).not.toContain("Confirm and execute");
  });

  it("labels a cancelled action and drops the execute controls", () => {
    const html = renderToStaticMarkup(
      <ConfirmableActionWidget {...baseProps} action={makeAction({ status: "cancelled" })} />,
    );

    expect(html).toContain(">cancelled<");
    expect(html).not.toContain("Confirm and execute");
    expect(html).not.toContain("Reject");
  });

  it("keeps the compact checkbox, execute and reject controls in one row", () => {
    const html = renderToStaticMarkup(<ConfirmableActionWidget {...baseProps} action={makeAction()} />);

    // The controls share a single non-wrapping row inside the widget body.
    expect(html).toContain("Continue after execution");
    expect(html).toContain("flex flex-nowrap items-center gap-2");
    expect(html).toContain("ml-auto flex shrink-0 items-center gap-2");
    // The raw editable JSON textarea is no longer rendered.
    expect(html).not.toContain("<textarea");
  });

  it("auto-expands the latest unconfirmed (blocking) action", () => {
    const html = renderToStaticMarkup(
      <ConfirmableActionWidget {...baseProps} action={makeAction()} autoExpand />,
    );

    // autoExpand opens the body so the blocking controls are immediately usable.
    expect(html).toContain("Confirm and execute");
    expect(html).toContain("Reject");
    expect(html).toContain("Proposed params");
  });

  it("keeps a non-latest pending action collapsed until the user expands it", () => {
    const html = renderToStaticMarkup(
      <ConfirmableActionWidget {...baseProps} action={makeAction()} autoExpand={false} />,
    );

    // The collapsed header still names the tool, but the body (and its
    // execute/reject controls) is not rendered until the user expands it.
    expect(html).toContain("coder_bash");
    expect(html).not.toContain("Confirm and execute");
    expect(html).not.toContain("Reject");
  });

  it("shows the structured edit preview for coder file actions", () => {
    const html = renderToStaticMarkup(
      <ConfirmableActionWidget
        {...baseProps}
        action={makeAction({
          target_tool_name: "coder_edit",
          input: { path: "src/app.ts", old_string: "before", new_string: "after" },
        })}
      />,
    );

    // The structured diff preview renders for the file action.
    expect(html).toContain("edit src/app.ts");
    expect(html).toContain("bg-red-500/10");
    expect(html).toContain("bg-emerald-500/10");
  });
});
