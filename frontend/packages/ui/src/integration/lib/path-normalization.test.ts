import { describe, expect, it } from "vitest"
import {
  normalizePathname,
  stripPathQueryAndHash,
  trimTrailingSlashes,
} from "./pinned-integrations"

// Regression tests for ci#737 / CodeQL alert #29 (js/polynomial-redos): the
// sidebar path normalization used `value.split("?")[0].split("#")[0].replace(
// /\/+$/, "")`, whose backtracking trailing-slash quantifier is super-linear on
// long runs of "/". The helpers below replace it and must stay linear while
// behaving identically for real routes.

describe("trimTrailingSlashes", () => {
  it("strips every trailing slash and nothing else", () => {
    expect(trimTrailingSlashes("/integrations/k8s/pods///")).toBe("/integrations/k8s/pods")
    expect(trimTrailingSlashes("/integrations/k8s/pods")).toBe("/integrations/k8s/pods")
    expect(trimTrailingSlashes("///")).toBe("")
    expect(trimTrailingSlashes("/")).toBe("")
    expect(trimTrailingSlashes("")).toBe("")
    // Interior slashes are preserved (only a trailing run is trimmed).
    expect(trimTrailingSlashes("/a//b/c/")).toBe("/a//b/c")
  })

  it("handles non-string input the way the old helper did", () => {
    expect(trimTrailingSlashes(undefined as unknown as string)).toBe("")
    expect(trimTrailingSlashes(null as unknown as string)).toBe("")
    expect(trimTrailingSlashes(42 as unknown as string)).toBe("")
  })

  it("stays linear on a pathological slash run", () => {
    const run = "/".repeat(200_000)
    const hostile = `${run}x${run}`
    const started = performance.now()
    expect(trimTrailingSlashes(hostile)).toBe(hostile.slice(0, 200_001))
    // A backtracking `/\/+$/` is quadratic-ish here; the single backward scan
    // is ~1ms. The 2s ceiling keeps slow shared CI runners green while still
    // failing loudly if a super-linear implementation creeps back in.
    expect(performance.now() - started).toBeLessThan(2_000)
  })
})

describe("stripPathQueryAndHash", () => {
  it("cuts at the earliest query or hash", () => {
    expect(stripPathQueryAndHash("/integrations/k8s/pods?ns=default#top")).toBe(
      "/integrations/k8s/pods",
    )
    expect(stripPathQueryAndHash("/integrations/k8s/pods#top?ns=default")).toBe(
      "/integrations/k8s/pods",
    )
    expect(stripPathQueryAndHash("/integrations/k8s/pods")).toBe("/integrations/k8s/pods")
    expect(stripPathQueryAndHash("?ns=default")).toBe("")
    expect(stripPathQueryAndHash("")).toBe("")
    expect(stripPathQueryAndHash(undefined as unknown as string)).toBe("")
  })
})

describe("normalizePathname", () => {
  it("matches the previous normalizePath behaviour for real routes", () => {
    const cases: [unknown, string][] = [
      ["/integrations/kubernetes/clusters/", "/integrations/kubernetes/clusters"],
      ["/integrations/k8s/pods?ns=default#top", "/integrations/k8s/pods"],
      ["/integrations", "/integrations"],
      ["/integrations/", "/integrations"],
      ["/integrations/kubernetes", "/integrations/kubernetes"],
      ["/chat", "/chat"],
      ["/integrations/k8s//pods/", "/integrations/k8s//pods"],
      ["", ""],
    ]
    for (const [input, expected] of cases) {
      expect(normalizePathname(input)).toBe(expected)
    }
  })

  it("tolerates raw / non-string input", () => {
    expect(normalizePathname(undefined)).toBe("")
    expect(normalizePathname(null)).toBe("")
    expect(normalizePathname(42)).toBe("")
  })

  it("is a stable comparison key (idempotent)", () => {
    const key = normalizePathname("/integrations/k8s/pods///?ns=default#top")
    expect(key).toBe("/integrations/k8s/pods")
    expect(normalizePathname(key)).toBe(key)
  })

  it("agrees with a split/filter/join reference on generated routes", () => {
    // Independent linear reference (split/pop/join) so a bug in the
    // charCodeAt scan cannot mask itself.
    const reference = (input: string): string => {
      const cuts = [input.indexOf("?"), input.indexOf("#")].filter((index) => index >= 0)
      const cut = cuts.length ? Math.min(...cuts) : input.length
      const parts = input.slice(0, cut).split("/")
      while (parts.length > 1 && parts[parts.length - 1] === "") {
        parts.pop()
      }
      return parts.join("/")
    }
    const segments = ["integrations", "kubernetes", "clusters", "pods", "k8s", "a", ""]
    for (let index = 0; index < 200; index += 1) {
      const parts: string[] = []
      const depth = (index % 5) + 1
      for (let level = 0; level < depth; level += 1) {
        parts.push(segments[(index * (level + 3)) % segments.length])
      }
      const suffix = index % 3 === 0 ? "?ns=default" : index % 3 === 1 ? "#top" : ""
      const trailing = "/".repeat(index % 4)
      const input = `/${parts.join("/")}${trailing}${suffix}`
      const expected = reference(input)
      expect(normalizePathname(input), input).toBe(expected === "/" ? "" : expected)
    }
  })

  it("stays fast on hostile input (no polynomial backtracking)", () => {
    const hostile = `${"/".repeat(200_000)}?x=${"?".repeat(200_000)}`
    const started = performance.now()
    for (let index = 0; index < 50; index += 1) {
      normalizePathname(hostile)
      normalizePathname(hostile.slice(0, 200_000))
    }
    expect(performance.now() - started).toBeLessThan(2_000)
  })
})
