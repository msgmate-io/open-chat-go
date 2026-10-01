import { useEffect, useMemo, useState } from "react";
import useSWR, { mutate as globalMutate } from "swr";
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  ChatUIMessageExtras,
  ConfirmableActionWidget,
  LoadingSpinner,
  Text,
  TextTypes,
  type ChatUIToolCall,
  type ConfirmableAction,
} from "@open-chat-go/ui";
import { cn, fetcher } from "@/lib/utils";
import { AdminActionTasksWidget } from "@/components/chat/AdminActionTasksWidget";

type ActionTaskAction = {
  kind: string;
  action_id?: string;
  title?: string;
  description?: string;
  target_tool_name?: string;
  danger_level?: string;
  reason?: string;
};

type ActionTaskPartner = {
  uuid: string;
  name: string;
  username?: string;
  contact_token?: string;
  is_automated: boolean;
};

type ActionTaskRow = {
  task_key: string;
  chat_uuid: string;
  chat_type: string;
  partner: ActionTaskPartner;
  message_uuid: string;
  message_text: string;
  message_send_at: string;
  message_meta: Record<string, unknown>;
  tool_calls: ChatUIToolCall[];
  actions: ActionTaskAction[];
  reason: string;
};

type ActionTasksResponse = {
  count: number;
  rows: ActionTaskRow[];
};

type PreviewMessage = {
  uuid: string;
  sender_uuid: string;
  sender_is_automated: boolean;
  data_type: string;
  text: string;
};

type PreviewResponse = {
  rows: PreviewMessage[];
};

