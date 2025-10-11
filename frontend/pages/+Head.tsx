// https://vike.dev/Head

import React from "react";
import logoUrl from "../assets/logo.png";

const themeBootstrapScript = `(() => {
  try {
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
      <link rel="icon" type="image/png" href={logoUrl} />
      <link rel="apple-touch-icon" href={logoUrl} />
    </>
  );
}
