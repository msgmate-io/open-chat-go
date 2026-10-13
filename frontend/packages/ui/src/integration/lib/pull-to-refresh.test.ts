import { describe, expect, it } from "vitest";
import {
  dampPullDistance,
  pullProgress,
  shouldTriggerRefresh,
  PULL_REFRESH_MAX,
  PULL_REFRESH_RESISTANCE,
  PULL_REFRESH_THRESHOLD,
} from "./pull-to-refresh";

describe("dampPullDistance", () => {
  it("returns 0 for non-positive or invalid input", () => {
    expect(dampPullDistance(0)).toBe(0);
    expect(dampPullDistance(-10)).toBe(0);
    expect(dampPullDistance(Number.NaN)).toBe(0);
    expect(dampPullDistance(Number.POSITIVE_INFINITY)).toBe(0);
  });

  it("applies the resistance factor", () => {
    expect(dampPullDistance(100)).toBeCloseTo(100 * PULL_REFRESH_RESISTANCE);
  });

  it("never exceeds the cap", () => {
    expect(dampPullDistance(10_000)).toBe(PULL_REFRESH_MAX);
  });

  it("falls back to defaults for invalid tuning values", () => {
    expect(dampPullDistance(100, 0, undefined)).toBeCloseTo(100 * PULL_REFRESH_RESISTANCE);
    expect(dampPullDistance(10_000, 1, 0)).toBe(PULL_REFRESH_MAX);
  });
});

describe("shouldTriggerRefresh", () => {
  it("arms exactly at the threshold", () => {
    expect(shouldTriggerRefresh(PULL_REFRESH_THRESHOLD)).toBe(true);
    expect(shouldTriggerRefresh(PULL_REFRESH_THRESHOLD - 1)).toBe(false);
    expect(shouldTriggerRefresh(0)).toBe(false);
  });

  it("is false for invalid input", () => {
    expect(shouldTriggerRefresh(Number.NaN)).toBe(false);
  });
});

describe("pullProgress", () => {
  it("scales from 0 to 1 at the threshold", () => {
    expect(pullProgress(0)).toBe(0);
    expect(pullProgress(PULL_REFRESH_THRESHOLD / 2)).toBeCloseTo(0.5);
    expect(pullProgress(PULL_REFRESH_THRESHOLD)).toBe(1);
    expect(pullProgress(PULL_REFRESH_THRESHOLD * 3)).toBe(1);
  });
});
