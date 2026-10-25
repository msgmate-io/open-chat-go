import { describe, expect, it } from "vitest";
// Imported through the integration barrel on purpose: integration repositories
// compile against `@open-chat-go/ui`, which re-exports this module with
// `export *`, so this test pins the *public* surface rather than the internal
// file (ci#876).
import {
  buildTurnBlocks,
  parseTurnTimeline,
  resolveTurnThoughts,
  type TurnThoughtsInput,
} from "../../index";
import { resolveTurnThoughts as internalResolveTurnThoughts } from "./turnTimeline";

describe("turnTimeline public exports (ci#876)", () => {
  it("re-exports the canonical resolver through the package root", () => {
    expect(typeof resolveTurnThoughts).toBe("function");
    expect(resolveTurnThoughts).toBe(internalResolveTurnThoughts);
  });

  it("resolves the ordered reasoning blocks an extension body renders", () => {
    const input: TurnThoughtsInput = {
      steps: [
        { text: "  inspect the repo  ", duration: "1.4s" },
        { text: "", duration: "10s" },
        { text: "apply the patch", duration: "820ms" },
      ],
      thoughts: ["ignored while steps are present"],
    };
    const thoughts = resolveTurnThoughts(input);
    expect(thoughts).toEqual([
      { kind: "thought", text: "inspect the repo", durationMs: 1400 },
      { kind: "thought", text: "apply the patch", durationMs: 820 },
    ]);
    // The resolved blocks feed straight into the ordered turn sequence.
    const blocks = buildTurnBlocks({ timeline: thoughts, toolCalls: [{ id: "call_1" }] });
    expect(blocks.map((block) => block.kind)).toEqual(["thought", "thought", "tool_call"]);
  });

  it("prefers the explicit meta timeline over the mid-stream fallbacks", () => {
    const timeline = parseTurnTimeline({
      timeline: [{ kind: "thought", text: "from meta", duration_ms: 1200 }],
    });
    expect(
      resolveTurnThoughts({ timeline, steps: [{ text: "from steps" }], thoughts: ["from reasoning"] }),
    ).toEqual(timeline);
  });

  it("falls back to the plain reasoning list when neither timeline nor steps exist", () => {
    expect(resolveTurnThoughts({ thoughts: ["  reasoned  ", ""] })).toEqual([
      { kind: "thought", text: "reasoned" },
    ]);
  });
});
