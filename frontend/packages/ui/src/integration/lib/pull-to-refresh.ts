// Pure math for the native pull-to-refresh gesture. Kept free of DOM/React so
// it can be unit-tested and so the component only wires events.
//
// The gesture is intentionally damped (like native social feeds): the further
// the user drags, the less the indicator follows the finger, and the indicator
// can never travel further than PULL_REFRESH_MAX.

// Pull distance (in px) that arms the refresh on release.
export const PULL_REFRESH_THRESHOLD = 64;

// Hard cap for the indicator travel so an over-drag does not feel unstuck.
export const PULL_REFRESH_MAX = 112;

// Visual dampening applied to the raw finger travel. A value in (0, 1] pulls
// the indicator at a fraction of the finger so short flicks still feel direct.
export const PULL_REFRESH_RESISTANCE = 0.55;

// dampPullDistance maps a raw finger travel (already measured only while the
// scroll container is at the top) onto the indicator offset.
export function dampPullDistance(
  rawDistance: number,
  resistance: number = PULL_REFRESH_RESISTANCE,
  max: number = PULL_REFRESH_MAX,
): number {
  if (!Number.isFinite(rawDistance) || rawDistance <= 0) {
    return 0;
  }
  const safeResistance = Number.isFinite(resistance) && resistance > 0 ? resistance : PULL_REFRESH_RESISTANCE;
  const safeMax = Number.isFinite(max) && max > 0 ? max : PULL_REFRESH_MAX;
  return Math.min(rawDistance * safeResistance, safeMax);
}

// shouldTriggerRefresh reports whether releasing at `distance` should arm the
// refresh request. A little slack below the threshold keeps it forgiving.
export function shouldTriggerRefresh(
  distance: number,
  threshold: number = PULL_REFRESH_THRESHOLD,
): boolean {
  if (!Number.isFinite(distance)) {
    return false;
  }
  return distance >= threshold;
}

// pullProgress is a 0..1 value useful for scaling/fading the indicator while
// the user drags (0 = hidden, 1 = fully armed).
export function pullProgress(
  distance: number,
  threshold: number = PULL_REFRESH_THRESHOLD,
): number {
  if (!Number.isFinite(distance) || distance <= 0) {
    return 0;
  }
  const safeThreshold = Number.isFinite(threshold) && threshold > 0 ? threshold : PULL_REFRESH_THRESHOLD;
  return Math.min(distance / safeThreshold, 1);
}
