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

  it("renders a completed tool call's recorded duration (ci#569)", () => {
    const html = render({
      uuid: "m4",
      text: "done",
      meta_data: { finished: false },
      tool_calls: [
        {
          name: "bash",
          arguments: { command: "sleep 1" },
          result: { exit_code: 0 },
          status: "succeeded",
          started_at: 1_700_000_000_000,
          duration_ms: 1400,
        },
      ],
    });
    expect(html).toContain('data-testid="tool-call-timing"');
    expect(html).toContain("1.4s");
  });

  it("renders a live elapsed timer for an ongoing timed tool call (ci#569)", () => {
    const html = render({
      uuid: "m5",
      text: "working",
      meta_data: { finished: false },
      tool_calls: [
        {
          name: "bash",
          arguments: { command: "sleep 30" },
          status: "ongoing",
          started_at: Date.now() - 2500,
        },
      ],
    });
    expect(html).toContain('data-testid="tool-call-timing"');
    expect(html).toContain("Running for");
  });

  it("shows the working spinner at the bottom of a still-streaming partial message", () => {
    const html = render({
      uuid: "partial_chat-1",
      text: "streaming so far",
      thoughts: [],
      meta_data: { partial_phase: "responding", timeline: [{ kind: "thought", text: "checking" }] },
    });
    expect(html).toContain("Working");
    expect(html).toContain("animate-spin");
    // The reasoning already streamed in is rendered inline, before the spinner.
    expect(html).toContain('data-testid="turn-thought"');
    expect(html.indexOf("checking")).toBeLessThan(html.indexOf("Working"));
  });

  it("renders the reasoning blocks between the tool calls when the turn has a timeline (ci#569)", () => {
    const html = render({
      uuid: "m6",
      text: "all done",
      meta_data: {
        finished: true,
        timeline: [
          { kind: "thought", text: "I should inspect the failing test.", duration_ms: 1200 },
          { kind: "thought", text: "The assertion is stale, I will fix it." },
        ],
      },
      tool_calls: [
        {
          name: "bash",
          arguments: { command: "npm test" },
          result: { exit_code: 1 },
          status: "failed",
          timeline_index: 1,
          duration_ms: 820,
        },
      ],
    });
    expect(html).toContain('data-testid="turn-sequence"');
    expect(html).toContain('data-testid="turn-thought"');
    expect(html).toContain("I should inspect the failing test.");
    expect(html).toContain("The assertion is stale, I will fix it.");
    expect(html).toContain("1.2s");
    expect(html).toContain("820ms");
    // The reasoning block that preceded the call is rendered before it.
    expect(html.indexOf("I should inspect the failing test.")).toBeLessThan(
      html.indexOf("npm test"),
    );
  });

  it("keeps the plain tool-call group for a turn without a timeline", () => {
    const html = render({
      uuid: "m7",
      text: "done",
      meta_data: { finished: true },
      tool_calls: [
        {
          name: "bash",
          arguments: { command: "ls" },
          result: { exit_code: 0 },
          status: "succeeded",
        },
      ],
    });
    expect(html).not.toContain('data-testid="turn-sequence"');
    expect(html).toContain("tool-calls-m7");
  });

  it("does not render the timeline narration twice while the turn streams (ci#569)", () => {
    const html = render({
      uuid: "partial_chat-2",
      text: "Now let me check the migration.",
      meta_data: {
        partial_phase: "responding",
        timeline: [
          { kind: "thought", text: "The build log points at the stale import." },
          { kind: "text", text: "Now let me check the migration." },
        ],
      },
      tool_calls: [
        {
          name: "read",
          arguments: { path: "migrations/001.sql" },
          result: {},
          status: "ongoing",
          timeline_index: 2,
        },
      ],
    });
    // The reasoning is rendered by the sequence, the narration by the body.
    expect(html).toContain('data-testid="turn-thought"');
    expect(html).toContain("The build log points at the stale import.");
    expect(html).not.toContain('data-testid="turn-narration"');
    // The streaming narration text appears exactly once (as the message body).
    const occurrences = html.split("Now let me check the migration.").length - 1;
    expect(occurrences).toBe(1);
  });

  it("renders the assistant narration between the calls in order (ci#569)", () => {
    const html = render({
      uuid: "m8",
      text: "All green now.",
      meta_data: {
        finished: true,
        timeline: [
          { kind: "thought", text: "The build log points at the stale import." },
          { kind: "text", text: "Let me drop the stale import first." },
          { kind: "text", text: "Now I will re-run the suite." },
        ],
      },
      tool_calls: [
        {
          name: "edit",
          arguments: { path: "src/app.ts" },
          result: { replacements: 1 },
          status: "succeeded",
          timeline_index: 2,
          duration_ms: 120,
        },
        {
          name: "bash",
          arguments: { command: "npm test" },
          result: { exit_code: 0 },
          status: "succeeded",
          timeline_index: 3,
          duration_ms: 8200,
        },
      ],
    });
    expect(html).toContain('data-testid="turn-narration"');
    expect(html).toContain("Let me drop the stale import first.");
    expect(html).toContain("Now I will re-run the suite.");
    // Reasoning sits first, then its narration, then the call it produced.
    const thought = html.indexOf("The build log points at the stale import.");
    const firstNarration = html.indexOf("Let me drop the stale import first.");
    const firstCall = html.indexOf("src/app.ts");
    const secondNarration = html.indexOf("Now I will re-run the suite.");
    expect(thought).toBeGreaterThan(-1);
    expect(thought).toBeLessThan(firstNarration);
    expect(firstNarration).toBeLessThan(firstCall);
    expect(firstCall).toBeLessThan(secondNarration);
    // Durations still render per call, and the header counts them correctly.
    expect(html).toContain("120ms");
    expect(html).toContain("8.2s");
    expect(html).toContain("1 thought");
    expect(html).toContain("2 commentary");
  });

  it("says so when a finished tool turn reported no reasoning at all (ci#569)", () => {
    const html = render({
      uuid: "m9",
      text: "done",
      meta_data: { finished: true },
      tool_calls: [
        {
          name: "bash",
          arguments: { command: "ls" },
          result: { exit_code: 0 },
          status: "succeeded",
        },
      ],
    });
    expect(html).toContain('data-testid="turn-no-reasoning"');
    expect(html).toContain("without any reasoning or commentary from the agent");
  });

  it("does not claim missing reasoning while the turn is still running", () => {
    const html = render({
      uuid: "m10",
      text: "running",
      meta_data: { finished: false },
      tool_calls: [
        {
          name: "bash",
          arguments: { command: "ls" },
          result: {},
          status: "ongoing",
        },
      ],
    });
    expect(html).not.toContain('data-testid="turn-no-reasoning"');
  });

  it("does not claim missing reasoning when thoughts were reported", () => {
    const html = render({
      uuid: "m11",
      text: "done",
      thoughts: ["I looked at the file first."],
      meta_data: { finished: true },
      tool_calls: [
        {
          name: "bash",
          arguments: { command: "ls" },
          result: { exit_code: 0 },
          status: "succeeded",
        },
      ],
    });
    expect(html).not.toContain('data-testid="turn-no-reasoning"');
  });
});
