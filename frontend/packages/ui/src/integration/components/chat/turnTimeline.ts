// The ordered "turn timeline" of a bot message (ci#569).
//
// A tool using agent produces an interleaved stream: it reasons, calls a tool,
// reads the result, reasons again, ... The backend records that stream in
// `meta_data.timeline` (one entry per reasoning block, in order) and stamps
// every tool call with `timeline_index`, the number of reasoning blocks that had
// already been emitted when the call was dispatched.
//
// `buildTurnBlocks` weaves both back together into the ordered sequence the chat
// UI renders, so the user can follow *what the agent was thinking* between the
// tool calls instead of seeing only opaque call rows. The helpers are
// framework-free so the ordering can be unit-tested without a DOM, and they
// degrade gracefully: a message with no timeline (or no `timeline_index` on its
// calls) keeps the layout it has always had.

import { toolCallTimelineIndex, type ToolCallLike } from "./toolCallSummary";

export type TurnThoughtKind = "thought";

/** One reasoning block of a turn, as recorded in `meta_data.timeline`. */
export interface TurnThoughtBlock {
  kind: TurnThoughtKind;
  text: string;
  /** Time the model spent producing the block, when the backend reported it. */
  durationMs?: number;
  /** True while the model is still producing this block. */
  streaming?: boolean;
}

/** One tool call of a turn, at its position in the ordered sequence. */
export interface TurnToolCallBlock {
  kind: "tool_call";
  /** Index of the call in the message's `tool_calls` array. */
  index: number;
  call: ToolCallLike;
}

export type TurnBlock = TurnThoughtBlock | TurnToolCallBlock;

export interface TurnSeasonInput {
  /** Reasoning blocks recorded by the backend, in order. */
  timeline?: TurnThoughtBlock[];
  /** Tool calls of the message, in emission order. */
  toolCalls?: ToolCallLike[];
}

/** The inputs the ordered reasoning blocks can be resolved from. */
export interface TurnThoughtsInput {
  /** Reasoning blocks the backend recorded in `meta_data.timeline`. */
  timeline?: TurnThoughtBlock[];
  /** Per-step reasoning the backend reported, with their durations. */
  steps?: Array<{ text: string; duration?: string }>;
  /** Reasoning recovered from the message's `reasoning` field / think tags. */
  thoughts?: string[];
}

/**
 * Resolves the ordered reasoning blocks of a turn, preferring the explicit
 * timeline and falling back to the thinking steps (which carry their durations)
 * and finally to the plain reasoning list. The fallback matters for a message
 * hydrated mid-stream from the streaming snapshot, which carries the reasoning
 * and the tool calls but not the meta timeline.
 */
export function resolveTurnThoughts(input: TurnThoughtsInput): TurnThoughtBlock[] {
  if (input.timeline && input.timeline.length > 0) {
    return input.timeline;
  }
  const steps = (input.steps ?? [])
    .map((step) => ({ text: typeof step?.text === "string" ? step.text.trim() : "", duration: step?.duration }))
    .filter((step) => step.text.length > 0)
    .map((step): TurnThoughtBlock => ({
      kind: "thought",
      text: step.text,
      durationMs: durationFromLabel(step.duration),
    }));
  if (steps.length > 0) {
    return steps;
  }
  return (input.thoughts ?? [])
    .map((thought) => (typeof thought === "string" ? thought.trim() : ""))
    .filter((thought) => thought.length > 0)
    .map((text): TurnThoughtBlock => ({ kind: "thought", text }));
}

/** Parses the human-readable duration the backend reports ("1.4s", "820ms"). */
function durationFromLabel(label: string | undefined): number | undefined {
  const value = typeof label === "string" ? label.trim().toLowerCase() : "";
  if (!value) return undefined;
  const match = value.match(/^([0-9]+(?:\.[0-9]+)?)\s*(ms|s|m)$/);
  if (!match) return undefined;
  const amount = Number(match[1]);
  if (!Number.isFinite(amount)) return undefined;
  const unit = match[2];
  if (unit === "ms") return Math.round(amount);
  if (unit === "s") return Math.round(amount * 1000);
  return Math.round(amount * 60_000);
}

function asNumber(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return undefined;
}

/**
 * Reads the ordered reasoning blocks the backend recorded for a message
 * (`meta_data.timeline`). Unknown or malformed entries are skipped, so an older
 * message (no timeline at all) simply yields an empty list.
 */
export function parseTurnTimeline(meta: unknown): TurnThoughtBlock[] {
  if (!meta || typeof meta !== "object") return [];
  const raw = (meta as Record<string, unknown>).timeline;
  if (!Array.isArray(raw)) return [];
  const blocks: TurnThoughtBlock[] = [];
  for (const entry of raw) {
    if (!entry || typeof entry !== "object" || Array.isArray(entry)) continue;
    const record = entry as Record<string, unknown>;
    const kind = typeof record.kind === "string" ? record.kind.trim().toLowerCase() : "";
    if (kind && kind !== "thought") continue;
    const text = typeof record.text === "string" ? record.text.trim() : "";
    if (!text) continue;
    const duration = asNumber(record.duration_ms);
    blocks.push({
      kind: "thought",
      text,
      durationMs: duration !== undefined && duration >= 0 ? duration : undefined,
      streaming: record.streaming === true,
    });
  }
  return blocks;
}

/**
 * Builds the ordered blocks of a turn: every reasoning block followed (in the
 * order the model emitted them) by the tool calls that were dispatched after it.
 *
 * Messages whose calls carry no `timeline_index` (older messages, or a backend
 * that does not report it) keep the historical layout: the reasoning first, then
 * the calls.
 */
export function buildTurnBlocks(input: TurnSeasonInput): TurnBlock[] {
  const thoughts = input.timeline ?? [];
  const calls = input.toolCalls ?? [];
  if (calls.length === 0) {
    return [...thoughts];
  }
  if (calls.every((call) => toolCallTimelineIndex(call) === undefined)) {
    return [
      ...thoughts,
      ...calls.map((call, index): TurnToolCallBlock => ({ kind: "tool_call", index, call })),
    ];
  }

  const blocks: TurnBlock[] = [];
  let nextThought = 0;
  calls.forEach((call, index) => {
    const boundary = toolCallTimelineIndex(call);
    const limit = Math.min(
      boundary === undefined ? thoughts.length : Math.max(boundary, 0),
      thoughts.length,
    );
    while (nextThought < limit) {
      blocks.push(thoughts[nextThought]);
      nextThought += 1;
    }
    blocks.push({ kind: "tool_call", index, call });
  });
  while (nextThought < thoughts.length) {
    blocks.push(thoughts[nextThought]);
    nextThought += 1;
  }
  return blocks;
}
