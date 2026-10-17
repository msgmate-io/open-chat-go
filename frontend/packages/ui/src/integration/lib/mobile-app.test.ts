import { describe, expect, it } from "vitest";
import {
  isMobileServerAuthenticated,
  isMobileServerAuthenticatedWithProbe,
  probeActiveMobileServerAuthenticated,
  recordMobileServerAuthProbe,
  resolveMobileServerAuthSelection,
  resolveMobileServerAuthenticated,
  type MobileAppConfig,
  type MobileServerConfig,
} from "./mobile-app";

function server(overrides: Partial<MobileServerConfig> & { id: string }): MobileServerConfig {
  return {
    label: overrides.id,
    upstreamUrl: `https://${overrides.id}.example`,
    routeApiWsToUpstream: true,
    runtimeEnv: {},
    ...overrides,
  };
}

function config(servers: MobileServerConfig[], activeServerId: string): MobileAppConfig {
  return { servers, activeServerId };
}

describe("isMobileServerAuthenticated", () => {
  it("is true only for an explicit authenticated flag", () => {
    expect(isMobileServerAuthenticated(server({ id: "a", isAuthenticated: true }))).toBe(true);
    expect(isMobileServerAuthenticated(server({ id: "b", isAuthenticated: false }))).toBe(false);
    expect(isMobileServerAuthenticated(server({ id: "c" }))).toBe(false);
    expect(isMobileServerAuthenticated(null)).toBe(false);
    expect(isMobileServerAuthenticated(undefined)).toBe(false);
  });
});

describe("isMobileServerAuthenticatedWithProbe", () => {
  it("keeps a native positive hint even if the probe has not resolved", () => {
    expect(
      isMobileServerAuthenticatedWithProbe(server({ id: "hosted", isAuthenticated: true }), "hosted", false),
    ).toBe(true);
  });

  it("trusts the live probe for the active server when the native hint missed", () => {
    // This is the ci.msgmate.io regression: the native cookie-jar read missed
    // but the server really had a valid session, so the selector must show Auth.
    expect(isMobileServerAuthenticatedWithProbe(server({ id: "ci" }), "ci", true)).toBe(true);
  });

  it("does not mark a non-active server from the active probe", () => {
    expect(isMobileServerAuthenticatedWithProbe(server({ id: "other" }), "ci", true)).toBe(false);
  });

  it("stays false when neither the native hint nor the probe authenticated", () => {
    expect(isMobileServerAuthenticatedWithProbe(server({ id: "ci" }), "ci", false)).toBe(false);
    expect(isMobileServerAuthenticatedWithProbe(null, "ci", true)).toBe(false);
  });
});

describe("resolveMobileServerAuthenticated", () => {
  it("trusts the native positive hint", () => {
    expect(resolveMobileServerAuthenticated(server({ id: "a", isAuthenticated: true }), {})).toBe(true);
  });

  it("uses the per-server probe result, not another server's", () => {
    expect(resolveMobileServerAuthenticated(server({ id: "ci" }), { ci: true })).toBe(true);
    expect(resolveMobileServerAuthenticated(server({ id: "other" }), { ci: true })).toBe(false);
  });

  it("stays false when neither the hint nor the probe authenticated", () => {
    expect(resolveMobileServerAuthenticated(server({ id: "ci" }), {})).toBe(false);
    expect(resolveMobileServerAuthenticated(server({ id: "ci" }), { ci: false })).toBe(false);
    expect(resolveMobileServerAuthenticated(null, { ci: true })).toBe(false);
  });
});

describe("recordMobileServerAuthProbe", () => {
  it("records an authenticated result per server without touching the others", () => {
    const next = recordMobileServerAuthProbe({ other: true }, "ci", true);
    expect(next).toEqual({ other: true, ci: true });
  });

  it("keeps a known-authenticated server sticky on a transient failed probe", () => {
    // A switch/runtime restart can race the probe; the server that was
    // authenticated seconds ago must not blink back to "No auth".
    const next = recordMobileServerAuthProbe({ ci: true }, "ci", false);
    expect(next).toEqual({ ci: true });
  });

  it("downgrades only once the failed probe is the latest known state", () => {
    const next = recordMobileServerAuthProbe({}, "ci", false);
    expect(next).toEqual({ ci: false });
  });

  it("returns the same reference when nothing changes", () => {
    const state = { ci: true };
    expect(recordMobileServerAuthProbe(state, "ci", true)).toBe(state);
    expect(recordMobileServerAuthProbe(state, "", false)).toBe(state);
  });
});

describe("probeActiveMobileServerAuthenticated", () => {
  it("returns false outside a browser runtime", async () => {
    await expect(probeActiveMobileServerAuthenticated()).resolves.toBe(false);
  });
});

describe("resolveMobileServerAuthSelection", () => {
  it("keeps the active server when it is authenticated", () => {
    const selection = resolveMobileServerAuthSelection(
      config(
        [server({ id: "local", routeApiWsToUpstream: false }), server({ id: "hosted", isAuthenticated: true })],
        "hosted",
      ),
    );

    expect(selection.active?.id).toBe("hosted");
    expect(selection.activeIsAuthenticated).toBe(true);
    expect(selection.recommended?.id).toBe("hosted");
    expect(selection.authenticated.map((entry) => entry.id)).toEqual(["hosted"]);
  });

  it("recommends a previously authenticated server when the active one is not", () => {
    const selection = resolveMobileServerAuthSelection(
      config(
        [server({ id: "local", routeApiWsToUpstream: false }), server({ id: "hosted", isAuthenticated: true })],
        "local",
      ),
    );

    expect(selection.active?.id).toBe("local");
    expect(selection.activeIsAuthenticated).toBe(false);
    expect(selection.recommended?.id).toBe("hosted");
  });

  it("returns no recommendation when nothing is authenticated", () => {
    const selection = resolveMobileServerAuthSelection(
      config(
        [server({ id: "local", routeApiWsToUpstream: false }), server({ id: "hosted" })],
        "hosted",
      ),
    );

    expect(selection.activeIsAuthenticated).toBe(false);
    expect(selection.authenticated).toEqual([]);
    expect(selection.recommended).toBeNull();
  });

  it("falls back to the first server when the active id is unknown", () => {
    const selection = resolveMobileServerAuthSelection(
      config([server({ id: "local", routeApiWsToUpstream: false })], "missing"),
    );

    expect(selection.active?.id).toBe("local");
    expect(selection.recommended).toBeNull();
  });
});
