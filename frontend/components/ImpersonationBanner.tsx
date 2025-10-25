import { useState } from "react";
import useSWR from "swr";
import { Button } from "@open-chat-go/ui";
import { fetcher } from "@/lib/utils";

type SelfUser = {
  name?: string;
  username?: string;
  impersonator?: { name?: string; username?: string };
};

// ImpersonationBanner renders a prominent marker whenever the current session is
// an admin impersonating another user. It is inert unless the account-management
// integration started an impersonation session (which sets `impersonator` on
// /api/v1/user/self).
export function ImpersonationBanner() {
  const { data: self, mutate } = useSWR<SelfUser>("/api/v1/user/self", fetcher);
  const [working, setWorking] = useState(false);

  if (!self?.impersonator) {
    return null;
  }

  const adminName = self.impersonator.name || self.impersonator.username || "admin";
  const targetName = self.name || self.username || "another user";

  const stop = async () => {
    setWorking(true);
    try {
      const response = await fetch("/api/v1/integrations/account_management/impersonation/stop", {
        method: "POST",
      });
      if (!response.ok) {
        return;
      }
      await mutate();
      window.location.href = "/chat";
    } finally {
      setWorking(false);
    }
  };

  return (
    <div className="sticky top-0 z-50 flex flex-wrap items-center justify-between gap-3 bg-amber-500 px-4 py-2 text-black">
      <span className="text-sm font-medium">
        Admin view: you are viewing and acting as <strong>{targetName}</strong> (impersonated by {adminName}). Actions
        are performed on this user&apos;s account.
      </span>
      <Button variant="outline" className="rounded-full shrink-0 border-black/40" disabled={working} onClick={stop}>
        Return to admin
      </Button>
    </div>
  );
}
