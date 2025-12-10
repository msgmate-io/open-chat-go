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

// Landing / login "auth shell" layout classes.
//
// The native app (and the native layout wrapper) set `overflow: hidden` on
// <html>/<body> and clip the page container, so the auth shell itself must be
// exactly one viewport tall for its own scroll container to work. A shell that
// only sets `min-height` grows past the viewport instead and gets clipped,
// leaving the lower form controls - and the native server selector - below the
// fold and unreachable.
//
// On small/native viewports the content is top-aligned so the theme toggle sits
// directly under the header and the form starts at the top of the panel;
// desktop keeps the side-by-side, vertically centered layout next to the
// typewriter pane.
export const AUTH_SHELL_CLASSES =
  "relative flex h-full flex-col overflow-y-auto bg-secondary md:h-full md:min-h-0 md:overflow-hidden md:bg-transparent"

export const AUTH_BODY_CLASSES =
  "flex md:flex-row flex-col items-center justify-start md:justify-center content-center h-auto min-h-0 md:h-full flex-1"

export const AUTH_PANEL_CLASSES =
  "flex flex-col flex-grow items-center justify-start md:justify-center content-center bg-secondary text-secondary-foreground w-full md:w-1/3 h-auto md:h-full"

export const AUTH_CONTENT_CLASSES =
  "flex flex-col items-center justify-start md:justify-center content-center w-full flex-grow p-4 pb-20 md:pb-4"
