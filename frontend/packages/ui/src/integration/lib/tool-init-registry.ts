import type { ComponentType } from "react";

export interface ToolInitWidgetProps {
  toolName: string;
  value: Record<string, unknown>;
  onChange: (nextValue: Record<string, unknown>) => void;
  helpText?: string;
}

const widgets = new Map<string, ComponentType<ToolInitWidgetProps>>();

/**
 * Privately-owned tool-init UI: integrations register custom widgets for the
 * `ui.widget` kinds their tools declare, so their selectors/protocols stay out
 * of the public package. Unregistered kinds fall back to the schema-driven
 * generic renderer.
 */
export function registerToolInitWidget(kind: string, widget: ComponentType<ToolInitWidgetProps>): void {
  const key = kind.trim().toLowerCase();
  if (key) {
    widgets.set(key, widget);
  }
}

export function resolveToolInitWidget(kind: string | null | undefined): ComponentType<ToolInitWidgetProps> | null {
  const key = (kind || "").trim().toLowerCase();
  if (!key) {
    return null;
  }
  return widgets.get(key) ?? null;
}
