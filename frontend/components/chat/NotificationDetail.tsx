import { ArrowLeft, ExternalLink, MessagesSquare } from "lucide-react";
import {
  Badge,
  BotMessageShell,
  Button,
  MessageMarkdown,
  Text,
  TextTypes,
} from "@open-chat-go/ui";
import {
  formatNotificationTimestamp,
  notificationActionLabel,
  notificationRoute,
  relayedChannelLabel,
  type InAppNotification,
} from "@/lib/notifications";

/**
 * Notification content pane. The body is rendered with the shared chat message
 * content renderer so notification text supports the same markdown as messages.
 */
export function NotificationDetail({
  notification,
  onBack,
  onOpenChats,
  onOpenAction,
}: {
  notification: InAppNotification;
  onBack: () => void;
  onOpenChats: () => void;
  onOpenAction: () => void;
}) {
  const route = notificationRoute(notification);
  const senderLabel = notification.source_integration
    ? `${notification.source_integration} · notification`
    : "notification";

  return (
    <div className="flex h-full min-h-0 w-full flex-col">
      <div className="flex flex-wrap items-center gap-2 border-b border-border/60 px-3 py-2">
        <Button
          variant="ghost"
          size="icon"
          className="md:hidden"
          onClick={onBack}
          aria-label="Back to notifications"
        >
          <ArrowLeft className="size-4" />
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="gap-1"
          onClick={onOpenChats}
          aria-label="Back to chats"
        >
          <MessagesSquare className="size-4" />
          <span className="hidden sm:inline">Chats</span>
        </Button>
        <div className="min-w-0 flex-1">
          <Text type={TextTypes.Body5} tag="h1" bold className="block truncate">
            {notification.title || notification.slug}
          </Text>
          <div className="flex flex-wrap items-center gap-1">
            {notification.source_integration ? (
              <Badge variant="outline">{notification.source_integration}</Badge>
            ) : null}
            <Text type={TextTypes.Body7} color="muted" tag="span">
              {formatNotificationTimestamp(notification.occurred_at_unix)}
            </Text>
          </div>
        </div>
        {route ? (
          <Button size="sm" className="gap-1" onClick={onOpenAction}>
            <ExternalLink className="size-4" />
            {notificationActionLabel(notification)}
          </Button>
        ) : null}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-4">
        <BotMessageShell senderLabel={senderLabel}>
          <MessageMarkdown>{notification.description || notification.title}</MessageMarkdown>
        </BotMessageShell>

        {notification.channels?.length > 0 ? (
          <div className="mt-4 flex flex-wrap items-center gap-2 border-t border-border/60 pt-3">
            <Text type={TextTypes.Body7} color="muted" tag="span" bold className="uppercase tracking-wide text-[10px]">
              Also sent via
            </Text>
            {notification.channels.map((channel) => (
              <Badge key={channel} variant="secondary">
                {relayedChannelLabel(channel)}
              </Badge>
            ))}
          </div>
        ) : (
          <div className="mt-4 border-t border-border/60 pt-3">
            <Text type={TextTypes.Body7} color="muted" tag="span">
              This notification was only delivered in-app.
            </Text>
          </div>
        )}
      </div>
    </div>
  );
}
