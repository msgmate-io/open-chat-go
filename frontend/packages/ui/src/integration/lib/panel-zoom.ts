// Zoom model for embedded / framed content (eg the admin Asynq queue monitor).
//
// It mirrors the terminal zoom model (see ./terminal-zoom): a discrete level
// drives a scale factor and the +/- controls are bounded by the same style of
// clamp. Embedded dashboards are wider than a phone viewport, so the compact
// (native / small-screen) runtime starts zoomed out so the whole frame fits.

import { isCompactRuntime } from "../../lib/responsive";

export const PANEL_ZOOM_MIN_LEVEL = -5;
export const PANEL_ZOOM_MAX_LEVEL = 5;
export const PANEL_ZOOM_STEP = 0.1;
export const PANEL_ZOOM_MIN_SCALE = 0.5;
export const PANEL_ZOOM_MAX_SCALE = 1.5;

export function clampPanelZoomLevel(level: number): number {
  return Math.max(PANEL_ZOOM_MIN_LEVEL, Math.min(PANEL_ZOOM_MAX_LEVEL, level));
}

export function panelScaleForLevel(level: number): number {
  const clamped = clampPanelZoomLevel(level);
  const scale = 1 + clamped * PANEL_ZOOM_STEP;
  return Math.max(PANEL_ZOOM_MIN_SCALE, Math.min(PANEL_ZOOM_MAX_SCALE, scale));
}

// defaultPanelZoomLevel zooms out on compact runtimes so a wide embedded
// dashboard is visible without horizontal scrolling on first open.
export function defaultPanelZoomLevel(): number {
  return isCompactRuntime() ? -2 : 0;
}
