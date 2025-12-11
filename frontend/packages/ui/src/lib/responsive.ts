// Viewport heuristics shared by the responsive surfaces in the app.
//
// The native-app screenshots that motivated these helpers are near-square
// phones with a very short viewport (e.g. ~540x600 CSS px): they are only
// slightly narrower than they are tall, so a pure width breakpoint misclassifies
// them as "desktop". We therefore treat a viewport as "small" when it is either
// narrow (width) *or* short (height).
export const SMALL_SCREEN_MAX_WIDTH = 640
export const SMALL_SCREEN_MAX_HEIGHT = 640

export function isSmallScreenViewport(width: number, height: number): boolean {
  if (!Number.isFinite(width) || !Number.isFinite(height)) {
    return false
  }
  return width < SMALL_SCREEN_MAX_WIDTH || height < SMALL_SCREEN_MAX_HEIGHT
}

// isCompactRuntime reports whether the app is running in a compact (native /
// small-screen) runtime. It is the runtime probe shared by the zoom models:
// native WebViews set data-openchat-runtime=mobile, and the browser fallback
// matches the same 768px breakpoint the responsive layouts use.
export function isCompactRuntime(): boolean {
  if (typeof window === "undefined" || typeof document === "undefined") {
    return false
  }
  if (document.documentElement.getAttribute("data-openchat-runtime") === "mobile") {
    return true
  }
  if (typeof window.matchMedia === "function") {
    return window.matchMedia("(max-width: 768px)").matches
  }
  return false
}

// clampCssSize returns a CSS `min()` expression that keeps an element at its
// preferred pixel size but never larger than a share of the smaller viewport
// axis (vmin). This keeps the landing/login hero art from overflowing very
// short / near-square viewports where a fixed pixel logo is taller than the
// screen.
export function clampCssSize(preferredPx: number, vminRatio: number): string {
  const safePx = Math.max(0, Math.round(preferredPx))
  const ratio = Math.min(1, Math.max(0, vminRatio))
  const vminPercent = Number((ratio * 100).toFixed(2))
  return `min(${safePx}px, ${vminPercent}vmin)`
}
