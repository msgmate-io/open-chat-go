import { Bell } from "lucide-react";
import { Badge, Button } from "@open-chat-go/ui";
import { cn } from "@/lib/utils";
import { useNotificationsIntegrationEnabled, useUnreadNotificationCount } from "@/lib/notifications";

/**
 * Top-bar bell that links to the in-app notification panel. It only renders when
 * the notifications integration is enabled and highlights in yellow with a count
 * badge while there are unread notifications.
 */
export function NotificationsBell({ navigateTo }: { navigateTo: (to: string) => void }) {
  const enabled = useNotificationsIntegrationEnabled();
  const { count } = useUnreadNotificationCount(enabled);

  if (!enabled) {
    return null;
  }

  const hasUnread = count > 0;
  const label = hasUnread
    ? `${count} unread notification${count === 1 ? "" : "s"}`
    : "Notifications";

  return (
    <Button
      type="button"
      variant="outline"
      aria-label={label}
      title={label}
      className={cn(
        "relative w-full sm:w-auto",
        hasUnread &&
          "border-amber-400 bg-amber-50 text-amber-900 hover:bg-amber-100 hover:text-amber-900 dark:border-amber-500/60 dark:bg-amber-500/15 dark:text-amber-200 dark:hover:bg-amber-500/25",
      )}
      onClick={() => navigateTo("/chats/notifications")}
    >
      <Bell
        className={cn("size-4", hasUnread && "text-amber-500 dark:text-amber-300")}
        aria-hidden="true"
      />
      Notifications
      {hasUnread ? (
        <Badge className="ml-2 bg-amber-500 text-white shadow-none dark:text-amber-950">
          {count > 99 ? "99+" : count}
        </Badge>
      ) : null}
    </Button>
  );
}
