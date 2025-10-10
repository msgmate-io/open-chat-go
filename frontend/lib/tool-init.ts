// Re-export shim: the tool-init helpers now live in `@open-chat-go/ui` so
// integration-owned pages can reuse them. Kept for existing app imports.
export {
  normalizeConfiguredToolName,
  resolveRequiredToolInitDescriptors,
  asToolInitMap,
  pickToolInitForTools,
  getMissingRequiredToolInitFields,
  buildToolInitPayloadForSelectedTools,
} from "@open-chat-go/ui";
export type {
  ToolCatalogRow,
  ToolInitUIConfig,
  ToolInitDescriptor,
} from "@open-chat-go/ui";