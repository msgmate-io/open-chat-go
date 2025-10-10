export type ToolCatalogRow = {
  name: string;
  requires_init?: boolean;
  init_schema?: Record<string, unknown>;
};

export type ToolInitUIConfig = {
  widget?: string;
  helpText?: string;
};

export type ToolInitDescriptor = {
  configuredName: string;
  resolvedName: string;
  schema: Record<string, unknown>;
  ui?: ToolInitUIConfig;
};

const DEFAULT_INIT_SCHEMA: Record<string, unknown> = {
  type: "object",
  properties: {},
  required: [],
};

export function normalizeConfiguredToolName(toolName: string): string {
  const trimmed = String(toolName || "").trim();
  if (!trimmed.includes(":")) return trimmed;
  if (trimmed.startsWith("interaction_start:") || trimmed.startsWith("interaction_complete:")) {
    const parts = trimmed.split(":", 2);
    if (parts.length === 2) {
      return String(parts[1] || "").trim();
    }
  }
  return trimmed;
}

export function resolveRequiredToolInitDescriptors(
  selectedTools: string[],
  catalogRows: ToolCatalogRow[],
): ToolInitDescriptor[] {
  const catalog = new Map<string, ToolCatalogRow>();
  for (const row of catalogRows) {
    const name = String(row?.name || "").trim();
    if (!name) continue;
    catalog.set(name, row);
  }

  const dedup = new Set<string>();
  const out: ToolInitDescriptor[] = [];

  for (const configuredRaw of selectedTools) {
    const configuredName = String(configuredRaw || "").trim();
    if (!configuredName) continue;

    const resolvedName = normalizeConfiguredToolName(configuredName);
    const tool = catalog.get(configuredName) || catalog.get(resolvedName);
    if (!tool || tool.requires_init !== true) {
      continue;
    }

    if (dedup.has(configuredName)) {
      continue;
    }
    dedup.add(configuredName);

    const schema =
      tool.init_schema && typeof tool.init_schema === "object"
        ? tool.init_schema
        : DEFAULT_INIT_SCHEMA;

    out.push({
      configuredName,
      resolvedName,
      schema,
      ui: getToolInitUIConfig(schema),
    });
  }

  return out;
}

function getToolInitUIConfig(schema: Record<string, unknown>): ToolInitUIConfig | undefined {
  const raw = schema["x-openchat-ui"];
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return undefined;
  }
  const ui = raw as Record<string, unknown>;

  const widget = typeof ui.widget === "string" ? ui.widget.trim() : "";
  const helpText = typeof ui.help_text === "string" ? ui.help_text.trim() : "";
  if (!widget && !helpText) {
    return undefined;
  }
  return {
    widget: widget || undefined,
    helpText: helpText || undefined,
  };
}

export function asToolInitMap(value: unknown): Record<string, Record<string, unknown>> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return {};
  }
  const out: Record<string, Record<string, unknown>> = {};
  for (const [key, raw] of Object.entries(value)) {
    if (!key.trim()) continue;
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) continue;
    out[key] = raw as Record<string, unknown>;
  }
  return out;
}

export function pickToolInitForTools(
  descriptors: ToolInitDescriptor[],
  source: Record<string, Record<string, unknown>>,
): Record<string, Record<string, unknown>> {
  const out: Record<string, Record<string, unknown>> = {};
  for (const descriptor of descriptors) {
    const fromConfigured = source[descriptor.configuredName];
    const fromResolved = source[descriptor.resolvedName];
    const chosen = fromConfigured || fromResolved;
    if (!chosen || typeof chosen !== "object" || Array.isArray(chosen)) {
      continue;
    }
    out[descriptor.configuredName] = chosen;
  }
  return out;
}

export function getMissingRequiredToolInitFields(
  descriptors: ToolInitDescriptor[],
  values: Record<string, Record<string, unknown>>,
): string[] {
  const missing: string[] = [];
  for (const descriptor of descriptors) {
    const requiredRaw = descriptor.schema.required;
    const required = Array.isArray(requiredRaw)
      ? requiredRaw.map((item) => String(item || "").trim()).filter(Boolean)
      : [];
    if (required.length === 0) {
      continue;
    }

    const payload = values[descriptor.configuredName] ?? values[descriptor.resolvedName] ?? {};
    for (const fieldName of required) {
      const hasValue = Object.prototype.hasOwnProperty.call(payload, fieldName);
      if (!hasValue) {
        missing.push(`${descriptor.configuredName}.${fieldName}`);
      }
    }
  }
  return missing;
}

export function buildToolInitPayloadForSelectedTools(
  selectedTools: string[],
  source: Record<string, Record<string, unknown>>,
): Record<string, Record<string, unknown>> {
  const out: Record<string, Record<string, unknown>> = {};
  for (const configuredRaw of selectedTools) {
    const configuredName = String(configuredRaw || "").trim();
    if (!configuredName) {
      continue;
    }
    const resolvedName = normalizeConfiguredToolName(configuredName);
    const fromConfigured = source[configuredName];
    const fromResolved = source[resolvedName];
    const chosen = fromConfigured || fromResolved;
    if (chosen && typeof chosen === "object" && !Array.isArray(chosen)) {
      out[configuredName] = chosen;
      continue;
    }
    out[configuredName] = {};
  }
  return out;
}
