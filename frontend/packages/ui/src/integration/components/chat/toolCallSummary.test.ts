import { describe, expect, it } from "vitest";
import {
  formatToolDuration,
  resolveToolCallStatus,
  summarizeToolCalls,
  toolCallCategory,
  toolCallDurationMs,
  toolCallStartedAtMs,
  toolCallSummary,
  toolCallTimelineIndex,
  toolDisplayLabel,
  toolResultPreview,
  toolStatusDotClasses,
} from "./toolCallSummary";

describe("resolveToolCallStatus", () => {
  it("honors an explicit status", () => {
    expect(resolveToolCallStatus({ status: "ongoing" })).toBe("ongoing");
    expect(resolveToolCallStatus({ status: "succeeded" })).toBe("succeeded");
    expect(resolveToolCallStatus({ status: "FAILED" })).toBe("failed");
    expect(resolveToolCallStatus({ status: "pending_confirmation" })).toBe("pending_confirmation");
  });

  it("infers failure from an error and from the marshalled result", () => {
    expect(resolveToolCallStatus({ error: "boom" })).toBe("failed");
    expect(resolveToolCallStatus({ result: "coder_bash failed with error: exit 1" })).toBe("failed");
  });

  it("detects a pending confirmation payload", () => {
    expect(resolveToolCallStatus({ result: '{"type":"confirm-action"}' })).toBe("pending_confirmation");
  });

  it("treats any non-empty result as succeeded and an empty call as ongoing", () => {
    expect(resolveToolCallStatus({ result: "done" })).toBe("succeeded");
    expect(resolveToolCallStatus({ result: { count: 0 } })).toBe("succeeded");
    expect(resolveToolCallStatus({})).toBe("ongoing");
  });

  it("marks a still-ongoing call in a terminal message as interrupted (ci#668)", () => {
    // The backend can leave the last call of a cancelled turn at "ongoing".
    expect(resolveToolCallStatus({ status: "ongoing" }, { terminal: true })).toBe("cancelled");
    expect(resolveToolCallStatus({}, { terminal: true })).toBe("cancelled");
    // A recorded terminal status is never downgraded.
    expect(resolveToolCallStatus({ status: "succeeded" }, { terminal: true })).toBe("succeeded");
    expect(resolveToolCallStatus({ status: "failed" }, { terminal: true })).toBe("failed");
    // A live turn keeps an ongoing call ongoing.
    expect(resolveToolCallStatus({ status: "ongoing" }, { terminal: false })).toBe("ongoing");
  });
});

describe("toolCallSummary", () => {
  it("renders a single shell command verbatim", () => {
    expect(toolCallSummary({ name: "coder_bash", arguments: { command: "npm run build" } })).toBe(
      "npm run build",
    );
  });

  it("summarizes a batched shell call", () => {
    expect(
      toolCallSummary({ name: "coder_bash", arguments: { commands: ["ls", "pwd", "whoami"] } }),
    ).toBe("3 commands");
  });

  it("shows the edited file path", () => {
    expect(toolCallSummary({ name: "coder_edit", arguments: { path: "src/app.ts" } })).toBe("src/app.ts");
  });

  it("shows a read path with its line range", () => {
    expect(
      toolCallSummary({
        name: "coder_read",
        arguments: { path: "src/app.ts", offset: 10, limit: 50 },
        result: { total_lines: 200 },
      }),
    ).toBe("src/app.ts:10-59");
  });

  it("shows a grep pattern with its include filter", () => {
    expect(
      toolCallSummary({ name: "coder_grep", arguments: { pattern: "TODO", include: "*.ts" } }),
    ).toBe('"TODO" in *.ts');
  });

  it("summarizes a git commit", () => {
    expect(toolCallSummary({ name: "coder_git", arguments: { action: "commit", message: "fix" } })).toBe(
      'commit "fix"',
    );
  });

  it("falls back to the raw command for an unknown tool", () => {
    expect(toolCallSummary({ name: "custom", arguments: { command: "do-thing" } })).toBe("do-thing");
  });
});

