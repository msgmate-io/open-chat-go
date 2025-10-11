export const TERMINAL_FONT_SIZE_DEFAULT = 13;
export const TERMINAL_FONT_SIZE_MIN = 7;
export const TERMINAL_FONT_SIZE_MAX = 22;
export const TERMINAL_FONT_SIZE_STEP = 1;

export const TERMINAL_ZOOM_MIN_LEVEL = -6;
export const TERMINAL_ZOOM_MAX_LEVEL = 6;

export function clampTerminalZoomLevel(level: number): number {
  return Math.max(TERMINAL_ZOOM_MIN_LEVEL, Math.min(TERMINAL_ZOOM_MAX_LEVEL, level));
}

export function terminalFontSizeForLevel(level: number): number {
  const clamped = clampTerminalZoomLevel(level);
  return Math.max(
    TERMINAL_FONT_SIZE_MIN,
    Math.min(TERMINAL_FONT_SIZE_MAX, TERMINAL_FONT_SIZE_DEFAULT + clamped * TERMINAL_FONT_SIZE_STEP)
  );
}

export function isCompactTerminalRuntime(): boolean {
  if (typeof window === "undefined" || typeof document === "undefined") {
    return false;
  }
  if (document.documentElement.getAttribute("data-openchat-runtime") === "mobile") {
    return true;
  }
  if (typeof window.matchMedia === "function") {
    return window.matchMedia("(max-width: 768px)").matches;
  }
  return false;
}

export function defaultTerminalZoomLevel(): number {
  return isCompactTerminalRuntime() ? -3 : 0;
}