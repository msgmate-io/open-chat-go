import { isMobileAppRuntime } from "@open-chat-go/ui";
import { useEffect, useState } from "react";

export function MobileNativeSafeAreaSpacer() {
  const [isMobileRuntime, setIsMobileRuntime] = useState(false);

  useEffect(() => {
    setIsMobileRuntime(isMobileAppRuntime());
  }, []);

  if (!isMobileRuntime) {
    return null;
  }

  return (
    <div
      aria-hidden="true"
      className="pointer-events-none w-full shrink-0"
      style={{
        height: "var(--openchat-safe-top, 0px)",
        backgroundColor: "#111111",
      }}
    />
  );
}
