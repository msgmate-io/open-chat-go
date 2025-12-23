// https://vike.dev/Head

import React from "react";
import logoUrl from "../assets/logo.png";

const themeBootstrapScript = `(() => {
  try {
    // Resolve the native-runtime flag before the first paint/hydration so the
    // app shell does not render the desktop layout for a frame and then flip to
    // the mobile/native layout. Mirrors isMobileAppRuntime() in
    // @open-chat-go/ui: Android WebView that exposes the OpenChat mobile bridge.
    const userAgent = navigator.userAgent || "";
    const isAndroidWebView = /Android/i.test(userAgent) && /\\bwv\\b/i.test(userAgent);
    const isNative =
      isAndroidWebView &&
      typeof window.OpenChatMobileBridge !== "undefined" &&
      typeof window.OpenChatMobileBridge.isMobileApp === "function" &&
      Boolean(window.OpenChatMobileBridge.isMobileApp());
    document.documentElement.setAttribute("data-openchat-runtime", isNative ? "mobile" : "web");

    const params = new URLSearchParams(window.location.search);
    const fromParams =
      params.get("embedTheme") ||
      params.get("theme") ||
      params.get("colorScheme") ||
      params.get("color_scheme") ||
      params.get("appearance");

    const isTheme = (value) => value === "dark" || value === "light";

    const readCookie = (name) => {
      const cookie = document.cookie
        .split("; ")
        .find((entry) => entry.startsWith(name + "="));
      if (!cookie) return "";
      return cookie.slice(name.length + 1);
    };

    let theme = "";

    if (isTheme(fromParams || "")) {
      theme = fromParams;
    } else {
      const persisted = readCookie("theme-store");
      if (persisted) {
        try {
          const parsed = JSON.parse(decodeURIComponent(persisted));
          const stored = parsed && parsed.state ? parsed.state.theme : "";
          if (isTheme(stored || "")) {
            theme = stored;
          }
        } catch {
          // Ignore malformed persisted state and use fallback.
        }
      }
    }

    if (!theme) {
      theme = window.self !== window.top ? "light" : "dark";
    }

    window.__OPENCHAT_INITIAL_THEME = theme;
  } catch {
    // Never block rendering on theme bootstrap failure.
  }
})();`;

export default function HeadDefault() {
  return (
    <>
      <script dangerouslySetInnerHTML={{ __html: themeBootstrapScript }} />
      <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover" />
      <link rel="icon" type="image/png" href={logoUrl} />
      <link rel="apple-touch-icon" href={logoUrl} />
    </>
  );
}
