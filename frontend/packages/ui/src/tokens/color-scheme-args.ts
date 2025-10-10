import type { CSSProperties } from "react";

/** Fallback hex values when DOM read is unavailable (SSR/build). Keep in sync with semantic.css. */
export const lightColorSchemeDefaults = {
  background: "#f8f9fb",
  foreground: "#18181b",
  card: "#ffffff",
  "card-foreground": "#18181b",
  popover: "#ffffff",
  "popover-foreground": "#18181b",
  primary: "#18181b",
  "primary-foreground": "#ffffff",
  secondary: "#eef0f4",
  "secondary-foreground": "#18181b",
  muted: "#f1f3f6",
  "muted-foreground": "#64748b",
  accent: "#e8eaef",
  "accent-foreground": "#171717",
  destructive: "#ef4444",
  "destructive-foreground": "#fafafa",
  brand: "#4ade80",
  "brand-foreground": "#171717",
  success: "#16a34a",
  "success-foreground": "#fafafa",
  border: "#dfe3ea",
  input: "#d5dbe4",
  ring: "#18181b",
  sidebar: "#ffffff",
  "sidebar-foreground": "#18181b",
  "sidebar-primary": "#18181b",
  "sidebar-primary-foreground": "#ffffff",
  "sidebar-accent": "#f1f3f6",
  "sidebar-accent-foreground": "#18181b",
  "sidebar-border": "#dfe3ea",
  "sidebar-ring": "#3b82f6",
  radius: 0.6,
} as const;

export const darkColorSchemeDefaults = {
  background: "#191e24",
  foreground: "#a6adbb",
  card: "#1f252d",
  "card-foreground": "#a6adbb",
  popover: "#1f252d",
  "popover-foreground": "#a6adbb",
  primary: "#a6adbb",
  "primary-foreground": "#18181b",
  secondary: "#232b35",
  "secondary-foreground": "#a6adbb",
  muted: "#1c222a",
  "muted-foreground": "#8b95a8",
  accent: "#2d3540",
  "accent-foreground": "#a6adbb",
  destructive: "#7f1d1d",
  "destructive-foreground": "#fafafa",
  brand: "#4ade80",
  "brand-foreground": "#18181b",
  success: "#22c55e",
  "success-foreground": "#fafafa",
  border: "#2f3844",
  input: "#2f3844",
  ring: "#a6adbb",
  sidebar: "#191e24",
  "sidebar-foreground": "#a6adbb",
  "sidebar-primary": "#a6adbb",
  "sidebar-primary-foreground": "#18181b",
  "sidebar-accent": "#232b35",
  "sidebar-accent-foreground": "#a6adbb",
  "sidebar-border": "#2f3844",
  "sidebar-ring": "#3b82f6",
  radius: 0.6,
} as const;

export type ColorSchemeControlArgs = {
  [K in keyof typeof darkColorSchemeDefaults]: (typeof darkColorSchemeDefaults)[K];
};

export type StorybookTheme = "light" | "dark";

export const cssVarKeys = Object.keys(darkColorSchemeDefaults).filter(
  (k) => k !== "radius"
) as Exclude<keyof ColorSchemeControlArgs, "radius">[];

export function getColorSchemeBaseline(
  theme: StorybookTheme
): ColorSchemeControlArgs {
  if (typeof document === "undefined") {
    return theme === "light"
      ? { ...lightColorSchemeDefaults }
      : { ...darkColorSchemeDefaults };
  }
  return readColorSchemeBaseline(theme);
}

function rgbToHex(rgb: string): string | null {
  const match = rgb.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
  if (!match) return null;
  const hex = (n: string) => Number(n).toString(16).padStart(2, "0");
  return `#${hex(match[1])}${hex(match[2])}${hex(match[3])}`;
}

function readCssColorVar(probe: HTMLElement, cssVar: string): string {
  const el = document.createElement("span");
  el.style.color = `var(${cssVar})`;
  probe.appendChild(el);
  const computed = getComputedStyle(el).color;
  probe.removeChild(el);
  return rgbToHex(computed) ?? computed;
}

