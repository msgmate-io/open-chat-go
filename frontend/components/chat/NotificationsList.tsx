import { useMemo, type ReactNode } from "react";
import { Bell, ChevronRight } from "lucide-react";
import {
  Badge,
  Button,
  Card,
  LoadingSpinner,
  Text,
  TextTypes,
  getChatTimeBucket,
} from "@open-chat-go/ui";
import { cn } from "@/lib/utils";
import {
  formatNotificationTimestamp,
  notificationRoute,
  relayedChannelLabel,
  type InAppNotification,
} from "@/lib/notifications";

function NotificationRow({
  notification,
  selected,
  onSelect,
}: {
  notification: InAppNotification;
  selected: boolean;
  onSelect: (uuid: string) => void;
}) {
  const route = notificationRoute(notification);
  return (
    <Card
      className={cn(
        "chat-list-row cursor-pointer",
        selected && "chat-list-row--selected",
        !notification.read && "border-amber-300/70 bg-amber-50/60 dark:border-amber-500/40 dark:bg-amber-500/10",
      )}
      onClick={() => onSelect(notification.uuid)}
    >
      <div className="relative flex items-start gap-2 px-2 py-1.5">
        <span
          className={cn(
            "mt-1.5 size-2 shrink-0 rounded-full",
            notification.read ? "bg-transparent" : "bg-amber-500",
          )}
          aria-hidden="true"
          title={notification.read ? undefined : "Unread"}
        />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1">
            <Text type={TextTypes.Body6} tag="span" bold className="block min-w-0 truncate">
              {notification.title || notification.slug}
            </Text>
          </div>
          <Text type={TextTypes.Body7} color="muted" className="block truncate">
            {notification.description || notification.title}
          </Text>
          <div className="mt-0.5 flex flex-wrap items-center gap-1">
            <Text type={TextTypes.Body7} color="muted" tag="span" className="text-[9px]">
              {formatNotificationTimestamp(notification.occurred_at_unix)}
            </Text>
            {notification.channels?.map((channel) => (
              <span
                key={channel}
                className="inline-block rounded-full border border-border/70 bg-secondary px-1.5 py-0 text-[8px] font-medium uppercase tracking-wide leading-4 text-muted-foreground"
              >
                {relayedChannelLabel(channel)}
              </span>
            ))}
            {route ? <ChevronRight className="size-3 text-muted-foreground" aria-hidden="true" /> : null}
          </div>
        </div>
      </div>
    </Card>
  );
}

/**
 * Notification list for the /chats/notifications screen. It mirrors the chats
 * list look (same row styling and time buckets) so the in-app panel feels like
 * the rest of the chat UI.
 */
export function NotificationsList({
  notifications,
  selectedUUID,
  isLoading,
  onSelect,
  onBack,
  onMarkAllRead,
  markingAll,
}: {
  notifications: InAppNotification[];
  selectedUUID: string | null;
  isLoading: boolean;
  onSelect: (uuid: string) => void;
  onBack: () => void;
  onMarkAllRead: () => void;
  markingAll: boolean;
}) {
  const unreadCount = notifications.filter((item) => !item.read).length;

  const items = useMemo(() => {
    const rendered: ReactNode[] = [];
    let lastBucket: string | null = null;
    for (const notification of notifications) {
      const date = new Date(notification.occurred_at_unix * 1000);
      const bucket = getChatTimeBucket(date);
      if (bucket.key !== lastBucket) {
        lastBucket = bucket.key;
        rendered.push(
          <div key={`bucket-${bucket.key}`} className="chat-list-divider">
            <Text type={TextTypes.Body7} color="muted" tag="span" bold>
              {bucket.label}
            </Text>
          </div>,
        );
      }
      rendered.push(
        <NotificationRow
          key={notification.uuid}
          notification={notification}
          selected={notification.uuid === selectedUUID}
          onSelect={onSelect}
        />,
      );
    }
    return rendered;
  }, [notifications, selectedUUID, onSelect]);

  return (
    <div className="flex h-full w-full flex-col">
      <div className="flex items-center justify-between gap-2 border-b border-border/60 px-3 py-2">
        <button
          type="button"
          className="flex min-w-0 items-center gap-2 text-left"
          onClick={onBack}
        >
          <ChevronRight className="size-4 rotate-180 text-muted-foreground" aria-hidden="true" />
          <div className="min-w-0">
            <Text type={TextTypes.Body5} tag="span" bold className="block truncate">
              Notifications
            </Text>
            <Text type={TextTypes.Body7} color="muted" tag="span" className="block">
              Back to chats
            </Text>
          </div>
        </button>
        {unreadCount > 0 ? (
          <Badge className="bg-amber-500 text-white shadow-none dark:text-amber-950">
            {unreadCount > 99 ? "99+" : unreadCount}
          </Badge>
        ) : null}
      </div>

      {unreadCount > 0 ? (
        <div className="flex justify-end px-2 py-1">
          <Button variant="ghost" size="sm" className="h-6 px-2 text-[10px]" onClick={onMarkAllRead} disabled={markingAll}>
            {markingAll ? "Marking…" : "Mark all read"}
          </Button>
        </div>
      ) : null}

      <div className="chat-list-scroll">
        {isLoading ? (
          <div className="flex items-center gap-2 px-3 py-4">
            <LoadingSpinner />
            <Text type={TextTypes.Body7} color="muted">
              Loading notifications…
            </Text>
          </div>
        ) : notifications.length === 0 ? (
          <div className="flex flex-col items-center gap-2 px-4 py-10 text-center">
            <Bell className="size-6 text-muted-foreground" aria-hidden="true" />
            <Text type={TextTypes.Body6} color="muted">
              You have no notifications yet.
            </Text>
          </div>
        ) : (
          items
        )}
      </div>
    </div>
  );
}
