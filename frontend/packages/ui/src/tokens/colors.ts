/**
 * Semantic color tokens — single source of truth for names and documentation.
 * CSS values live in `src/styles/semantic.css`; keep both in sync when editing.
 */
export type ColorToken = {
  name: string
  cssVar: string
  description: string
  tailwind: string
}

export const semanticColorTokens: ColorToken[] = [
  { name: "background", cssVar: "--background", description: "Page / panel background", tailwind: "bg-background" },
  { name: "foreground", cssVar: "--foreground", description: "Primary text", tailwind: "text-foreground" },
  { name: "card", cssVar: "--card", description: "Card surfaces", tailwind: "bg-card" },
  { name: "card-foreground", cssVar: "--card-foreground", description: "Text on cards", tailwind: "text-card-foreground" },
  { name: "popover", cssVar: "--popover", description: "Dropdowns, menus", tailwind: "bg-popover" },
  { name: "popover-foreground", cssVar: "--popover-foreground", description: "Text in popovers", tailwind: "text-popover-foreground" },
  { name: "primary", cssVar: "--primary", description: "Primary actions & links", tailwind: "bg-primary" },
  { name: "primary-foreground", cssVar: "--primary-foreground", description: "Text on primary", tailwind: "text-primary-foreground" },
  { name: "secondary", cssVar: "--secondary", description: "Secondary surfaces (chat list)", tailwind: "bg-secondary" },
  { name: "secondary-foreground", cssVar: "--secondary-foreground", description: "Text on secondary", tailwind: "text-secondary-foreground" },
  { name: "muted", cssVar: "--muted", description: "Muted backgrounds", tailwind: "bg-muted" },
  { name: "muted-foreground", cssVar: "--muted-foreground", description: "Secondary / hint text", tailwind: "text-muted-foreground" },
  { name: "accent", cssVar: "--accent", description: "Hover / selected states", tailwind: "bg-accent" },
  { name: "accent-foreground", cssVar: "--accent-foreground", description: "Text on accent", tailwind: "text-accent-foreground" },
  { name: "destructive", cssVar: "--destructive", description: "Errors & delete actions", tailwind: "bg-destructive" },
  { name: "destructive-foreground", cssVar: "--destructive-foreground", description: "Text on destructive", tailwind: "text-destructive-foreground" },
  { name: "brand", cssVar: "--brand", description: "Marketing CTA (landing)", tailwind: "bg-brand" },
  { name: "brand-foreground", cssVar: "--brand-foreground", description: "Text on brand CTA", tailwind: "text-brand-foreground" },
  { name: "success", cssVar: "--success", description: "Success states & badges", tailwind: "bg-success" },
  { name: "success-foreground", cssVar: "--success-foreground", description: "Text on success", tailwind: "text-success-foreground" },
  { name: "border", cssVar: "--border", description: "Borders & dividers", tailwind: "border-border" },
  { name: "input", cssVar: "--input", description: "Input borders", tailwind: "border-input" },
  { name: "ring", cssVar: "--ring", description: "Focus rings", tailwind: "ring-ring" },
  { name: "sidebar", cssVar: "--sidebar", description: "Sidebar background", tailwind: "bg-sidebar" },
  { name: "sidebar-foreground", cssVar: "--sidebar-foreground", description: "Sidebar text", tailwind: "text-sidebar-foreground" },
  { name: "sidebar-primary", cssVar: "--sidebar-primary", description: "Sidebar active item", tailwind: "bg-sidebar-primary" },
  { name: "sidebar-primary-foreground", cssVar: "--sidebar-primary-foreground", description: "Text on sidebar active item", tailwind: "text-sidebar-primary-foreground" },
  { name: "sidebar-accent", cssVar: "--sidebar-accent", description: "Sidebar hover", tailwind: "bg-sidebar-accent" },
  { name: "sidebar-accent-foreground", cssVar: "--sidebar-accent-foreground", description: "Text on sidebar hover", tailwind: "text-sidebar-accent-foreground" },
  { name: "sidebar-border", cssVar: "--sidebar-border", description: "Sidebar borders", tailwind: "border-sidebar-border" },
  { name: "sidebar-ring", cssVar: "--sidebar-ring", description: "Sidebar focus rings", tailwind: "ring-sidebar-ring" },
]

export const radiusToken = {
  name: "radius",
  cssVar: "--radius",
  description: "Default border radius",
  tailwind: "rounded-md",
} as const
