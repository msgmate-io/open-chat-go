import { useCallback, useState } from "react";

import {
  PANEL_ZOOM_MAX_LEVEL,
  PANEL_ZOOM_MIN_LEVEL,
  clampPanelZoomLevel,
  defaultPanelZoomLevel,
  panelScaleForLevel,
} from "../lib/panel-zoom";

// usePanelZoom mirrors useTerminalZoom for embedded/framed content: it exposes a
// bounded zoom level plus the +/- handlers and the matching scale factor.
export function usePanelZoom() {
  const [level, setLevel] = useState<number>(() => defaultPanelZoomLevel());

  const shrink = useCallback(() => {
    setLevel((prev) => clampPanelZoomLevel(prev - 1));
  }, []);

  const grow = useCallback(() => {
    setLevel((prev) => clampPanelZoomLevel(prev + 1));
  }, []);

  return {
    level,
    scale: panelScaleForLevel(level),
    shrink,
    grow,
    canShrink: level > PANEL_ZOOM_MIN_LEVEL,
    canGrow: level < PANEL_ZOOM_MAX_LEVEL,
  };
}
