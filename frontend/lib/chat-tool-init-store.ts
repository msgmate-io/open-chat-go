type ToolInitPayload = Record<string, Record<string, unknown>>;

const pendingByKey = new Map<string, ToolInitPayload>();

export function buildPendingToolInitKey(contactToken: string, modelTitle: string): string {
  return `${String(contactToken || "").trim()}::${String(modelTitle || "").trim()}`;
}

export function getPendingToolInit(contactToken: string, modelTitle: string): ToolInitPayload | null {
  const key = buildPendingToolInitKey(contactToken, modelTitle);
  return pendingByKey.get(key) ?? null;
}

export function setPendingToolInit(contactToken: string, modelTitle: string, payload: ToolInitPayload): void {
  const key = buildPendingToolInitKey(contactToken, modelTitle);
  pendingByKey.set(key, payload);
}

export function clearPendingToolInit(contactToken: string, modelTitle: string): void {
  const key = buildPendingToolInitKey(contactToken, modelTitle);
  pendingByKey.delete(key);
}
