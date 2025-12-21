import { useEffect, useState } from "react";
import useSWR from "swr";
import { Download, Smartphone, X } from "lucide-react";
import { cn, fetcher } from "../lib/utils";
import {
  buildCurrentOpenInAppDeepLink,
  isMobileWebRuntime,
  OPEN_CHAT_MOBILE_RELEASE_URL,
  resolveOpenInAppWidgetMode,
  type OpenInAppWidgetMode,
} from "../lib/mobile-web";

const DISMISS_KEY_PREFIX = "openchat.openInApp.dismissed:";

type SelfUser = {
  name?: string;
  email?: string;
  is_admin?: boolean;
};

function readDismissed(pathname: string): boolean {
  if (typeof window === "undefined") {
    return false;
  }
  try {
    return window.sessionStorage.getItem(`${DISMISS_KEY_PREFIX}${pathname}`) === "1";
  } catch {
    return false;
  }
}

function writeDismissed(pathname: string): void {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.sessionStorage.setItem(`${DISMISS_KEY_PREFIX}${pathname}`, "1");
  } catch {
    // Ignore storage failures (private mode, disabled storage).
  }
}

// OpenInAppWidget recommends installing / switching to the native Android
// client when a mobile browser is on a useful page. It is shown on public
// shareable surfaces (integration pages, interaction shares) for everyone and
// on the signed-in work surfaces (chats, models, tools, profile) for
// authenticated users. It never renders inside the native app itself.
export function OpenInAppWidget({ className }: { className?: string }) {
  const [mounted, setMounted] = useState(false);
  const [mode, setMode] = useState<OpenInAppWidgetMode | null>(null);
  const [href, setHref] = useState<string | null>(null);
  const [dismissed, setDismissed] = useState(false);
  const [pathname, setPathname] = useState("");

  useEffect(() => {
    setMounted(true);
    if (!isMobileWebRuntime()) {
      return;
    }
    const currentPath = window.location.pathname;
    const resolvedMode = resolveOpenInAppWidgetMode(currentPath);
    if (!resolvedMode) {
      return;
    }
    setPathname(currentPath);
    setMode(resolvedMode);
    setHref(buildCurrentOpenInAppDeepLink());
    setDismissed(readDismissed(currentPath));
  }, []);

  // Only authenticated surfaces (chats/models/tools/profile) need the session
  // probe. Public share pages render for anonymous visitors too, so skip the
  // request there.
  const { data: user } = useSWR<SelfUser>(
    mode === "authenticated" ? "/api/v1/user/self" : null,
    fetcher,
  );

  if (!mounted || !mode || !href || dismissed) {
    return null;
  }

  if (mode === "authenticated" && !user) {
    return null;
  }

  const onDismiss = () => {
    setDismissed(true);
    writeDismissed(pathname);
  };

  return (
    <div
      data-openchat-open-in-app="true"
      className={cn(
        "pointer-events-none fixed inset-x-0 bottom-0 z-50 flex justify-center px-3 pb-[calc(env(safe-area-inset-bottom,0px)+0.75rem)]",
        className,
      )}
    >
      <div className="pointer-events-auto flex w-full max-w-md items-center gap-3 rounded-2xl border border-border/60 bg-background/95 p-3 shadow-lg backdrop-blur supports-[backdrop-filter]:bg-background/80">
        <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-brand/10 text-brand">
          <Smartphone className="h-5 w-5" />
        </span>
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium leading-tight text-foreground">
            Open in the app
          </p>
          <p className="truncate text-xs text-muted-foreground">
            Get the full experience on Android.
          </p>
        </div>
        <a
          href={href}
          className="inline-flex shrink-0 items-center gap-1 rounded-full bg-brand px-3 py-1.5 text-xs font-medium text-brand-foreground transition-colors hover:bg-brand/90"
        >
          <Smartphone className="h-3.5 w-3.5" />
          Open
        </a>
        <a
          href={OPEN_CHAT_MOBILE_RELEASE_URL}
          target="_blank"
          rel="noreferrer"
          aria-label="Get the Android app"
          className="inline-flex shrink-0 items-center gap-1 rounded-full border border-input px-3 py-1.5 text-xs font-medium text-foreground transition-colors hover:bg-accent"
        >
          <Download className="h-3.5 w-3.5" />
          Get app
        </a>
        <button
          type="button"
          onClick={onDismiss}
          aria-label="Dismiss"
          className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          <X className="h-4 w-4" />
        </button>
      </div>
    </div>
  );
}
