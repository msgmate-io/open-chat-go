import { ListTodo } from "lucide-react";
import { cn } from "../../lib/utils";
import { Badge } from "../badge";
import { Card } from "../card";
import { Text, TextTypes } from "../text";

function ExploreChatsIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" fill="none" viewBox="0 0 24 24" className="text-muted-foreground">
      <path
        fill="currentColor"
        fillRule="evenodd"
        d="M6.75 4.5a2.25 2.25 0 1 0 0 4.5 2.25 2.25 0 0 0 0-4.5M2.5 6.75a4.25 4.25 0 1 1 8.5 0 4.25 4.25 0 0 1-8.5 0M17.25 4.5a2.25 2.25 0 1 0 0 4.5 2.25 2.25 0 0 0 0-4.5M13 6.75a4.25 4.25 0 1 1 8.5 0 4.25 4.25 0 0 1-8.5 0M6.75 15a2.25 2.25 0 1 0 0 4.5 2.25 2.25 0 0 0 0-4.5M2.5 17.25a4.25 4.25 0 1 1 8.5 0 4.25 4.25 0 0 1-8.5 0M17.25 15a2.25 2.25 0 1 0 0 4.5 2.25 2.25 0 0 0 0-4.5M13 17.25a4.25 4.25 0 1 1 8.5 0 4.25 4.25 0 0 1-8.5 0"
        clipRule="evenodd"
      />
    </svg>
  );
}

export function DefaultChats({
  navigateTo,
  defaultBotContact,
  isBotLoading = false,
  botAvatarSrc,
  actionCount = 0,
}: {
  navigateTo: (to: string) => void;
  defaultBotContact: { contact_token?: string } | null | undefined;
  isBotLoading?: boolean;
  botAvatarSrc?: string;
  actionCount?: number;
}) {
  const hasActions = actionCount > 0;
  const hasDefaultBot = Boolean(defaultBotContact?.contact_token);
  const botUnavailable = !isBotLoading && !hasDefaultBot;
  const botDisabled = isBotLoading || botUnavailable;
  return (
    <div className="mb-1 flex flex-col gap-1">
      <Card
        className={cn("chat-list-row", botDisabled && "pointer-events-none opacity-60")}
        aria-disabled={botDisabled}
        title={botUnavailable ? "Default bot unavailable" : undefined}
        onClick={() => {
          if (hasDefaultBot && defaultBotContact?.contact_token) {
            navigateTo(`/chat/new/${defaultBotContact.contact_token}`);
          }
        }}
      >
        <div className="flex items-center gap-3 px-2 py-2">
          {botAvatarSrc ? (
            <img src={botAvatarSrc} className="size-8 rounded-full object-cover" alt="" />
          ) : (
            <div className="flex size-8 items-center justify-center rounded-full bg-muted text-sm font-semibold">
              H
            </div>
          )}
          <Text type={TextTypes.Body6} tag="span" bold className="truncate">
            Hal 9025
          </Text>
        </div>
      </Card>
      <Card className={cn("chat-list-row")} onClick={() => navigateTo("/chat/new")}>
        <div className="flex items-center gap-3 px-2 py-2">
          <div className="flex size-8 items-center justify-center rounded-md bg-muted">
            <ExploreChatsIcon />
          </div>
          <Text type={TextTypes.Body6} tag="span" bold className="truncate">
            Bots & Users Overview
          </Text>
        </div>
      </Card>
      <Card
        className={cn(
          "chat-list-row",
          hasActions && "border-amber-300/70 ring-1 ring-amber-400/50 dark:border-amber-500/40",
        )}
        onClick={() => navigateTo("/chats/actions")}
      >
        <div className="flex items-center gap-3 px-2 py-2">
          <div
            className={cn(
              "flex size-8 items-center justify-center rounded-md",
              hasActions
                ? "bg-amber-100 text-amber-700 dark:bg-amber-500/20 dark:text-amber-300"
                : "bg-muted text-muted-foreground",
            )}
          >
            <ListTodo className="size-4" />
          </div>
          <Text type={TextTypes.Body6} tag="span" bold className="truncate">
            Actions required
          </Text>
          {hasActions ? (
            <Badge
              variant="default"
              className="ml-auto bg-amber-500 text-white shadow-none dark:bg-amber-500 dark:text-amber-950"
            >
              {actionCount}
            </Badge>
          ) : null}
        </div>
      </Card>
    </div>
  );
}
