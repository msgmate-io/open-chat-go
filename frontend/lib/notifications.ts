import useSWR from "swr";
import { fetcher } from "./utils";

/** Integration name whose API backs the in-app notification panel. */
export const NOTIFICATIONS_INTEGRATION = "notifications";

export type InAppNotification = {
  uuid: string;
  slug: string;
  title: string;
  description: string;
  source_integration?: string;
  action_type?: string;
  action_target?: string;
  channels: string[];
  context?: string;
  read: boolean;
  occurred_at_unix: number;
  created_at_unix: number;
};

export type NotificationInboxResponse = {
  rows: InAppNotification[];
  total: number;
  unread_count: number;
};

type IntegrationsListResponse = {
  rows: { name: string }[];
};

/**
 * Reports whether the notifications integration is compiled into this backend
 * and visible to the current user. The bell, the screen and every API call are
 * gated on this so builds without the integration never render broken UI.
 */
export function useNotificationsIntegrationStatus(): { enabled: boolean; resolved: boolean } {
  const { data } = useSWR<IntegrationsListResponse>("/api/v1/integrations/list", fetcher, {
    revalidateOnFocus: false,
    shouldRetryOnError: false,
  });
  return {
    enabled: Boolean(data?.rows?.some((row) => row.name === NOTIFICATIONS_INTEGRATION)),
    resolved: Boolean(data),
  };
}

export function useNotificationsIntegrationEnabled(): boolean {
  return useNotificationsIntegrationStatus().enabled;
}

/** Lightweight unread-count poll used for the bell badge. */
export function useUnreadNotificationCount(enabled: boolean) {
  const { data, mutate } = useSWR<{ unread_count: number }>(
    enabled ? "/api/v1/integrations/notifications/inbox/count" : null,
    fetcher,
    {
      refreshInterval: () => (typeof document !== "undefined" && document.hidden ? 0 : 15000),
      revalidateOnFocus: true,
    },
  );
  return {
    count: typeof data?.unread_count === "number" ? data.unread_count : 0,
    mutate,
  };
}

const CHANNEL_LABELS: Record<string, string> = {
  in_app: "In-app",
  email: "Email",
  signal: "Signal",
};

export function relayedChannelLabel(channel: string): string {
  const key = channel.trim().toLowerCase();
  if (!key) {
    return "";
  }
  return CHANNEL_LABELS[key] ?? key.charAt(0).toUpperCase() + key.slice(1);
}

/** Resolves a notification's click-through target to an app route, if any. */
export function notificationRoute(notification: InAppNotification): string | null {
  const type = (notification.action_type || "").trim();
  const target = (notification.action_target || "").trim();
  if (!type || !target) {
    return null;
  }
  if (type === "chat") {
    return `/chat/${encodeURIComponent(target)}`;
  }
  if (type === "section") {
    return target.startsWith("/") ? target : `/${target}`;
  }
  return null;
}

export function notificationActionLabel(notification: InAppNotification): string {
  const type = (notification.action_type || "").trim();
  if (type === "chat") {
    return "Open chat";
  }
  if (type === "section") {
    return "Open";
  }
  return "Open";
}

export async function markNotificationRead(uuid: string): Promise<void> {
  await fetcher(`/api/v1/integrations/notifications/inbox/${encodeURIComponent(uuid)}/read`, {
    method: "POST",
  });
}

export async function markAllNotificationsRead(): Promise<void> {
  await fetcher("/api/v1/integrations/notifications/inbox/read-all", { method: "POST" });
}

export function formatNotificationTimestamp(unixSeconds: number): string {
  if (!unixSeconds) {
    return "";
  }
  try {
    return new Date(unixSeconds * 1000).toLocaleString(undefined, {
      dateStyle: "medium",
      timeStyle: "short",
    });
  } catch {
    return "";
  }
}
