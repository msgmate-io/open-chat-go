import { useThemeStore } from "@open-chat-go/ui";
import "@fontsource/dm-sans/400.css";
import "@fontsource/dm-sans/500.css";
import "@fontsource/dm-sans/600.css";
import "@fontsource/dm-sans/700.css";
import "@fontsource/dm-sans/400-italic.css";
import "@fontsource/signika-negative/400.css";
import "@fontsource/signika-negative/500.css";
import "@fontsource/signika-negative/600.css";
import "@fontsource/signika-negative/700.css";
import "./style.css";
import "./tailwind.css";
import "@/integrations/extensions.gen";
import React, { useEffect, useRef, useState } from "react";
import { applyTheme, isThemeName } from "@/lib/theme";
import { applyDocumentTitle } from "@open-chat-go/ui";
import { OfflineIndicator } from "@/components/OfflineIndicator";
import { ImpersonationBanner } from "@/components/ImpersonationBanner";
import { MobileNativeSafeAreaSpacer } from "@/components/MobileNativeSafeAreaSpacer";
import { isMobileAppRuntime } from "@open-chat-go/ui";
import { navigate } from "vike/client/router";

function resolveThemeOverrideFromUrl(): "light" | "dark" | "" {
  if (typeof window === "undefined") {
    return "";
  }

  const params = new URLSearchParams(window.location.search);
  const embedTheme = params.get("embedTheme");
  if (typeof embedTheme === "string" && isThemeName(embedTheme)) {
    return embedTheme;
  }

  const candidates = [
    params.get("theme"),
    params.get("colorScheme"),
    params.get("color_scheme"),
    params.get("appearance"),
  ];

  for (const candidate of candidates) {
    if (typeof candidate === "string" && isThemeName(candidate)) {
      return candidate;
    }
  }

  return "";
}

function resolveInitialThemeFromBootstrap(): "light" | "dark" | "" {
  if (typeof window === "undefined") {
    return "";
  }

  const initialTheme = (
    window as Window & { __OPENCHAT_INITIAL_THEME?: string }
  ).__OPENCHAT_INITIAL_THEME;

  return typeof initialTheme === "string" && isThemeName(initialTheme)
    ? initialTheme
    : "";
}

export default function LayoutDefault({ children }: { children: React.ReactNode }) {
  const theme = useThemeStore((state) => state.theme);
  const safeTheme = isThemeName(theme) ? theme : "dark";
  const [isMobileRuntime, setIsMobileRuntime] = useState(false);
  // The bootstrap value is only used to paint the first frame without a flash.
  // Re-applying it on every store change would override the user's selection.
  const hasAppliedInitialTheme = useRef(false);

  useEffect(() => {
    const forcedTheme = resolveThemeOverrideFromUrl();
    if (forcedTheme !== "") {
      applyTheme(forcedTheme);
      return;
    }

    if (!hasAppliedInitialTheme.current) {
      hasAppliedInitialTheme.current = true;
      const initialTheme = resolveInitialThemeFromBootstrap();
      if (initialTheme !== "") {
        applyTheme(initialTheme);
        return;
      }
    }

    if (typeof window !== "undefined" && window.self !== window.top) {
      applyTheme("light");
      return;
    }

    applyTheme(safeTheme);
  }, [safeTheme]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    const updateTitle = () => applyDocumentTitle(window.location.pathname);
    updateTitle();
    window.addEventListener("popstate", updateTitle);
    window.addEventListener("hashchange", updateTitle);
    return () => {
      window.removeEventListener("popstate", updateTitle);
      window.removeEventListener("hashchange", updateTitle);
    };
  }, []);

  useEffect(() => {
    setIsMobileRuntime(isMobileAppRuntime());
  }, []);

  useEffect(() => {
    if (typeof document === "undefined") {
      return;
    }

    document.documentElement.setAttribute(
      "data-openchat-runtime",
      isMobileRuntime ? "mobile" : "web",
    );
  }, [isMobileRuntime]);

  useEffect(() => {
    if (typeof window === "undefined" || !isMobileRuntime) {
      return;
    }

    const html = document.documentElement;
    const body = document.body;
    const prevHtmlOverflow = html.style.overflow;
    const prevBodyOverflow = body.style.overflow;

    html.style.overflow = "hidden";
    body.style.overflow = "hidden";

    return () => {
      html.style.overflow = prevHtmlOverflow;
      body.style.overflow = prevBodyOverflow;
    };
  }, [isMobileRuntime]);

  useEffect(() => {
    if (typeof window === "undefined" || !isMobileRuntime) {
      return;
    }

    const root = document.documentElement;
    const updateVisualKeyboardInset = () => {
      const viewport = window.visualViewport;
      if (!viewport) {
        root.style.setProperty("--openchat-keyboard-bottom-visual", "0px");
        return;
      }
      const keyboardInset = Math.max(0, window.innerHeight - viewport.height - viewport.offsetTop);
      root.style.setProperty("--openchat-keyboard-bottom-visual", `${Math.round(keyboardInset)}px`);
    };

    updateVisualKeyboardInset();
    window.addEventListener("resize", updateVisualKeyboardInset);
    window.visualViewport?.addEventListener("resize", updateVisualKeyboardInset);
    window.visualViewport?.addEventListener("scroll", updateVisualKeyboardInset);

    return () => {
      window.removeEventListener("resize", updateVisualKeyboardInset);
      window.visualViewport?.removeEventListener("resize", updateVisualKeyboardInset);
      window.visualViewport?.removeEventListener("scroll", updateVisualKeyboardInset);
      root.style.setProperty("--openchat-keyboard-bottom-visual", "0px");
    };
  }, [isMobileRuntime]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }

    const onDocumentClick = (event: MouseEvent) => {
      if (event.defaultPrevented || event.button !== 0) {
        return;
      }
      if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
        return;
      }

      const target = event.target as HTMLElement | null;
      const anchor = target?.closest("a") as HTMLAnchorElement | null;
      if (!anchor) {
        return;
      }
      if (anchor.target && anchor.target !== "_self") {
        return;
      }
      if (anchor.hasAttribute("download") || anchor.getAttribute("rel") === "external") {
        return;
      }

      const href = anchor.getAttribute("href") || "";
      if (!href || href.startsWith("mailto:") || href.startsWith("tel:")) {
        return;
      }

      const url = new URL(anchor.href, window.location.href);
      if (url.origin !== window.location.origin) {
        return;
      }

      const next = `${url.pathname}${url.search}${url.hash}`;
      const current = `${window.location.pathname}${window.location.search}${window.location.hash}`;
      if (next === current) {
        return;
      }

      event.preventDefault();
      navigate(next);
    };

    document.addEventListener("click", onDocumentClick);
    return () => document.removeEventListener("click", onDocumentClick);
  }, []);

  const rootClassName = isMobileRuntime
    ? "h-dvh overflow-hidden bg-background text-foreground flex flex-col"
    : "min-h-screen h-dvh bg-background text-foreground flex flex-col";

  const contentClassName = isMobileRuntime
    ? "min-h-0 flex-1 overflow-hidden"
    : "min-h-0 flex-1 overflow-auto";

  return (
    <div className={rootClassName}>
      <ImpersonationBanner />
      <OfflineIndicator />
      <MobileNativeSafeAreaSpacer />
      <div className={contentClassName}>{children}</div>
    </div>
  );
}
