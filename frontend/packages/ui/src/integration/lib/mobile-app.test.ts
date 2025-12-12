import { describe, expect, it } from "vitest";
import {
  isMobileServerAuthenticated,
  resolveMobileServerAuthSelection,
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
