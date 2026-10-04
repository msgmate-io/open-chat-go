import { useState } from "react";
import useSWR, { mutate as globalMutate } from "swr";
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  LoadingSpinner,
  Text,
  TextTypes,
} from "@open-chat-go/ui";
import { fetcher } from "@/lib/utils";

type AdminActionTaskAction = {
  kind: string;
  title?: string;
  description?: string;
  target_tool_name?: string;
};

type AdminActionTaskOwner = {
  uuid?: string;
  name?: string;
  username?: string;
  is_automated?: boolean;
};

type AdminActionTaskRow = {
  task_key: string;
  chat_uuid: string;
  chat_type: string;
  message_uuid: string;
  message_send_at: string;
  actions: AdminActionTaskAction[];
  chat_owner?: AdminActionTaskOwner;
};

type AdminActionTasksResponse = {
  count: number;
  rows: AdminActionTaskRow[];
};

const ACTION_LABELS: Record<string, string> = {
  confirmable_action: "Confirmable action",
  opencode_permission: "OpenCode permission",
  opencode_needs_action: "OpenCode needs action",
  interaction_confirmation: "Interaction confirmation",
  tool_confirmation: "Tool confirmation",
};

function formatTimestamp(raw: string): string {
  const date = new Date(raw);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return date.toLocaleString();
}

// AdminActionTasksWidget is an admin-only overview of interactions that are
// waiting on a user action across every account (the admin "see all" scope). It
// is intentionally compact: the owning user, the pending action kinds and a
// shortcut into the chat, so an admin can spot and triage stalled interactions.
export function AdminActionTasksWidget({
  navigateTo,
}: {
  navigateTo: (to: string) => void;
}) {
  const { data, error, isLoading, mutate } = useSWR<AdminActionTasksResponse>(
    "/api/v1/chats/action-tasks?scope=all",
    fetcher,
    {
      refreshInterval: () => (typeof document !== "undefined" && document.hidden ? 0 : 10000),
      revalidateOnFocus: true,
    },
  );
  const { data: integrations } = useSWR<{ rows?: Array<{ name?: string }> }>(
    "/api/v1/integrations/list",
    fetcher,
    { revalidateOnFocus: false },
  );
  const [busy, setBusy] = useState(false);
  const [impersonating, setImpersonating] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  const accountManagementAvailable = Boolean(
    integrations?.rows?.some((integration) => integration.name === "account_management"),
  );

  // impersonateUser assumes the identity of the user who owns the pending
  // action so the admin can open their action stack and resolve the
  // confirmation as that user.
  const impersonateUser = async (row: AdminActionTaskRow) => {
    const owner = row.chat_owner;
    if (!owner?.uuid || impersonating) {
      return;
    }
    setImpersonating(row.task_key);
    setActionError(null);
    try {
      const response = await fetch(
        "/api/v1/integrations/account_management/impersonation/start",
        {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ user_uuid: owner.uuid }),
        },
      );
      if (!response.ok) {
        setActionError((await response.text()).trim() || "Failed to impersonate user.");
        return;
      }
      window.location.href = `/chat/${row.chat_uuid}`;
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "Failed to impersonate user.");
    } finally {
      setImpersonating(null);
    }
  };

  if (error) {
    return null;
  }

  const rows = data?.rows ?? [];
  const count = data?.count ?? rows.length;

  const ignoreAll = async () => {
    if (busy || count === 0) {
      return;
    }
    if (
      typeof window !== "undefined" &&
      !window.confirm(
        "Ignore all pending actions? They will stop appearing in this admin view for you. Owners' own action stacks are unchanged.",
      )
    ) {
      return;
    }
    setBusy(true);
    setActionError(null);
    try {
      const response = await fetch("/api/v1/chats/action-tasks/dismiss-all", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
      });
      if (!response.ok) {
        setActionError((await response.text()) || "Failed to ignore pending actions.");
        return;
      }
      await mutate();
      void globalMutate("/api/v1/chats/action-tasks?count_only=1");
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "Failed to ignore pending actions.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card className="w-full border-amber-300/50 bg-amber-50/40 shadow-sm dark:border-amber-400/30 dark:bg-amber-950/10">
      <CardHeader className="gap-1 pb-3">
        <div className="flex items-center justify-between gap-3">
          <CardTitle className="flex items-center gap-2 text-base">
            <span className="rounded bg-amber-200/70 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-amber-800 dark:bg-amber-400/20 dark:text-amber-200">
              Admin
            </span>
            Pending actions across all users
          </CardTitle>
          <div className="flex items-center gap-2">
            {count > 0 ? <Badge variant="secondary">{count}</Badge> : null}
            <Button
              type="button"
              size="sm"
              variant="destructive"
              className="shrink-0"
              disabled={count === 0 || busy}
              onClick={() => void ignoreAll()}
            >
              {busy ? "Ignoring..." : "Ignore all"}
            </Button>
          </div>
        </div>
        <CardDescription>
          Interactions waiting on their owner&apos;s action. Open a chat to inspect or resolve it.
        </CardDescription>
        {actionError ? (
          <Text type={TextTypes.Body7} color="destructive">
            {actionError}
          </Text>
        ) : null}
      </CardHeader>
      <CardContent className="pt-0">
        {isLoading && !data ? (
          <div className="flex h-16 items-center justify-center">
            <LoadingSpinner />
          </div>
        ) : rows.length === 0 ? (
          <Text type={TextTypes.Body7} color="muted">
            No pending actions across any user.
          </Text>
        ) : (
          <div className="flex flex-col divide-y divide-border/60">
            {rows.map((row) => {
              const owner = row.chat_owner;
              const ownerLabel = owner?.name || owner?.username || "Unknown user";
              return (
                <div key={row.task_key} className="flex items-center justify-between gap-3 py-2">
                  <div className="min-w-0">
                    <Text type={TextTypes.Body6} bold className="truncate">
                      {ownerLabel}
                    </Text>
                    <Text type={TextTypes.Body7} color="muted" className="truncate">
                      {row.chat_type.replace(":", " / ")} · {formatTimestamp(row.message_send_at)}
                    </Text>
                    <div className="mt-1 flex flex-wrap gap-1">
                      {row.actions.map((action, index) => (
                        <Badge key={`${action.kind}-${index}`} variant="outline" className="text-[10px]">
                          {ACTION_LABELS[action.kind] ?? action.kind}
                        </Badge>
                      ))}
                    </div>
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    {owner?.uuid && accountManagementAvailable ? (
                      <Button
                        type="button"
                        size="sm"
                        variant="ghost"
                        className="shrink-0 text-xs"
                        disabled={impersonating !== null}
                        onClick={() => void impersonateUser(row)}
                      >
                        {impersonating === row.task_key ? "Impersonating…" : "Impersonate"}
                      </Button>
                    ) : null}
                    <Button
                      type="button"
                      size="sm"
                      variant="outline"
                      className="shrink-0"
                      onClick={() => navigateTo(`/chat/${row.chat_uuid}`)}
                    >
                      Open
                    </Button>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
