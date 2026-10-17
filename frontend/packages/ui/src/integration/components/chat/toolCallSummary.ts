// Shared, framework-free helpers that turn a raw tool call into the compact,
// human-readable pieces the chat UI renders: a status, a per-tool parameter
// summary (e.g. the shell command, the edited file, the search pattern, the git
// action) and a short result preview. They are used by the core tool-call rows
// in `MessageItem` and are the canonical surface integration-owned message
// bodies (such as the msgmate coder timeline) build their rows on, so every
// tool call reads the same regardless of the chat backend.
//
// Tool names, arguments and results arrive in several shapes depending on the
// backend: names may carry a `coder_` namespace prefix, and structured results
// may be an already-decoded object or the JSON string the integration bridge
// marshals. The helpers below normalize both.

export type ToolCallStatus = "ongoing" | "succeeded" | "failed" | "pending_confirmation";

/** The subset of a tool call these helpers need. */
export interface ToolCallLike {
  id?: string;
  name?: string;
  arguments?: unknown;
  result?: unknown;
  status?: string;
  error?: string;
  /** Unix milliseconds when execution began (ci#569). */
  started_at?: number | string;
  /** Total execution time in milliseconds, once the call completed (ci#569). */
  duration_ms?: number | string;
}

export type ToolCallCategory = "edit" | "command" | "read" | "workspace" | "other";

export interface ToolCallSummary {
  /** Total number of calls in the group. */
  total: number;
  /** Category breakdown, e.g. "3 edits · 2 commands · 1 read". */
  details: string;
  failed: number;
  pending: number;
  running: number;
}

export function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  return value as Record<string, unknown>;
}

function str(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function num(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return undefined;
}

export function plural(count: number, singular: string, pluralForm?: string): string {
  return `${count} ${count === 1 ? singular : pluralForm || `${singular}s`}`;
}

/** Strip a `coder_` namespace prefix and lowercase, for stable matching. */
export function shortToolName(name: string | undefined): string {
  return (name || "").replace(/^coder_/, "").toLowerCase();
}

// A tool call's result may arrive as an object or as the JSON string the bridge
// marshals. Decode the string form so the per-tool summaries can read fields.
export function resultRecord(call: ToolCallLike): Record<string, unknown> | null {
  const result = call.result;
  if (typeof result === "string") {
    const trimmed = result.trim();
    if (trimmed.startsWith("{") && trimmed.endsWith("}")) {
      try {
        return asRecord(JSON.parse(trimmed));
      } catch {
        return null;
      }
    }
    return null;
  }
  return asRecord(result);
}

// Resolve a tool call's lifecycle status. An explicit status wins; otherwise it
// is inferred from the error text / result so older messages that only carried
// a result still render a sensible affordance.
export function resolveToolCallStatus(call: ToolCallLike): ToolCallStatus {
  const rawStatus = typeof call?.status === "string" ? call.status.trim().toLowerCase() : "";
  if (rawStatus === "ongoing") return "ongoing";
  if (rawStatus === "succeeded") return "succeeded";
  if (rawStatus === "failed") return "failed";
  if (rawStatus === "pending_confirmation") return "pending_confirmation";

  const errorText = typeof call?.error === "string" ? call.error.trim() : "";
  if (errorText) {
    return "failed";
  }

  if (typeof call?.result === "string") {
    const normalized = call.result.trim().toLowerCase();
    if (normalized.includes(" failed with error:")) return "failed";
    if (normalized.includes('"type":"confirm-action"')) return "pending_confirmation";
    if (normalized.length > 0) return "succeeded";
  }

  if (call?.result !== undefined && call?.result !== null) {
    if (Array.isArray(call.result)) {
      if (call.result.length > 0) return "succeeded";
    } else if (typeof call.result === "object") {
      if (Object.keys(call.result as Record<string, unknown>).length > 0) return "succeeded";
    } else {
      return "succeeded";
    }
  }

  return "ongoing";
}

export function isCompletedToolStatus(status: ToolCallStatus): boolean {
  return status === "succeeded" || status === "failed";
}

/** Unix milliseconds when the call began, if the backend reported it (ci#569). */
export function toolCallStartedAtMs(call: ToolCallLike): number | undefined {
  const started = num(call?.started_at);
  return started !== undefined && started > 0 ? started : undefined;
}

/** Recorded execution time in milliseconds, if the backend reported it (ci#569). */
export function toolCallDurationMs(call: ToolCallLike): number | undefined {
  const duration = num(call?.duration_ms);
  return duration !== undefined && duration >= 0 ? duration : undefined;
}

/**
 * Human-readable duration: sub-second as "820ms", under a minute as "1.4s",
 * otherwise "1m 03s". Keeps the tool-call row compact (ci#569).
 */
export function formatToolDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const seconds = ms / 1000;
  if (seconds < 60) return `${seconds.toFixed(1)}s`;
  const whole = Math.floor(seconds);
  const minutes = Math.floor(whole / 60);
  const remainder = whole % 60;
  return `${minutes}m ${String(remainder).padStart(2, "0")}s`;
}

