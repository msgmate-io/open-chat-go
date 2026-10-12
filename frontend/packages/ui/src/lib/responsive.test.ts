import { describe, expect, it } from "vitest"
import {
  AUTH_BODY_CLASSES,
  AUTH_CONTENT_CLASSES,
  AUTH_PANEL_CLASSES,
  AUTH_SHELL_CLASSES,
  AUTH_THEME_TOGGLE_CLASSES,
  SMALL_SCREEN_MAX_HEIGHT,
  SMALL_SCREEN_MAX_WIDTH,
  clampCssSize,
  isSmallScreenViewport,
} from "./responsive"

describe("isSmallScreenViewport", () => {
  it("treats normal desktop viewports as non-small", () => {
    expect(isSmallScreenViewport(1440, 900)).toBe(false)
    expect(isSmallScreenViewport(1280, 800)).toBe(false)
  })

  it("treats narrow viewports as small", () => {
    expect(isSmallScreenViewport(320, 900)).toBe(true)
    expect(isSmallScreenViewport(SMALL_SCREEN_MAX_WIDTH - 1, 1200)).toBe(true)
  })

  it("treats short / near-square viewports as small even when they are wide", () => {
    // The native-app screenshots are roughly 540x600 CSS px: slightly wider
    // than tall, so a width-only breakpoint would miss them.
    expect(isSmallScreenViewport(540, 600)).toBe(true)
    expect(isSmallScreenViewport(900, SMALL_SCREEN_MAX_HEIGHT - 1)).toBe(true)
  })

  it("treats the breakpoint boundary as non-small", () => {
    expect(isSmallScreenViewport(SMALL_SCREEN_MAX_WIDTH, SMALL_SCREEN_MAX_HEIGHT)).toBe(false)
  })

  it("ignores non-finite inputs instead of flipping to compact", () => {
    expect(isSmallScreenViewport(Number.NaN, 900)).toBe(false)
    expect(isSmallScreenViewport(900, Number.POSITIVE_INFINITY)).toBe(false)
  })
})

describe("clampCssSize", () => {
  it("clamps a fixed pixel size to a share of the smaller viewport axis", () => {
    expect(clampCssSize(330, 0.5)).toBe("min(330px, 50vmin)")
    expect(clampCssSize(480, 0.6)).toBe("min(480px, 60vmin)")
  })

  it("rounds the pixel size and clamps the ratio into [0, 1]", () => {
    expect(clampCssSize(240.4, 2)).toBe("min(240px, 100vmin)")
    expect(clampCssSize(240.6, -1)).toBe("min(241px, 0vmin)")
  })
})

describe("auth shell layout classes", () => {
  it("makes the shell exactly one viewport tall so it can scroll internally", () => {
    // The native layout wrapper clips page overflow; a `min-height` shell would
    // grow past the viewport and hide the form + native server selector.
    expect(AUTH_SHELL_CLASSES.split(/\s+/)).toContain("h-full")
    expect(AUTH_SHELL_CLASSES.split(/\s+/)).toContain("overflow-y-auto")
    expect(AUTH_SHELL_CLASSES).not.toContain("min-h-full")
    // Desktop keeps the fixed, non-scrolling side-by-side layout.
    expect(AUTH_SHELL_CLASSES).toContain("md:overflow-hidden")
  })

  it("top-aligns the auth content on small viewports and centers it on desktop", () => {
    for (const classes of [AUTH_BODY_CLASSES, AUTH_PANEL_CLASSES, AUTH_CONTENT_CLASSES]) {
      const tokens = classes.split(/\s+/)
      expect(tokens).toContain("justify-start")
      expect(tokens).toContain("md:justify-center")
    }
  })

  it("floats the theme toggle to the header corner on small viewports only", () => {
    const tokens = AUTH_THEME_TOGGLE_CLASSES.split(/\s+/)
    // Pinned to the top-right so it lines up with the page header instead of
    // taking its own row below it.
    expect(tokens).toContain("absolute")
    expect(tokens).toContain("right-0")
    expect(tokens).toContain("top-0")
    // Desktop keeps the toggle in the panel flow, right-aligned.
    expect(tokens).toContain("md:static")
    expect(tokens).toContain("md:w-full")
  })
})
