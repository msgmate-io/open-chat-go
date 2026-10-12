import { useCallback, useMemo, useState } from "react";
import useSWR from "swr";
import { navigate } from "vike/client/router";
import { Bell, MessagesSquare } from "lucide-react";
import { Button, ChatBase, LoadingSpinner, Text, TextTypes } from "@open-chat-go/ui";
import { fetcher } from "@/lib/utils";
import { NotificationsList } from "@/components/chat/NotificationsList";
import { NotificationDetail } from "@/components/chat/NotificationDetail";
import {
  markAllNotificationsRead,
  markNotificationRead,
  notificationRoute,
  useNotificationsIntegrationStatus,
  useUnreadNotificationCount,
  type NotificationInboxResponse,
} from "@/lib/notifications";

const INBOX_KEY = "/api/v1/integrations/notifications/inbox?limit=100";

export default function NotificationsPage() {
  const { enabled, resolved } = useNotificationsIntegrationStatus();
  const { mutate: mutateUnreadCount } = useUnreadNotificationCount(enabled);
  const { data, isLoading, mutate } = useSWR<NotificationInboxResponse>(
    enabled ? INBOX_KEY : null,
    fetcher,
    { revalidateOnFocus: true },
  );

  const [selectedUUID, setSelectedUUID] = useState<string | null>(null);
  const [markingAll, setMarkingAll] = useState(false);

  const notifications = data?.rows ?? [];
  const selected = useMemo(
    () => notifications.find((item) => item.uuid === selectedUUID) ?? null,
    [notifications, selectedUUID],
  );

  const openChats = useCallback(() => navigate("/chat"), []);

  const handleSelect = useCallback(
    async (uuid: string) => {
      setSelectedUUID(uuid);
      const target = notifications.find((item) => item.uuid === uuid);
      if (!target || target.read) {
        return;
      }
      // Optimistically clear the unread highlight, then persist the read state.
      await mutate(
        (current) =>
          current
            ? {
                ...current,
                rows: current.rows.map((item) =>
                  item.uuid === uuid ? { ...item, read: true } : item,
                ),
                unread_count: Math.max(0, current.unread_count - 1),
              }
            : current,
        { revalidate: false },
      );
      try {
        await markNotificationRead(uuid);
      } catch {
        // A failed read marker is non-fatal; the next revalidation restores it.
      }
      await Promise.all([mutate(), mutateUnreadCount()]);
    },
    [notifications, mutate, mutateUnreadCount],
  );

  const handleMarkAllRead = useCallback(async () => {
    setMarkingAll(true);
    try {
      await markAllNotificationsRead();
      await Promise.all([mutate(), mutateUnreadCount()]);
    } catch {
      // Ignore; the list revalidation below reflects the actual state.
    } finally {
      setMarkingAll(false);
    }
  }, [mutate, mutateUnreadCount]);

  const handleOpenAction = useCallback(() => {
    if (!selected) {
      return;
    }
    const route = notificationRoute(selected);
    if (route) {
      navigate(route);
    }
  }, [selected]);

  const navigateTo = useCallback((to: string) => {
    if (to === "/chats/notifications") {
      return;
    }
    navigate(to);
  }, []);

  if (!resolved) {
    return (
      <div className="flex h-full w-full items-center justify-center gap-2 p-6">
        <LoadingSpinner />
        <Text type={TextTypes.Body6} color="muted">
          Loading notifications…
        </Text>
      </div>
    );
  }

  if (!enabled) {
    return (
      <div className="mx-auto flex h-full w-full max-w-lg flex-col items-center justify-center gap-3 p-6 text-center">
        <Bell className="size-8 text-muted-foreground" aria-hidden="true" />
        <Text type={TextTypes.Heading5} tag="h1">
          Notifications are not available
        </Text>
        <Text type={TextTypes.Body6} color="muted">
          The notifications integration is not enabled on this server.
        </Text>
        <Button variant="outline" onClick={openChats}>
          Back to chats
        </Button>
      </div>
    );
  }

  return (
    <ChatBase
      chatUUID={selectedUUID}
      mobileViewMode={selectedUUID ? "content" : "list"}
      navigateTo={navigateTo}
      sidebar={
        <NotificationsList
          notifications={notifications}
          selectedUUID={selectedUUID}
          isLoading={isLoading}
          onSelect={handleSelect}
          onBack={openChats}
          onMarkAllRead={handleMarkAllRead}
          markingAll={markingAll}
        />
      }
    >
      {selected ? (
        <NotificationDetail
          notification={selected}
          onBack={() => setSelectedUUID(null)}
          onOpenChats={openChats}
          onOpenAction={handleOpenAction}
        />
      ) : (
        <div className="mx-auto flex h-full w-full max-w-lg flex-col items-center justify-center gap-2 p-6 text-center">
          <Bell className="size-8 text-muted-foreground" aria-hidden="true" />
          <Text type={TextTypes.Body5} bold>
            Select a notification
          </Text>
          <Text type={TextTypes.Body6} color="muted">
            Pick a notification from the list to read it here.
          </Text>
          <Button variant="ghost" size="sm" className="gap-1" onClick={openChats}>
            <MessagesSquare className="size-4" />
            Back to chats
          </Button>
        </div>
      )}
    </ChatBase>
  );
}
