export const THEMES = ["dark", "light"] as const;
export type ThemeName = (typeof THEMES)[number];

export function isThemeName(value: string): value is ThemeName {
  return THEMES.includes(value as ThemeName);
}

/** Apply the light/dark theme: toggles the `.dark` class used by the semantic tokens. */
export function applyTheme(theme: ThemeName) {
  if (typeof document === "undefined") {
    return;
  }

  const root = document.documentElement;
  root.classList.toggle("dark", theme === "dark");
  root.setAttribute("data-theme", theme);
  document.getElementById("theme-root")?.setAttribute("data-theme", theme);
}
