// Mobile-*web* detection and "open in app" deep-link construction.
//
// The native Android client wraps this same frontend in a WebView and exposes
// `window.OpenChatMobileBridge` (see ./mobile-app.ts). When the exact same pages
// are opened in a mobile *browser* (no bridge) we surface an "Open in app"
// affordance that hands the current URL - server plus path - to the native
// client through a custom-scheme deep link. The client adds/selects the matching
// server profile and loads the requested page, so e.g. a shared
// `/interaction/<uuid>` link or a `/chat/<uuid>` URL opens in the app.

import { isMobileAppRuntime } from "./mobile-app";

// Custom URL scheme the Android host registers (see AndroidManifest.xml). The
// payload is carried as query parameters so a single filter covers every page.
export const OPEN_IN_APP_SCHEME = "openchat://open";

// Fallback target when the deep link is not handled by an installed client.
export const OPEN_CHAT_MOBILE_RELEASE_URL =
  "https://github.com/msgmate-io/open-chat-go-mobile/releases/latest";

// "public" pages are shareable/un-authenticated surfaces (integration pages and
// interaction shares) where the button is always useful. "authenticated" pages
// are the signed-in working surfaces (chats, models, tools, profile) where the
// button is only offered to a signed-in user.
export type OpenInAppWidgetMode = "public" | "authenticated";

const PUBLIC_PAGE_PREFIXES = ["/integrations", "/integration/", "/interaction/"];

const AUTHENTICATED_PAGE_PREFIXES = [
  "/chat",
  "/chats",
  "/models",
  "/tools",
  "/profile",
];

// Never offer "open in app" on auth flows, native-only routes, docs or admin
// surfaces: they either make no sense in the app or are already app-internal.
const EXCLUDED_PAGE_PREFIXES = [
  "/login",
  "/sign-up",
  "/signup-request-send",
  "/callback",
  "/email-verification",
  "/mobile",
  "/admin",
  "/docs",
  "/api",
  "/test",
  "/interaction_renderer",
];

function matchesPrefix(pathname: string, prefixes: string[]): boolean {
  return prefixes.some((prefix) => {
    const normalized = prefix.endsWith("/") ? prefix : `${prefix}/`;
    return pathname === prefix || pathname.startsWith(normalized);
  });
}

// isMobileDeviceUserAgent reports whether the UA looks like a phone/tablet.
export function isMobileDeviceUserAgent(userAgent: string): boolean {
  return /Android|iPhone|iPad|iPod|Mobile|Silk|Kindle|Opera Mini|IEMobile/i.test(userAgent);
}

// isMobileWebRuntime is true only on a mobile browser *outside* the native app.
export function isMobileWebRuntime(): boolean {
  if (typeof window === "undefined" || typeof navigator === "undefined") {
    return false;
  }
  if (isMobileAppRuntime()) {
    return false;
  }
  return isMobileDeviceUserAgent(navigator.userAgent ?? "");
}

// resolveOpenInAppWidgetMode classifies a pathname into the widget mode, or
// null when the widget must not be shown at all. It is intentionally pure so it
// can be unit-tested without a DOM.
export function resolveOpenInAppWidgetMode(pathname: string): OpenInAppWidgetMode | null {
  if (!pathname) {
    return null;
  }
  if (matchesPrefix(pathname, EXCLUDED_PAGE_PREFIXES)) {
    return null;
  }
  if (matchesPrefix(pathname, PUBLIC_PAGE_PREFIXES)) {
    return "public";
  }
  if (matchesPrefix(pathname, AUTHENTICATED_PAGE_PREFIXES)) {
    return "authenticated";
  }
  return null;
}

export type ResolvedWebLocation = {
  origin: string;
  path: string;
};

// resolveWebLocation splits a full http(s) URL into the server origin and the
// relative path (including query and hash) the native client should load.
export function resolveWebLocation(url: string): ResolvedWebLocation | null {
  try {
    const parsed = new URL(url);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
      return null;
    }
    return {
      origin: parsed.origin,
      path: `${parsed.pathname}${parsed.search}${parsed.hash}`,
    };
  } catch {
    return null;
  }
}

// buildOpenInAppDeepLink builds the `openchat://` URL for a full page URL.
// Both the whole `url` and the split `server`/`path` are included: the client
// prefers `url` and falls back to the explicit pair for forward compatibility.
export function buildOpenInAppDeepLink(url: string): string | null {
  const location = resolveWebLocation(url);
  if (!location) {
    return null;
  }
  const params = new URLSearchParams();
  params.set("url", url);
  params.set("server", location.origin);
  params.set("path", location.path);
  return `${OPEN_IN_APP_SCHEME}?${params.toString()}`;
}

// buildCurrentOpenInAppDeepLink is the browser convenience wrapper around the
// active location; returns null during SSR or on a non-http(s) document.
export function buildCurrentOpenInAppDeepLink(): string | null {
  if (typeof window === "undefined" || !window.location) {
    return null;
  }
  return buildOpenInAppDeepLink(window.location.href);
}
