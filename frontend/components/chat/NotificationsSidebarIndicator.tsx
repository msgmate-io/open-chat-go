import { Bell } from "lucide-react";
import { registerChatSidebarHeaderAction } from "@open-chat-go/ui";
import { cn } from "@/lib/utils";
import { useNotificationsIntegrationEnabled, useUnreadNotificationCount } from "@/lib/notifications";

/**
 * Compact notification indicator shown in the chat sidebar header, left of the
 * compose button. It only renders on the small-mobile breakpoints (where the app
 * shows either the sidebar or the chat content) and while the notifications
 * integration is enabled. The count badge mirrors the top-bar notifications bell.
 */
export function NotificationsSidebarIndicator({ navigateTo }: { navigateTo: (to: string) => void }) {
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
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={() => navigateTo("/chats/notifications")}
      className={cn(
        "relative flex size-8 shrink-0 items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-accent hover:text-foreground sm:hidden",
        hasUnread && "text-amber-500 hover:text-amber-600 dark:text-amber-300",
      )}
    >
      <Bell className="size-4" aria-hidden="true" />
      {hasUnread ? (
        <span className="absolute -right-0.5 -top-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-amber-500 px-1 text-[10px] font-semibold leading-none text-white dark:text-amber-950">
          {count > 99 ? "99+" : count}
        </span>
      ) : null}
    </button>
  );
}

registerChatSidebarHeaderAction({
  id: "notifications",
  order: 0,
  Component: NotificationsSidebarIndicator,
});
