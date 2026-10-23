import { useMemo, type ReactNode } from "react";
import { cn } from "../../lib/utils";

// ToolCallDetail renders a tool call's parameters, result and error in a compact,
// consistent way across every tool: parameters become a neat key/value overview
// (one row per argument, multi-line values shown as a readable text block) and
// the result is shown as a readable text block (or key/value overview when the
// tool returns a structured object). It is shared by the core tool-call rows and
// by integration-owned message bodies (e.g. the msgmate coder timeline) so both
// render tool output identically.

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function isScalar(value: unknown): value is string | number | boolean | null | undefined {
  return value === null || value === undefined || typeof value !== "object";
}

function formatScalar(value: unknown): string {
  if (value === null) return "null";
  if (value === undefined) return "";
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

// Some tools marshal their structured result as a JSON string; decode it so the
// overview can show its fields instead of one escaped blob.
function decodeJsonString(value: unknown): unknown {
  if (typeof value !== "string") {
    return value;
  }
  const trimmed = value.trim();
  if (!trimmed || (trimmed[0] !== "{" && trimmed[0] !== "[")) {
    return value;
  }
  try {
    return JSON.parse(trimmed);
  } catch {
    return value;
  }
}

function isEmptyValue(value: unknown): boolean {
  if (value === undefined || value === null) return true;
  if (typeof value === "string") return value.trim().length === 0;
  if (Array.isArray(value)) return value.length === 0;
  if (isPlainObject(value)) return Object.keys(value).length === 0;
  return false;
}

function isBlockString(value: string): boolean {
  return value.includes("\n") || value.length > 120;
}

const BLOCK_CLASSES =
  "scrollbar-hidden max-h-56 overflow-auto whitespace-pre-wrap break-words rounded-md border border-border/60 bg-background/60 p-2 font-mono text-[11px] leading-5";
const INLINE_CLASSES = "rounded bg-muted/60 px-1 py-px font-mono text-[11px]";

// ValueText renders a single value: long/multi-line strings become a readable
// text block, short scalars stay inline, and structured values recurse into the
// key/value overview.
export function ToolValueText({ value, depth = 0 }: { value: unknown; depth?: number }): ReactNode {
  const decoded = decodeJsonString(value);

  if (isEmptyValue(decoded)) {
    return null;
  }

  if (typeof decoded === "string") {
    return isBlockString(decoded) ? (
      <pre className={BLOCK_CLASSES}>{decoded}</pre>
    ) : (
      <code className={INLINE_CLASSES}>{decoded}</code>
    );
  }

  if (Array.isArray(decoded)) {
    if (decoded.every(isScalar)) {
      return <code className={INLINE_CLASSES}>{decoded.map(formatScalar).join(", ")}</code>;
    }
    if (depth >= 2) {
      return (
        <pre className={BLOCK_CLASSES}>{JSON.stringify(decoded, null, 2)}</pre>
      );
    }
    return (
      <div className="space-y-1">
        {decoded.map((item, index) => (
          <div key={index} className="rounded border border-border/50 bg-muted/20 px-1.5 py-1">
            <ToolValueText value={item} depth={depth + 1} />
          </div>
        ))}
      </div>
    );
  }

  if (isPlainObject(decoded)) {
    if (depth >= 2) {
      return <pre className={BLOCK_CLASSES}>{JSON.stringify(decoded, null, 2)}</pre>;
    }
    return <ToolKeyValueTable data={decoded} depth={depth} />;
  }

  return <code className={INLINE_CLASSES}>{formatScalar(decoded)}</code>;
}

// ToolKeyValueTable renders an object as a neat two-column key/value overview.
export function ToolKeyValueTable({
  data,
  depth = 0,
}: {
  data: Record<string, unknown>;
  depth?: number;
}): ReactNode {
  const entries = useMemo(
    () => Object.entries(data).filter(([, value]) => !isEmptyValue(value)),
    [data],
  );
  if (entries.length === 0) {
    return null;
  }
  return (
    <div className="overflow-hidden rounded-md border border-border/60">
      <div className="divide-y divide-border/50">
        {entries.map(([key, value]) => (
          <div key={key} className="flex flex-col gap-1 px-2 py-1.5 sm:flex-row sm:gap-2">
            <div className="shrink-0 break-words font-mono text-[11px] font-medium text-muted-foreground sm:w-32">
              {key}
            </div>
            <div className="min-w-0 flex-1">
              <ToolValueText value={value} depth={depth + 1} />
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

// ToolCallDetail is the combined parameters/result/error view used to expand a
// tool call. Sections with no value are omitted.
export function ToolCallDetail({
  arguments: args,
  result,
  error,
  className,
}: {
  arguments?: unknown;
  result?: unknown;
  error?: unknown;
  className?: string;
}): ReactNode {
  const hasArgs = !isEmptyValue(args);
  const hasResult = !isEmptyValue(result);
  const hasError = !isEmptyValue(error);

  if (!hasArgs && !hasResult && !hasError) {
    return null;
  }

  return (
    <div className={cn("space-y-2", className)}>
      {hasArgs ? (
        <div>
          <div className="mb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
            Parameters
          </div>
          <ToolValueText value={args} />
        </div>
      ) : null}
      {hasResult ? (
        <div>
          <div className="mb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
            Result
          </div>
          <ToolValueText value={result} />
        </div>
      ) : null}
      {hasError ? (
        <div>
          <div className="mb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
            Error
          </div>
          <pre className={cn(BLOCK_CLASSES, "border-rose-500/40 bg-rose-500/5 text-rose-700 dark:text-rose-300")}>
            {typeof error === "string" ? error : formatScalar(error)}
          </pre>
        </div>
      ) : null}
    </div>
  );
}