export function toolStatusLabel(status: ToolCallStatus): string {
  if (status === "succeeded") return "Succeeded";
  if (status === "failed") return "Failed";
  if (status === "pending_confirmation") return "Needs confirmation";
  return "Ongoing";
}

/** Badge classes for a status pill (border + tinted text). */
export function toolStatusBadgeClasses(status: ToolCallStatus): string {
  if (status === "succeeded") return "bg-emerald-500/10 text-emerald-700 border-emerald-500/30";
  if (status === "failed") return "bg-rose-500/10 text-rose-700 border-rose-500/30";
  if (status === "pending_confirmation") return "bg-amber-500/10 text-amber-700 border-amber-500/30";
  return "bg-sky-500/10 text-sky-700 border-sky-500/30";
}

/** Classes for the small status dot shown before a tool label. */
export function toolStatusDotClasses(status: ToolCallStatus): string {
  if (status === "succeeded") return "bg-emerald-500";
  if (status === "failed") return "bg-rose-500";
  if (status === "pending_confirmation") return "bg-amber-500";
  return "bg-sky-500 animate-pulse";
}

/** Map a tool name to a coarse category, used for the collapsed group summary. */
export function toolCallCategory(name: string | undefined): ToolCallCategory {
  switch (shortToolName(name)) {
    case "edit":
    case "write":
    case "patch":
      return "edit";
    case "bash":
    case "git":
    case "shell":
    case "exec":
      return "command";
    case "read":
    case "list":
    case "glob":
    case "grep":
    case "search":
    case "webfetch":
    case "websearch":
      return "read";
    case "select_workspace":
    case "workspace":
      return "workspace";
    default:
      return "other";
  }
}

