import { describe, expect, it } from "vitest";
import {
  buildOpenInAppDeepLink,
  isMobileDeviceUserAgent,
  OPEN_IN_APP_SCHEME,
  resolveOpenInAppWidgetMode,
  resolveWebLocation,
} from "./mobile-web";

describe("isMobileDeviceUserAgent", () => {
  it("detects common mobile platforms", () => {
    expect(
      isMobileDeviceUserAgent(
        "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120 Mobile Safari/537.36",
      ),
    ).toBe(true);
    expect(isMobileDeviceUserAgent("Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)")).toBe(
      true,
    );
  });

  it("rejects desktop user agents", () => {
    expect(
      isMobileDeviceUserAgent(
        "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120 Safari/537.36",
      ),
    ).toBe(false);
  });
});

describe("resolveOpenInAppWidgetMode", () => {
  it("treats integration and interaction pages as public", () => {
    expect(resolveOpenInAppWidgetMode("/integrations")).toBe("public");
    expect(resolveOpenInAppWidgetMode("/integrations/git/workspaces")).toBe("public");
    expect(resolveOpenInAppWidgetMode("/interaction/abc-123")).toBe("public");
  });

  it("treats signed-in work surfaces as authenticated", () => {
    expect(resolveOpenInAppWidgetMode("/chat/uuid")).toBe("authenticated");
    expect(resolveOpenInAppWidgetMode("/chats")).toBe("authenticated");
    expect(resolveOpenInAppWidgetMode("/models")).toBe("authenticated");
    expect(resolveOpenInAppWidgetMode("/tools")).toBe("authenticated");
    expect(resolveOpenInAppWidgetMode("/profile")).toBe("authenticated");
  });

  it("never shows on excluded routes", () => {
    expect(resolveOpenInAppWidgetMode("/login")).toBeNull();
    expect(resolveOpenInAppWidgetMode("/sign-up")).toBeNull();
    expect(resolveOpenInAppWidgetMode("/callback")).toBeNull();
    expect(resolveOpenInAppWidgetMode("/mobile/share")).toBeNull();
    expect(resolveOpenInAppWidgetMode("/admin")).toBeNull();
    expect(resolveOpenInAppWidgetMode("/docs")).toBeNull();
    expect(resolveOpenInAppWidgetMode("/")).toBeNull();
  });
});

describe("resolveWebLocation", () => {
  it("splits origin from path, query and hash", () => {
    expect(resolveWebLocation("https://msgmate.io/chat/abc?x=1#y")).toEqual({
      origin: "https://msgmate.io",
      path: "/chat/abc?x=1#y",
    });
  });

  it("keeps non-default ports", () => {
    expect(resolveWebLocation("http://localhost:1984/interaction/1")?.origin).toBe(
      "http://localhost:1984",
    );
  });

  it("rejects non-http(s) URLs", () => {
    expect(resolveWebLocation("openchat://open")).toBeNull();
    expect(resolveWebLocation("not a url")).toBeNull();
  });
});

describe("buildOpenInAppDeepLink", () => {
  it("carries url, server and path", () => {
    const href = buildOpenInAppDeepLink("https://msgmate.io/interaction/abc?tab=1");
    expect(href).not.toBeNull();
    const params = new URLSearchParams(href!.slice(href!.indexOf("?") + 1));
    expect(href!.startsWith(OPEN_IN_APP_SCHEME)).toBe(true);
    expect(params.get("url")).toBe("https://msgmate.io/interaction/abc?tab=1");
    expect(params.get("server")).toBe("https://msgmate.io");
    expect(params.get("path")).toBe("/interaction/abc?tab=1");
  });

  it("returns null for invalid URLs", () => {
    expect(buildOpenInAppDeepLink("mailto:test@example.com")).toBeNull();
  });
});
