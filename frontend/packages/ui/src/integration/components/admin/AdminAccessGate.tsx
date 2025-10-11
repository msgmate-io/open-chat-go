import type { ReactNode } from "react";
import { navigate } from "vike/client/router";
import { ShieldAlert } from "lucide-react";
import { Button, Text, TextTypes } from "@open-chat-go/ui";
import { useCurrentUser } from "../../hooks/use-current-user";

export function AdminAccessGate({ children }: { children: ReactNode }) {
  const { data: user, isLoading, error } = useCurrentUser();

  if (isLoading) {
    return (
      <div className="space-y-4">
        <div className="animate-pulse">
          <div className="h-8 w-1/4 rounded bg-muted" />
          <div className="mt-2 h-4 w-1/2 rounded bg-muted" />
        </div>
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          {[0, 1, 2].map((i) => (
            <div key={i} className="h-24 animate-pulse rounded-xl bg-muted" />
          ))}
        </div>
      </div>
    );
  }

  if (error || !user) {
    return (
      <div className="flex flex-col items-center justify-center gap-4 py-16 text-center">
        <ShieldAlert className="size-10 text-muted-foreground" />
        <Text type={TextTypes.Heading5} tag="h1" bold>
          Sign in required
        </Text>
        <Text type={TextTypes.Body5} color="muted" className="max-w-md">
          You must be logged in to access the admin area.
        </Text>
        <Button onClick={() => navigate("/login")}>Go to login</Button>
      </div>
    );
  }

  if (!user.is_admin) {
    return (
      <div className="flex flex-col items-center justify-center gap-4 py-16 text-center">
        <ShieldAlert className="size-10 text-destructive" />
        <Text type={TextTypes.Heading5} tag="h1" bold>
          Admin access required
        </Text>
        <Text type={TextTypes.Body5} color="muted" className="max-w-md">
          This area is restricted to administrators. Contact an admin if you need access.
        </Text>
        <Button variant="outline" onClick={() => navigate("/profile")}>
          Back to profile
        </Button>
      </div>
    );
  }

  return <>{children}</>;
}
