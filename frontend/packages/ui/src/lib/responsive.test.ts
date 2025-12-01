import { describe, expect, it } from "vitest"
import {
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
