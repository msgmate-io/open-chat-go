import React, { useState } from "react";
import { cn } from "../../lib/utils";
import { ChevronDown, EllipsisVertical, Info, Server, Settings2, Wrench } from "lucide-react";
import {
  Badge,
  BotDisplay,
  Button,
  Card,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@open-chat-go/ui";
import { CollapseIndicator } from "../CollapseIndicator";
import { ChatDetailsPanel, type ChatDetailsTabId } from "./ChatDetailsPanel";

const DETAILS_MENU_ITEMS: ReadonlyArray<{
  tab: ChatDetailsTabId;
  label: string;
  Icon: typeof Info;
  hiddenInSharedView?: boolean;
}> = [
  { tab: "overview", label: "Overview", Icon: Info },
  { tab: "init", label: "Tool init", Icon: Settings2, hiddenInSharedView: true },
  { tab: "tools", label: "Browse tools", Icon: Wrench },
  { tab: "mcp", label: "MCP servers", Icon: Server },
];

interface InteractionChatInfoProps {
  chat: any;
  user?: any;
  isSharedView?: boolean;
  showMobileHeaderControls?: boolean;
  leftPannelCollapsed?: boolean;
  onToggleSidebar?: () => void;
  /**
   * Hide the model chip in the header. Integration details panels that render
   * the model in their own card (eg the msgmate coder) set this so the model is
   * shown in one place instead of duplicated in the header.
   */
  hideModelDisplay?: boolean;
  /**
   * Extra items rendered in the header overflow menu. Integrations use this to
   * surface their own actions (eg "Configure msgmate coder") without forking the
   * shared interaction header.
   */
  extraMenuItems?: React.ReactNode;
  /**
   * Actions rendered as a compact, right-aligned row at the bottom of the header
   * card. Integrations use this to float small controls on the chat header (eg
   * the msgmate coder's workspace shell and Open-Chat TUI launchers) without
   * forking the shared interaction header.
   */
  headerActions?: React.ReactNode;
}

type ChatStatusLike = { is_active?: boolean; state?: string } | null | undefined;

// The header badge used to be a static "Active". Derive it from the live chat
// status so the widget reflects what the interaction is actually doing.
function resolveStatusBadge(
  status: ChatStatusLike,
  isSharedView: boolean,
): { label: string; variant: "default" | "secondary" | "destructive" | "outline"; className?: string } | null {
  if (!status) {
    return isSharedView ? { label: "Shared", variant: "outline" } : null;
  }
  if (status.is_active || status.state === "active") {
    return {
      label: "Active",
      variant: "secondary",
      className: "border-amber-300/70 bg-amber-50 text-amber-700 dark:bg-amber-950/30 dark:text-amber-300",
    };
  }
  switch (status.state) {
    case "needs_confirmation":
      return {
        label: "Needs action",
        variant: "secondary",
        className: "border-blue-300/70 bg-blue-50 text-blue-700 dark:bg-blue-950/30 dark:text-blue-300",
      };
    case "failed":
      return { label: "Failed", variant: "destructive" };
    case "finished":
      return {
        label: "Finished",
        variant: "secondary",
        className: "border-emerald-300/70 bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300",
      };
    case "idle":
      return { label: "Idle", variant: "outline" };
    default:
      return { label: status.state || "Idle", variant: "outline" };
  }
}

export function InteractionChatInfo({
  chat,
  user,
  isSharedView = false,
  leftPannelCollapsed = false,
  onToggleSidebar,
  hideModelDisplay = false,
  extraMenuItems,
  headerActions,
}: InteractionChatInfoProps) {
  const [isExpanded, setIsExpanded] = useState(false);
  const [activeTab, setActiveTab] = useState<ChatDetailsTabId>("overview");
  const config = chat?.config ?? {};
  const partner = chat?.partner;
  const partnerInitial = partner?.name?.charAt(0)?.toUpperCase() ?? "B";
  // A user-visible chat title (set manually or by the internal_automations
  // integration) takes precedence over the bot name in the header, mirroring
  // how the chat list labels the row.
  const chatTitle = (chat?.settings?.title || "").trim();
  // The tool catalog, the model payload and the init data all live in the
  // details tabs (ci#749); the header only keeps the toggle and the menu
  // shortcuts into a section.

  const isInteractionChat = chat?.chat_type === "interaction";
  const canViewOnConversation = user?.is_admin === true;
  const statusBadge = resolveStatusBadge(chat?.status, isSharedView);

  if (!chat || (!isInteractionChat && !canViewOnConversation)) {
    return null;
  }

  return (
    <div className="w-full px-2 pb-2 pt-2.5 md:px-4 md:pt-3">
      <Card className="mx-auto w-full border border-border/70 bg-card/95 p-2.5 shadow-sm md:max-w-[960px] md:p-3">
        <div className="flex items-center gap-2">
          {onToggleSidebar ? (
            <CollapseIndicator
              leftPannelCollapsed={leftPannelCollapsed}
              onToggleCollapse={onToggleSidebar}
              className="size-8 shrink-0 rounded-lg border border-border/70 bg-card/80 p-1.5 md:hidden"
            />
          ) : null}
          <div className="flex size-8 shrink-0 items-center justify-center rounded-full bg-primary text-xs font-semibold text-primary-foreground md:size-9 md:text-sm">
            {partnerInitial}
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex min-w-0 items-center gap-2">
              <p className="truncate text-sm font-semibold text-foreground">
                {chatTitle
                  ? chatTitle
                  : `${partner?.name || "Bot"} ${isInteractionChat ? "interaction" : "conversation"}`}
              </p>
              {hideModelDisplay ? null : (
                <BotDisplay
                  selectedModel={config.model}
                  className="max-w-[8rem] shrink-0 rounded-md border-border/60 bg-muted/30 px-1.5 py-0.5 text-[11px] font-medium shadow-none sm:max-w-[12rem]"
                />
              )}
            </div>
          </div>
          {statusBadge ? (
            <Badge
              variant={statusBadge.variant}
              className={cn("hidden shrink-0 md:inline-flex", statusBadge.className)}
            >
              {statusBadge.label}
            </Badge>
          ) : null}
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setIsExpanded((prev) => !prev)}
            aria-expanded={isExpanded}
            aria-controls="interaction-chat-details"
            title={isExpanded ? "Hide chat details" : "Show chat details"}
            className="hidden h-7 shrink-0 gap-1 rounded-full px-2 text-xs text-muted-foreground hover:bg-muted hover:text-foreground sm:inline-flex"
          >
            Details
            <ChevronDown
              className={cn("size-3.5 transition-transform", isExpanded && "rotate-180")}
              aria-hidden
            />
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="size-7 shrink-0 rounded-full text-muted-foreground hover:bg-muted hover:text-foreground"
                aria-label="Chat details options"
                title="Chat details options"
              >
                <EllipsisVertical className="size-4" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-56">
              {DETAILS_MENU_ITEMS.map(({ tab, label, Icon, hiddenInSharedView }) => {
                if (hiddenInSharedView && isSharedView) return null;
                return (
                  <DropdownMenuItem
                    key={tab}
                    onClick={() => {
                      setActiveTab(tab);
                      setIsExpanded(true);
                    }}
                  >
                    <Icon className="mr-2 h-3.5 w-3.5 text-muted-foreground" aria-hidden />
                    {label}
                  </DropdownMenuItem>
                );
              })}
              {extraMenuItems}
              <DropdownMenuItem
                onClick={() => setIsExpanded((prev) => !prev)}
                aria-expanded={isExpanded}
              >
                {isExpanded ? "Hide details" : "Show details"}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>

        {headerActions ? (
          <div className="mt-2 flex flex-wrap items-center justify-end gap-1">{headerActions}</div>
        ) : null}

        {isExpanded && (
          <div id="interaction-chat-details" className="mt-3">
            <ChatDetailsPanel
              chat={chat}
              user={user}
              isSharedView={isSharedView}
              activeTab={activeTab}
              onTabChange={setActiveTab}
            />
          </div>
        )}
      </Card>
    </div>
  );
}