describe("toolResultPreview", () => {
  it("reports a grep match count", () => {
    expect(toolResultPreview({ name: "coder_grep", result: { count: 3 } })).toBe("3 matches");
    expect(toolResultPreview({ name: "coder_grep", result: { count: 0 } })).toBe("no matches");
  });

  it("reports a read line count", () => {
    expect(toolResultPreview({ name: "coder_read", result: { total_lines: 42 } })).toBe("42 lines");
  });

  it("reports a shell exit code", () => {
    expect(toolResultPreview({ name: "coder_bash", result: { exit_code: 0 } })).toBe("exit 0");
  });

  it("reports a glob file count with a truncation marker", () => {
    expect(toolResultPreview({ name: "coder_glob", result: { count: 5, truncated: true } })).toBe("5 files+");
  });
});

describe("toolCallCategory / toolDisplayLabel", () => {
  it("categorizes common tools", () => {
    expect(toolCallCategory("coder_edit")).toBe("edit");
    expect(toolCallCategory("coder_bash")).toBe("command");
    expect(toolCallCategory("coder_grep")).toBe("read");
    expect(toolCallCategory("coder_select_workspace")).toBe("workspace");
    expect(toolCallCategory("weird")).toBe("other");
  });

  it("produces a friendly label", () => {
    expect(toolDisplayLabel("coder_grep")).toBe("Search");
    expect(toolDisplayLabel("coder_bash")).toBe("Shell");
    expect(toolDisplayLabel("coder_git")).toBe("Git");
    expect(toolDisplayLabel("")).toBe("Tool");
  });
});

describe("summarizeToolCalls", () => {
  it("breaks the group down by category and counts running/failed/pending", () => {
    const summary = summarizeToolCalls([
      { name: "coder_edit", status: "succeeded" },
      { name: "coder_write", status: "succeeded" },
      { name: "coder_bash", status: "ongoing" },
      { name: "coder_grep", status: "failed" },
      { name: "coder_git", status: "pending_confirmation" },
    ]);
    expect(summary.total).toBe(5);
    expect(summary.details).toBe("2 edits · 2 commands · 1 read");
    expect(summary.running).toBe(1);
    expect(summary.failed).toBe(1);
    expect(summary.pending).toBe(1);
    expect(summary.cancelled).toBe(0);
  });

  it("counts interrupted calls separately (ci#668)", () => {
    const summary = summarizeToolCalls([
      { name: "coder_bash", status: "cancelled" },
      { name: "coder_read", status: "interrupted" },
      { name: "coder_edit", status: "succeeded" },
    ]);
    expect(summary.cancelled).toBe(2);
    expect(summary.running).toBe(0);
  });
});

describe("toolStatusDotClasses", () => {
  it("animates only while a call is ongoing", () => {
    expect(toolStatusDotClasses("ongoing")).toContain("animate-pulse");
    expect(toolStatusDotClasses("succeeded")).not.toContain("animate-pulse");
  });
});

describe("tool call timing (ci#569)", () => {
  it("reads the timing from number or string payloads", () => {
    expect(toolCallStartedAtMs({ started_at: 1_700_000_000_000 })).toBe(1_700_000_000_000);
    expect(toolCallStartedAtMs({ started_at: "1700000000000" })).toBe(1_700_000_000_000);
    expect(toolCallStartedAtMs({ started_at: 0 })).toBeUndefined();
    expect(toolCallStartedAtMs({})).toBeUndefined();
    expect(toolCallDurationMs({ duration_ms: 820 })).toBe(820);
    expect(toolCallDurationMs({ duration_ms: "820" })).toBe(820);
    expect(toolCallDurationMs({ duration_ms: 0 })).toBe(0);
    expect(toolCallDurationMs({})).toBeUndefined();
  });

  it("formats sub-second, second and minute durations compactly", () => {
    expect(formatToolDuration(820)).toBe("820ms");
    expect(formatToolDuration(1400)).toBe("1.4s");
    expect(formatToolDuration(63_000)).toBe("1m 03s");
    expect(formatToolDuration(Number.NaN)).toBe("");
  });

  it("reads the call's position in the turn timeline", () => {
    expect(toolCallTimelineIndex({ timeline_index: 2 })).toBe(2);
    expect(toolCallTimelineIndex({ timeline_index: "0" })).toBe(0);
    expect(toolCallTimelineIndex({ timeline_index: -1 })).toBeUndefined();
    expect(toolCallTimelineIndex({})).toBeUndefined();
  });
});