/** Read live token values from `semantic.css` for the given theme. */
export function readColorSchemeBaseline(
  theme: StorybookTheme
): ColorSchemeControlArgs {
  const fallback =
    theme === "light" ? lightColorSchemeDefaults : darkColorSchemeDefaults;
  if (typeof document === "undefined") {
    return { ...fallback };
  }

  const probe = document.createElement("div");
  probe.setAttribute("aria-hidden", "true");
  probe.style.cssText =
    "position:absolute;visibility:hidden;pointer-events:none;width:0;height:0;overflow:hidden";
  if (theme === "dark") probe.classList.add("dark");
  document.body.appendChild(probe);

  const out = { ...fallback } as Record<string, string | number>;
  for (const key of cssVarKeys) {
    out[key] = readCssColorVar(probe, `--${key}`) as string;
  }

  const radiusRaw = getComputedStyle(probe).getPropertyValue("--radius").trim();
  const radiusPx = parseFloat(radiusRaw);
  if (!Number.isNaN(radiusPx)) {
    out.radius = radiusRaw.endsWith("rem")
      ? radiusPx
      : radiusPx / 16;
  }

  document.body.removeChild(probe);
  return out as ColorSchemeControlArgs;
}

/** Keep manual control tweaks when switching light/dark. */
export function mergeColorSchemeArgs(
  current: ColorSchemeControlArgs,
  previousBaseline: ColorSchemeControlArgs,
  nextBaseline: ColorSchemeControlArgs
): ColorSchemeControlArgs {
  const merged = { ...nextBaseline };
  for (const key of cssVarKeys) {
    if (current[key] !== previousBaseline[key]) {
      merged[key] = current[key];
    }
  }
  if (current.radius !== previousBaseline.radius) {
    merged.radius = current.radius;
  }
  return merged;
}

/** Only inject inline vars the user changed vs the active theme baseline. */
export function colorSchemeOverrideStyle(
  args: ColorSchemeControlArgs,
  baseline: ColorSchemeControlArgs
): CSSProperties {
  const style: Record<string, string> = {};
  for (const key of cssVarKeys) {
    if (args[key] !== baseline[key]) {
      style[`--${key}`] = String(args[key]);
    }
  }
  if (args.radius !== baseline.radius) {
    style["--radius"] = `${args.radius}rem`;
  }
  return style as CSSProperties;
}

export const colorSchemeArgTypes = {
  background: { control: "color", table: { category: "Surfaces" } },
  foreground: { control: "color", table: { category: "Surfaces" } },
  card: { control: "color", table: { category: "Surfaces" } },
  "card-foreground": { control: "color", table: { category: "Surfaces" } },
  popover: { control: "color", table: { category: "Surfaces" } },
  "popover-foreground": { control: "color", table: { category: "Surfaces" } },
  primary: { control: "color", table: { category: "Actions" } },
  "primary-foreground": { control: "color", table: { category: "Actions" } },
  secondary: { control: "color", table: { category: "Actions" } },
  "secondary-foreground": { control: "color", table: { category: "Actions" } },
  brand: { control: "color", table: { category: "Actions" } },
  "brand-foreground": { control: "color", table: { category: "Actions" } },
  muted: { control: "color", table: { category: "Feedback" } },
  "muted-foreground": { control: "color", table: { category: "Feedback" } },
  accent: { control: "color", table: { category: "Feedback" } },
  "accent-foreground": { control: "color", table: { category: "Feedback" } },
  destructive: { control: "color", table: { category: "Feedback" } },
  "destructive-foreground": { control: "color", table: { category: "Feedback" } },
  success: { control: "color", table: { category: "Feedback" } },
  "success-foreground": { control: "color", table: { category: "Feedback" } },
  border: { control: "color", table: { category: "Chrome" } },
  input: { control: "color", table: { category: "Chrome" } },
  ring: { control: "color", table: { category: "Chrome" } },
  sidebar: { control: "color", table: { category: "Sidebar" } },
  "sidebar-foreground": { control: "color", table: { category: "Sidebar" } },
  "sidebar-primary": { control: "color", table: { category: "Sidebar" } },
  "sidebar-primary-foreground": { control: "color", table: { category: "Sidebar" } },
  "sidebar-accent": { control: "color", table: { category: "Sidebar" } },
  "sidebar-accent-foreground": { control: "color", table: { category: "Sidebar" } },
  "sidebar-border": { control: "color", table: { category: "Sidebar" } },
  "sidebar-ring": { control: "color", table: { category: "Sidebar" } },
  radius: {
    control: { type: "range", min: 0, max: 1.5, step: 0.05 },
    table: { category: "Layout" },
  },
} as const;
