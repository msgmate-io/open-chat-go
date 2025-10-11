import { useEffect, useState } from "react";
import { isDeviceOnlineRuntime } from "@open-chat-go/ui";

export function OfflineIndicator() {
  const [isOffline, setIsOffline] = useState(false);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }

    const syncStatus = () => {
      setIsOffline(!isDeviceOnlineRuntime());
    };

    syncStatus();

    window.addEventListener("online", syncStatus);
    window.addEventListener("offline", syncStatus);
    const interval = window.setInterval(syncStatus, 2000);
    return () => {
      window.removeEventListener("online", syncStatus);
      window.removeEventListener("offline", syncStatus);
      window.clearInterval(interval);
    };
  }, []);

  if (!isOffline) {
    return null;
  }

  return (
    <div
      className="pointer-events-none fixed right-3 z-[100] rounded-full border border-amber-500/60 bg-amber-100/95 px-3 py-1 text-xs font-semibold text-amber-900 shadow-sm"
      style={{ top: "calc(var(--openchat-safe-top, 0px) + 8px)" }}
    >
      Offline
    </div>
  );
}