/** A friendly, capitalized label for a tool name. */
export function toolDisplayLabel(name: string | undefined): string {
  const short = shortToolName(name);
  switch (short) {
    case "read":
      return "Read";
    case "list":
      return "List";
    case "glob":
      return "Glob";
    case "grep":
    case "search":
      return "Search";
    case "webfetch":
      return "Fetch";
    case "websearch":
      return "Web search";
    case "ask":
      return "Question";
    case "write":
      return "Write";
    case "edit":
    case "patch":
      return "Edit";
    case "bash":
    case "shell":
      return "Shell";
    case "git":
      return "Git";
    case "select_workspace":
    case "workspace":
      return "Workspace";
    default:
      return short || "Tool";
  }
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function fileListFromArgs(raw: unknown): string[] {
  if (Array.isArray(raw)) return raw.map((item) => String(item)).filter(Boolean);
  if (typeof raw === "string") return raw.split(/\s+/).filter(Boolean);
  return [];
}

// coder_read reports { path, content, offset, limit, total_lines, truncated }.
// The summary shows the file and the line range actually read, e.g.
// "src/app.ts:10-59" instead of the bare path.
function readLineRange(args: Record<string, unknown>, result: Record<string, unknown> | null): string {
  const start = Math.max(1, num(args.offset) ?? 1);
  const limit = Math.max(1, num(args.limit) ?? 2000);
  const total = num(result?.total_lines);
  const end = total !== undefined ? Math.min(start + limit - 1, total) : undefined;
  if (end !== undefined && end >= start) return `${start}-${end}`;
  return start > 1 ? `from ${start}` : "";
}

function bashSummary(args: Record<string, unknown>): string {
  const commands = fileListFromArgs(args.commands);
  const single = str(args.command);
  const all = (single ? [single] : []).concat(commands);
  const cwd = str(args.cwd);
  const prefix = cwd && cwd !== "." ? `(in ${cwd}) ` : "";
  if (all.length === 0) return "";
  if (all.length === 1) return prefix + all[0];
  return `${prefix}${plural(all.length, "command")}`;
}

function gitSummary(args: Record<string, unknown>): string {
  const action = str(args.action);
  switch (action) {
    case "commit":
      return `commit "${str(args.message)}"`;
    case "branch":
      return `branch ${str(args.branch)}`;
    case "checkout":
      return `checkout ${str(args.branch)}`;
    case "push":
      return `push ${str(args.remote) || "origin"} ${str(args.branch)}`;
    case "add": {
      const files = fileListFromArgs(args.files);
      return files.length > 0
        ? `add ${files.length === 1 ? files[0] : plural(files.length, "file")}`
        : "add";
    }
    case "diff":
      return args.staged === true ? "diff --staged" : "diff";
    case "reset":
      return args.clean === true ? "reset --clean" : "reset";
    default:
      return action;
  }
}

// toolCallSummary renders the call's parameters as a compact, tool-aware
// one-liner (the actual command/arguments) instead of a generic best-effort
// string, so e.g. Glob shows its pattern and Read shows the file plus range.
export function toolCallSummary(call: ToolCallLike): string {
  const args = asRecord(call.arguments);
  if (!args) return "";
  const path = str(args.path);
  switch (shortToolName(call.name)) {
    case "glob":
      return str(args.pattern);
    case "list": {
      const depth = num(args.depth);
      const target = path || ".";
      return depth && depth !== 2 ? `${target} (depth ${depth})` : target;
    }
    case "grep":
    case "search": {
      const pattern = str(args.pattern) || str(args.query);
      if (!pattern) return "";
      let summary = `"${pattern}"`;
      const include = str(args.include);
      if (include) summary += ` in ${include}`;
      if (path && path !== ".") summary += ` under ${path}`;
      return summary;
    }
    case "read": {
      if (!path) return "";
      const range = readLineRange(args, resultRecord(call));
      return range ? `${path}:${range}` : path;
    }
    case "write":
    case "edit":
    case "patch":
      return path || str(args.file) || str(args.filename);
    case "webfetch":
      return str(args.url);
    case "websearch":
      return str(args.query);
    case "ask": {
      const questions = args.questions;
      if (Array.isArray(questions) && questions.length > 0) {
        const first = asRecord(questions[0]);
        const label = str(first?.header) || str(first?.question);
        return questions.length > 1 ? `${label} (+${questions.length - 1} more)` : label;
      }
      return "";
    }
    case "bash":
    case "shell":
      return bashSummary(args);
    case "git":
      return gitSummary(args);
    case "select_workspace":
    case "workspace":
      return str(args.workspace) || str(args.workspace_uuid) || str(args.model);
    default:
      break;
  }
  // Fallback for unknown/custom tools.
  const command = str(args.command);
  if (command) return command;
  const action = str(args.action);
  if (action) return path ? `${action} ${path}` : action;
  if (path) return path;
  const pattern = str(args.pattern) || str(args.query);
  if (pattern) return str(args.include) ? `${pattern} (${str(args.include)})` : pattern;
  const url = str(args.url);
  if (url) return url;
  return "";
}

// toolResultPreview renders a short, human-readable summary of the tool's
// outcome (how many files/entries/matches/lines were found, the exit code, ...)
// so the compact row communicates the result without expanding the call.
export function toolResultPreview(call: ToolCallLike): string {
  const record = resultRecord(call);
  const truncated = record?.truncated === true ? "+" : "";
  switch (shortToolName(call.name)) {
    case "glob": {
      const count = num(record?.count);
      if (count === undefined) break;
      return count === 0 ? "no files" : `${plural(count, "file")}${truncated}`;
    }
    case "list": {
      const count = num(record?.count);
      if (count === undefined) break;
      return count === 0 ? "empty" : `${plural(count, "entry", "entries")}${truncated}`;
    }
    case "grep":
    case "search": {
      const count = num(record?.count);
      if (count === undefined) break;
      return count === 0 ? "no matches" : `${plural(count, "match", "matches")}${truncated}`;
    }
    case "read": {
      // Structured reads report a kind: image (dims + format), pdf (page
      // count) or notebook (cell count); everything else is line-based text.
      const kind = str(record?.kind);
      if (kind === "image") {
        const format = str(record?.format).toUpperCase();
        const width = num(record?.width);
        const height = num(record?.height);
        const dims = width && height ? `${width}×${height}` : "";
        return [format, dims].filter(Boolean).join(" ") || "image";
      }
      if (kind === "pdf") {
        const pages = num(record?.page_count);
        if (pages !== undefined) return pages === 0 ? "empty" : plural(pages, "page");
      }
      if (kind === "notebook") {
        const cells = num(record?.cell_count);
        if (cells !== undefined) return cells === 0 ? "empty" : plural(cells, "cell");
      }
      const total = num(record?.total_lines);
      if (total === undefined) break;
      return total === 0 ? "empty" : plural(total, "line");
    }
    case "webfetch": {
      const status = num(record?.status);
      if (status === undefined) break;
      return `${status}`;
    }
    case "websearch": {
      const count = num(record?.count);
      if (count === undefined) break;
      return count === 0 ? "no results" : plural(count, "result");
    }
    case "write": {
      const bytes = num(record?.bytes_written);
      if (bytes === undefined) break;
      return record?.created === true ? `created ${formatBytes(bytes)}` : `${formatBytes(bytes)}`;
    }
    case "edit":
    case "patch": {
      const replacements = num(record?.replacements);
      if (replacements === undefined) break;
      return plural(replacements, "replacement");
    }
    case "bash":
    case "shell": {
      const count = num(record?.count);
      if (count !== undefined && count > 1) return plural(count, "command");
      const exit = num(record?.exit_code);
      if (exit === undefined) break;
      return `exit ${exit}`;
    }
    case "git": {
      const branch = str(record?.branch);
      if (branch) return record?.dirty === true ? `${branch} *` : branch;
      const exit = num(record?.exit_code);
      if (exit === undefined) break;
      return record?.success === true ? "ok" : `exit ${exit}`;
    }
    default:
      break;
  }
  // Fallback for unknown/custom tools.
  const result = call.result;
  if (typeof result === "string") {
    const firstLine = result.split("\n").find((line) => line.trim().length > 0) || "";
    return firstLine.trim();
  }
  if (record) {
    const exit = num(record.exit_code);
    if (exit !== undefined) return `exit ${exit}`;
    const count = num(record.count);
    if (count !== undefined) return plural(count, "result");
    const total = num(record.total_lines);
    if (total !== undefined) return plural(total, "line");
  }
  return "";
}

// summarizeToolCalls produces the static summary shown when a group is
// collapsed: a breakdown by kind plus failure/confirmation counters. It holds
// no animation so a finished turn reads as a calm, static block.
export function summarizeToolCalls(calls: ToolCallLike[]): ToolCallSummary {
  const counts: Record<ToolCallCategory, number> = {
    edit: 0,
    command: 0,
    read: 0,
    workspace: 0,
    other: 0,
  };
  let failed = 0;
  let pending = 0;
  let running = 0;

  for (const call of calls) {
    counts[toolCallCategory(call.name)] += 1;
    const status = resolveToolCallStatus(call);
    if (status === "failed") failed += 1;
    else if (status === "pending_confirmation") pending += 1;
    else if (status === "ongoing") running += 1;
  }

  const parts: string[] = [];
  if (counts.edit) parts.push(plural(counts.edit, "edit"));
  if (counts.command) parts.push(plural(counts.command, "command"));
  if (counts.read) parts.push(plural(counts.read, "read"));
  if (counts.workspace) parts.push(plural(counts.workspace, "workspace"));
  if (counts.other) parts.push(plural(counts.other, "action"));

  return { total: calls.length, details: parts.join(" · "), failed, pending, running };
}
