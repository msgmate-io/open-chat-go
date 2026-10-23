import { describe, expect, it } from "vitest";
import { buildTurnBlocks, parseTurnTimeline } from "./turnTimeline";

describe("parseTurnTimeline", () => {
  it("reads the ordered reasoning blocks the backend recorded", () => {
    const blocks = parseTurnTimeline({
      timeline: [
        { kind: "thought", text: "first", duration_ms: 1200 },
        { kind: "thought", text: "  second  ", streaming: true },
      ],
    });
    expect(blocks).toEqual([
      { kind: "thought", text: "first", durationMs: 1200, streaming: false },
      { kind: "thought", text: "second", durationMs: undefined, streaming: true },
    ]);
  });

  it("accepts string durations and ignores malformed entries", () => {
    const blocks = parseTurnTimeline({
      timeline: [
        { kind: "thought", text: "a", duration_ms: "820" },
        { text: "" },
        { kind: "unknown-kind", text: "narration" },
        null,
        { kind: "thought", text: "b", duration_ms: "nope" },
      ],
    });
    expect(blocks.map((block) => block.text)).toEqual(["a", "b"]);
    expect(blocks[0].durationMs).toBe(820);
    expect(blocks[1].durationMs).toBeUndefined();
  });

  it("keeps assistant narration blocks distinct from reasoning (ci#569)", () => {
    const blocks = parseTurnTimeline({
      timeline: [
        { kind: "thought", text: "the test fails on the stale assertion" },
        { kind: "text", text: "Let me check the migration next." },
        { kind: "text", text: "  Patching the assertion.  " },
      ],
    });
    expect(blocks.map((block) => block.kind)).toEqual(["thought", "text", "text"]);
    expect(blocks.map((block) => block.text)).toEqual([
      "the test fails on the stale assertion",
      "Let me check the migration next.",
      "Patching the assertion.",
    ]);
  });

  it("returns nothing for messages without a timeline", () => {
    expect(parseTurnTimeline(undefined)).toEqual([]);
    expect(parseTurnTimeline({})).toEqual([]);
    expect(parseTurnTimeline({ timeline: "nope" })).toEqual([]);
  });
});

describe("buildTurnBlocks", () => {
  const thoughts = [
    { kind: "thought" as const, text: "before the first call" },
    { kind: "thought" as const, text: "before the second call" },
  ];

  it("interleaves the reasoning with the calls that followed it", () => {
    const calls = [
      { id: "c1", name: "bash", timeline_index: 1 },
      { id: "c2", name: "grep", timeline_index: 2 },
    ];
    const blocks = buildTurnBlocks({ timeline: thoughts, toolCalls: calls });
    expect(blocks.map((block) => (block.kind === "tool_call" ? block.call.id : block.text))).toEqual([
      "before the first call",
      "c1",
      "before the second call",
      "c2",
    ]);
  });

  it("keeps the historical layout when the calls carry no position", () => {
    const calls = [{ id: "c1", name: "bash" }, { id: "c2", name: "grep" }];
    const blocks = buildTurnBlocks({ timeline: thoughts, toolCalls: calls });
    expect(blocks.map((block) => (block.kind === "tool_call" ? block.call.id : block.text))).toEqual([
      "before the first call",
      "before the second call",
      "c1",
      "c2",
    ]);
  });

  it("handles a call dispatched before any reasoning and reasoning after the last call", () => {
    const calls = [
      { id: "c1", name: "bash", timeline_index: 0 },
      { id: "c2", name: "grep", timeline_index: 1 },
    ];
    const blocks = buildTurnBlocks({ timeline: thoughts, toolCalls: calls });
    expect(blocks.map((block) => (block.kind === "tool_call" ? block.call.id : block.text))).toEqual([
      "c1",
      "before the first call",
      "c2",
      "before the second call",
    ]);
  });

  it("clamps out-of-range positions and ignores a missing timeline", () => {
    const calls = [{ id: "c1", name: "bash", timeline_index: 99 }];
    expect(buildTurnBlocks({ timeline: thoughts, toolCalls: calls })).toEqual([
      thoughts[0],
      thoughts[1],
      { kind: "tool_call", index: 0, call: calls[0] },
    ]);
    expect(buildTurnBlocks({ toolCalls: calls })).toEqual([
      { kind: "tool_call", index: 0, call: calls[0] },
    ]);
  });

  it("renders only the reasoning when the turn called no tool", () => {
    expect(buildTurnBlocks({ timeline: thoughts })).toEqual(thoughts);
  });
});