type SelfUser = {
  uuid?: string;
  is_admin?: boolean;
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

function MessagePreview({
  chatUUID,
  selfUUID,
}: {
  chatUUID: string;
  selfUUID?: string;
}) {
  const { data, isLoading } = useSWR<PreviewResponse>(
    `/api/v1/chats/${chatUUID}/messages/list?limit=6`,
    fetcher,
    { revalidateOnFocus: false },
  );

  const rows = useMemo(() => {
    const items = [...(data?.rows ?? [])];
    // Messages come back newest-first; show them chronologically.
    return items.reverse().filter((row) => row.data_type !== "event");
  }, [data]);

  if (isLoading) {
    return (
      <div className="flex h-20 items-center justify-center">
        <LoadingSpinner />
      </div>
    );
  }

  if (rows.length === 0) {
    return (
      <Text type={TextTypes.Body7} color="muted">
        No recent messages.
      </Text>
    );
  }

  return (
    <div className="flex flex-col gap-1.5">
      {rows.map((row) => {
        const isSelf = Boolean(selfUUID && row.sender_uuid === selfUUID);
        const text = row.text?.trim() || (row.data_type !== "text" ? `[${row.data_type}]` : "");
        return (
          <div key={row.uuid} className={cn("flex", isSelf ? "justify-end" : "justify-start")}>
            <div
              className={cn(
                "max-w-[85%] truncate rounded-lg px-2.5 py-1 text-xs",
                isSelf ? "bg-primary/10 text-foreground" : "bg-muted text-muted-foreground",
              )}
            >
              {text || "…"}
            </div>
          </div>
        );
      })}
    </div>
  );
}

function InteractionConfirmationControls({
  task,
  busy,
  onResolved,
  onError,
}: {
  task: ActionTaskRow;
  busy: boolean;
  onResolved: (task: ActionTaskRow) => void;
  onError: (message: string) => void;
}) {
  const [deciding, setDeciding] = useState<"approve" | "reject" | null>(null);

  const decide = async (decision: "approve" | "reject") => {
    setDeciding(decision);
    try {
      const response = await fetch(
        `/api/v1/chats/${task.chat_uuid}/messages/${task.message_uuid}/interaction-confirmation/${decision}`,
        { method: "POST" },
      );
      if (!response.ok) {
        onError((await response.text()) || "Failed to resolve confirmation.");
        return;
      }
      onResolved(task);
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to resolve confirmation.");
    } finally {
      setDeciding(null);
    }
  };

  return (
    <div className="flex items-center gap-2">
      <Button type="button" size="sm" onClick={() => decide("approve")} disabled={busy || deciding !== null}>
        Approve
      </Button>
      <Button
        type="button"
        size="sm"
        variant="outline"
        onClick={() => decide("reject")}
        disabled={busy || deciding !== null}
      >
        Reject
      </Button>
    </div>
  );
}

function TaskCard({
  task,
  selfUUID,
  busy,
  onResolved,
  onError,
  onOpen,
  onSkip,
  onDismiss,
}: {
  task: ActionTaskRow;
  selfUUID?: string;
  busy: boolean;
  onResolved: (task: ActionTaskRow) => void;
  onError: (message: string) => void;
  onOpen: (task: ActionTaskRow) => void;
  onSkip: () => void;
  onDismiss: (task: ActionTaskRow) => void;
}) {
  const confirmableActions = task.actions.filter(
    (action) => action.kind === "confirmable_action" && action.action_id && action.target_tool_name,
  );
  const hasInteractionConfirmation = task.actions.some(
    (action) => action.kind === "interaction_confirmation",
  );

  return (
    <Card className="w-full border-border/70 bg-card shadow-md">
      <CardHeader className="gap-2 pb-3">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <CardTitle className="truncate text-base">
              {task.partner?.name || task.partner?.username || "Unknown"}
            </CardTitle>
            <CardDescription className="truncate">
              {task.partner?.is_automated ? "Bot" : "Contact"} · {task.chat_type.replace(":", " / ")} ·{" "}
              {formatTimestamp(task.message_send_at)}
            </CardDescription>
          </div>
          <div className="flex shrink-0 flex-wrap justify-end gap-1">
            {task.actions.map((action, index) => (
              <Badge key={`${action.kind}-${index}`} variant="secondary">
                {ACTION_LABELS[action.kind] ?? action.kind}
              </Badge>
            ))}
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-3 pt-0">
        <div className="rounded-lg border border-border/60 bg-muted/20 p-3">
          <MessagePreview chatUUID={task.chat_uuid} selfUUID={selfUUID} />
        </div>

        {task.message_text?.trim() ? (
          <Text type={TextTypes.Body7} color="muted" className="line-clamp-3">
            {task.message_text}
          </Text>
        ) : null}

        {confirmableActions.map((action) => (
          <ConfirmableActionWidget
            key={`${task.task_key}-${action.action_id}`}
            chatUUID={task.chat_uuid}
            messageUUID={task.message_uuid}
            action={
              {
                action_id: action.action_id as string,
                target_tool_name: action.target_tool_name as string,
                title: action.title,
                description: action.description,
                danger_level: action.danger_level,
                status: "pending",
              } as ConfirmableAction
            }
            onExecuted={() => onResolved(task)}
          />
        ))}

        <ChatUIMessageExtras
          toolCalls={task.tool_calls ?? []}
          meta={task.message_meta ?? {}}
          chatUUID={task.chat_uuid}
          onMutate={() => onResolved(task)}
        />

        {hasInteractionConfirmation ? (
          <InteractionConfirmationControls task={task} busy={busy} onResolved={onResolved} onError={onError} />
        ) : null}

        <div className="flex flex-wrap items-center gap-2 pt-1">
          <Button type="button" size="sm" variant="outline" onClick={() => onOpen(task)}>
            Open chat
          </Button>
          <Button type="button" size="sm" variant="ghost" onClick={() => onSkip()}>
            Skip
          </Button>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            className="text-muted-foreground"
            onClick={() => onDismiss(task)}
            disabled={busy}
          >
            Ignore / mark completed
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

export function ActionTasksView({ navigateTo }: { navigateTo: (to: string) => void }) {
  const { data, error: loadError, isLoading, mutate } = useSWR<ActionTasksResponse>(
    "/api/v1/chats/action-tasks",
    fetcher,
    {
      refreshInterval: () => (typeof document !== "undefined" && document.hidden ? 0 : 5000),
      revalidateOnFocus: true,
    },
  );
  const { data: selfUser } = useSWR<SelfUser>("/api/v1/user/self", fetcher, {
    revalidateOnFocus: false,
  });

  const [dismissed, setDismissed] = useState<Set<string>>(() => new Set());
  const [index, setIndex] = useState(0);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  const visibleTasks = useMemo(
    () => (data?.rows ?? []).filter((task) => !dismissed.has(task.task_key)),
    [data?.rows, dismissed],
  );
  const total = visibleTasks.length;
  const safeIndex = total > 0 ? Math.min(index, total - 1) : 0;
  const current = visibleTasks[safeIndex];

  useEffect(() => {
    if (index > 0 && index >= total) {
      setIndex(total > 0 ? total - 1 : 0);
    }
  }, [index, total]);

  const markResolved = (task: ActionTaskRow) => {
    setDismissed((previous) => {
      const next = new Set(previous);
      next.add(task.task_key);
      return next;
    });
    void globalMutate("/api/v1/chats/action-tasks?count_only=1");
    void mutate();
  };

  const dismissTask = async (task: ActionTaskRow) => {
    setBusy(true);
    setActionError(null);
    try {
      const response = await fetch(`/api/v1/chats/${task.chat_uuid}/action-tasks/dismiss`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ message_uuid: task.message_uuid }),
      });
      if (!response.ok) {
        setActionError((await response.text()) || "Failed to dismiss task.");
        return;
      }
      markResolved(task);
    } catch (error) {
      setActionError(error instanceof Error ? error.message : "Failed to dismiss task.");
    } finally {
      setBusy(false);
    }
  };

  const skipTask = () => {
    setActionError(null);
    setIndex((value) => (total > 0 ? (value + 1) % total : 0));
  };

  const openChat = (task: ActionTaskRow) => {
    navigateTo(`/chat/${task.chat_uuid}`);
  };

  const peeks = visibleTasks.slice(safeIndex + 1, safeIndex + 3);

  return (
    <div className="mx-auto h-full w-full max-w-3xl space-y-4 overflow-y-auto px-4 py-6 md:px-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <Text type={TextTypes.Heading6} tag="h1" bold>
            Actions required
          </Text>
          <Text type={TextTypes.Body7} color="muted" className="mt-1">
            Chats and interactions currently waiting on you.
          </Text>
        </div>
        <div className="flex items-center gap-2">
          {total > 0 ? (
            <Badge variant="secondary">
              {total} pending
            </Badge>
          ) : null}
          <Button type="button" size="sm" variant="outline" onClick={() => void mutate()}>
            Refresh
          </Button>
        </div>
      </div>

      {selfUser?.is_admin ? <AdminActionTasksWidget navigateTo={navigateTo} /> : null}

      {actionError ? (
        <Card className="border-destructive/30 bg-destructive/5">
          <CardContent className="pt-4">
            <Text type={TextTypes.Body7} color="destructive">
              {actionError}
            </Text>
          </CardContent>
        </Card>
      ) : null}

      {isLoading ? (
        <div className="flex h-40 items-center justify-center">
          <LoadingSpinner />
        </div>
      ) : loadError ? (
        <Card className="border-destructive/30 bg-destructive/5">
          <CardHeader>
            <CardTitle className="text-base">Could not load action tasks</CardTitle>
            <CardDescription>Reload the page or try again in a moment.</CardDescription>
          </CardHeader>
        </Card>
      ) : !current ? (
        <Card className="border-dashed border-border/70 bg-muted/30">
          <CardHeader>
            <CardTitle className="text-lg">You&apos;re all caught up</CardTitle>
            <CardDescription>No interactions currently require your action.</CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <Text type={TextTypes.Body7} color="muted">
              Task {safeIndex + 1} / {total}
            </Text>
            {total > 1 ? (
              <Button type="button" size="sm" variant="ghost" onClick={skipTask}>
                Next task
              </Button>
            ) : null}
          </div>

          <div className="relative pb-3">
            {peeks[1] ? (
              <div className="pointer-events-none absolute inset-0 translate-y-3 scale-[0.97] rounded-xl border border-border/50 bg-card/50" />
            ) : null}
            {peeks[0] ? (
              <div className="pointer-events-none absolute inset-0 translate-y-1.5 scale-[0.985] rounded-xl border border-border/60 bg-card/70" />
            ) : null}
            <div className="relative z-10">
              <TaskCard
                task={current}
                selfUUID={selfUser?.uuid}
                busy={busy}
                onResolved={markResolved}
                onError={setActionError}
                onOpen={openChat}
                onSkip={skipTask}
                onDismiss={dismissTask}
              />
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
