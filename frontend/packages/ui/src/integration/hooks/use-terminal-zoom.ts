import { useCallback, useState } from "react";

import {
  TERMINAL_ZOOM_MAX_LEVEL,
  TERMINAL_ZOOM_MIN_LEVEL,
  clampTerminalZoomLevel,
  defaultTerminalZoomLevel,
  terminalFontSizeForLevel,
} from "../lib/terminal-zoom";

export function useTerminalZoom() {
  const [level, setLevel] = useState<number>(() => defaultTerminalZoomLevel());

  const shrink = useCallback(() => {
    setLevel((prev) => clampTerminalZoomLevel(prev - 1));
  }, []);

  const grow = useCallback(() => {
    setLevel((prev) => clampTerminalZoomLevel(prev + 1));
  }, []);

  return {
    level,
    fontSize: terminalFontSizeForLevel(level),
    shrink,
    grow,
    canShrink: level > TERMINAL_ZOOM_MIN_LEVEL,
    canGrow: level < TERMINAL_ZOOM_MAX_LEVEL,
  };
}