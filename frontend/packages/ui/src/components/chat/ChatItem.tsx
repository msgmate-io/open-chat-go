import { useState } from "react";
import { cn } from "../../lib/utils";
import { OnlineIndicator } from "./OnlineIndicator";
import { ChatSettings } from "./ChatSettings";
import { DropdownMenuTrigger } from "../dropdown-menu";
import { Card } from "../card";
import { Button } from "../button";
import { Text, TextTypes } from "../text";

function DotsHorizontal({ className }: { className?: string }) {
  return (
    <svg
      className={className ?? "size-4 text-muted-foreground"}
      width="20"
      height="20"
      viewBox="0 0 20 20"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
    >
      <path
        d="M5 10C5 10.8284 4.32843 11.5 3.5 11.5C2.67157 11.5 2 10.8284 2 10C2 9.17157 2.67157 8.5 3.5 8.5C4.32843 8.5 5 9.17157 5 10ZM11.5 10C11.5 10.8284 10.8284 11.5 10 11.5C9.17157 11.5 8.5 10.8284 8.5 10C8.5 9.17157 9.17157 8.5 10 8.5C10.8284 8.5 11.5 9.17157 11.5 10ZM17.5 11.5C18.3284 11.5 19 10.8284 19 10C19 9.17157 18.3284 8.5 17.5 8.5C16.6716 8.5 16 9.17157 16 10C16 10.8284 16.6716 11.5 17.5 11.5Z"
        fill="currentColor"
        fillRule="evenodd"
        clipRule="evenodd"
      />
    </svg>
  );
}

export function ChatItem({
  chat,
  isSelected = false,
}: {
  chat: {
    uuid: string;
    partner?: { first_name?: string; second_name?: string; is_bot?: boolean; is_online?: boolean };
    settings?: { title?: string };
    newest_message?: { text?: string };
  };
  isSelected?: boolean;
}) {
  const [settingsOpen, setSettingsOpen] = useState(false);
  const title = chat?.settings?.title
    ? chat.settings.title
    : `${chat.partner?.first_name ?? ""} ${chat.partner?.second_name ?? ""}`.trim();

  return (
    <ChatSettings chat={chat} open={settingsOpen} setOpen={setSettingsOpen}>
      <Card
        className={cn(
          "chat-list-row",
          settingsOpen && "pointer-events-none",
          isSelected && "chat-list-row--selected"
        )}
        onClick={() => {
          if (!settingsOpen) {
            // TODO: navigate
          }
        }}
      >
        <div className="relative px-2 py-2">
          <div className="flex items-start gap-2 pr-8">
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <Text type={TextTypes.Body6} tag="span" bold className="truncate">
                  {title}
                </Text>
                {!chat?.partner?.is_bot ? (
                  <OnlineIndicator isOnline={chat?.partner?.is_online ?? false} />
                ) : null}
              </div>
              <Text type={TextTypes.Body7} color="muted" className="truncate">
                {chat.newest_message?.text}
              </Text>
            </div>
          </div>
          <div className="absolute right-1 top-1/2 -translate-y-1/2">
            <DropdownMenuTrigger asChild>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="size-7 shrink-0"
                onClick={(event) => event.stopPropagation()}
                aria-label="Chat settings"
              >
                <DotsHorizontal />
              </Button>
            </DropdownMenuTrigger>
          </div>
        </div>
      </Card>
    </ChatSettings>
  );
}

export type ChatState = "idle" | "active" | "finished" | "failed" | "needs_confirmation";

const CHAT_STATE_DOT_CLASSES: Record<ChatState, string> = {
  idle: "",
  active: "bg-yellow-400 chat-state-dot--glow",
  finished: "bg-green-500",
  failed: "bg-red-500",
  needs_confirmation: "bg-blue-500",
};

const CHAT_STATE_DOT_LABELS: Record<ChatState, string> = {
  idle: "",
  active: "Bot interaction running",
  finished: "AI completion done",
  failed: "Error occurred in the interaction",
  needs_confirmation: "User confirmation required",
};

function ChatStateDot({ state }: { state?: string }) {
  if (!state || state === "idle") {
    return null;
  }
  const knownState = (Object.keys(CHAT_STATE_DOT_CLASSES) as ChatState[]).includes(state as ChatState)
    ? (state as ChatState)
    : "idle";
  const dotClasses = CHAT_STATE_DOT_CLASSES[knownState];
  if (!dotClasses) {
    return null;
  }
  return (
    <span
      aria-label={CHAT_STATE_DOT_LABELS[knownState]}
      title={CHAT_STATE_DOT_LABELS[knownState]}
      className={cn("chat-state-dot size-2 shrink-0 rounded-full", dotClasses)}
    />
  );
}

export function ChatItemCompact({
  chat = null,
  isSelected = false,
  navigateTo = () => {},
  state,
  tags,
}: {
  chat: {
    uuid?: string;
    settings?: { title?: string };
    latest_message?: { text?: string };
  } | null;
  isSelected?: boolean;
  navigateTo: (to: string) => void;
  state?: string;
  tags?: string[];
}) {
  const [settingsOpen, setSettingsOpen] = useState(false);
  const title = chat?.settings?.title?.trim();
  const content = chat?.latest_message?.text || "No messages yet";
  const visibleTags = (tags ?? []).filter((tag) => tag.trim().length > 0);
  const shownTags = visibleTags.slice(0, 3);
  const hiddenTagCount = visibleTags.length - shownTags.length;

  return (
    <ChatSettings chat={chat} open={settingsOpen} setOpen={setSettingsOpen}>
      <Card
        className={cn(
          "chat-list-row",
          settingsOpen && "pointer-events-none",
          isSelected && "chat-list-row--selected"
        )}
        onClick={() => {
          if (!settingsOpen && chat?.uuid) {
            navigateTo(`/chat/${chat.uuid}`);
          }
        }}
      >
        <div className="relative flex items-center gap-2 px-2 py-2">
          <ChatStateDot state={state} />
          <div className="min-w-0 flex-1 pr-8">
            {title ? (
              <>
                <Text type={TextTypes.Body6} tag="span" bold className="block truncate">
                  {title}
                </Text>
                <Text type={TextTypes.Body7} color="muted" className="block truncate">
                  {content}
                </Text>
              </>
            ) : (
              <Text type={TextTypes.Body6} tag="span" className="block min-w-0 truncate">
                {content}
              </Text>
            )}
            {shownTags.length > 0 ? (
              <div className="mt-1 flex flex-wrap items-center gap-1">
                {shownTags.map((tag) => (
                  <span
                    key={tag}
                    className="inline-flex max-w-[7rem] items-center truncate rounded-full border border-border/70 bg-secondary px-1.5 py-[1px] text-[9px] font-medium uppercase tracking-wide text-muted-foreground"
                  >
                    {tag}
                  </span>
                ))}
                {hiddenTagCount > 0 ? (
                  <span className="text-[9px] text-muted-foreground">+{hiddenTagCount}</span>
                ) : null}
              </div>
            ) : null}
          </div>
          <div className="absolute right-1 top-1/2 -translate-y-1/2">
            <DropdownMenuTrigger asChild>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="size-7 shrink-0"
                onClick={(event) => event.stopPropagation()}
                aria-label="Chat settings"
              >
                <DotsHorizontal />
              </Button>
            </DropdownMenuTrigger>
          </div>
        </div>
      </Card>
    </ChatSettings>
  );
}
