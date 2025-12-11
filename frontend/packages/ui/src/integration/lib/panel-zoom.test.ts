import { describe, expect, it } from "vitest";
import {
  PANEL_ZOOM_MAX_LEVEL,
  PANEL_ZOOM_MAX_SCALE,
  PANEL_ZOOM_MIN_LEVEL,
  PANEL_ZOOM_MIN_SCALE,
  clampPanelZoomLevel,
  panelScaleForLevel,
} from "./panel-zoom";

describe("clampPanelZoomLevel", () => {
  it("keeps levels inside the supported range", () => {
    expect(clampPanelZoomLevel(0)).toBe(0);
    expect(clampPanelZoomLevel(-3)).toBe(-3);
    expect(clampPanelZoomLevel(99)).toBe(PANEL_ZOOM_MAX_LEVEL);
    expect(clampPanelZoomLevel(-99)).toBe(PANEL_ZOOM_MIN_LEVEL);
  });
});

describe("panelScaleForLevel", () => {
  it("maps level 0 to the natural scale", () => {
    expect(panelScaleForLevel(0)).toBe(1);
  });

  it("zooms out for negative levels and in for positive levels", () => {
    expect(panelScaleForLevel(-2)).toBeCloseTo(0.8);
    expect(panelScaleForLevel(2)).toBeCloseTo(1.2);
  });

  it("clamps the scale to the supported bounds", () => {
    expect(panelScaleForLevel(-100)).toBe(PANEL_ZOOM_MIN_SCALE);
    expect(panelScaleForLevel(100)).toBe(PANEL_ZOOM_MAX_SCALE);
  });

  it("stays monotonic across the level range", () => {
    let previous = panelScaleForLevel(PANEL_ZOOM_MIN_LEVEL);
    for (let level = PANEL_ZOOM_MIN_LEVEL + 1; level <= PANEL_ZOOM_MAX_LEVEL; level += 1) {
      const current = panelScaleForLevel(level);
      expect(current).toBeGreaterThanOrEqual(previous);
      previous = current;
    }
  });
});
