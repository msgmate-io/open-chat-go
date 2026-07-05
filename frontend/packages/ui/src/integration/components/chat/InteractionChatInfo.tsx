import React, { useState } from "react";
import { cn } from "../../lib/utils";
import { EllipsisVertical, Info, Maximize2, Server, Settings2, Wrench } from "lucide-react";
import {
  Badge,
  BotDisplay,
  Button,
  Card,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@open-chat-go/ui";
import { CollapseIndicator } from "../CollapseIndicator";
import { ChatDetailsPanel, type ChatDetailsTabId } from "./ChatDetailsPanel";

/**
 * Chat details (ci#788) live in their own modal instead of expanding the chat
 * header in place: the header stays compact on every breakpoint and the tabbed
 * details surface gets the full viewport it needs, including on mobile.
 */
export const CHAT_DETAILS_DIALOG_ID = "interaction-chat-details";

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

/**
 * The tabbed chat-details surface rendered inside its own centered modal
 * (ci#788). Header and content are exposed separately so the dialog can also be
 * used standalone (eg on the shared-view page) and so tests can pin the header
 * contract without a live dialog instance.
 */
export function ChatDetailsDialogHeader({ isSharedView = false }: { isSharedView?: boolean }) {
  return (
    <DialogHeader className="pr-8 text-left">
      <DialogTitle className="text-base">Chat details</DialogTitle>
      <DialogDescription className="text-xs">
        {isSharedView
          ? "Read-only overview of this shared chat."
          : "Overview of this chat, its configured tools, tool init payload and MCP servers."}
      </DialogDescription>
    </DialogHeader>
  );
}

interface ChatDetailsDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  chat: any;
  user?: any;
  isSharedView?: boolean;
  activeTab: ChatDetailsTabId;
  onTabChange: (tab: ChatDetailsTabId) => void;
}

export function ChatDetailsDialog({
  open,
  onOpenChange,
  chat,
  user,
  isSharedView = false,
  activeTab,
  onTabChange,
}: ChatDetailsDialogProps) {
  if (!chat) return null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        id={CHAT_DETAILS_DIALOG_ID}
        aria-label="Chat details"
        // Centered sheet on small screens, roomy panel on desktop: the details
        // surface replaced the header expansion so it must stay usable on every
        // breakpoint (ci#788).
        //
        // Desktop gets a taller, wider panel with a single scroll region so the
        // details no longer need a scrollbar per section (ci#831).
        className="max-h-[90dvh] w-[calc(100vw-1.5rem)] max-w-[calc(100vw-1.5rem)] gap-3 overflow-hidden p-4 sm:max-w-[640px] sm:p-5 lg:h-[min(88dvh,52rem)] lg:max-h-[88dvh] lg:w-[min(92vw,80rem)] lg:max-w-[80rem] lg:p-6 xl:max-w-[88rem]"
      >
        <ChatDetailsDialogHeader isSharedView={isSharedView} />
        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain pr-1">
          <ChatDetailsPanel
            chat={chat}
            user={user}
            isSharedView={isSharedView}
            activeTab={activeTab}
            onTabChange={onTabChange}
          />
        </div>
      </DialogContent>
    </Dialog>
  );
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
  const [isDetailsOpen, setIsDetailsOpen] = useState(false);
  const [activeTab, setActiveTab] = useState<ChatDetailsTabId>("overview");
  // Opening a section from the overflow menu also selects its tab, so the menu
  // stays a shortcut into the details modal instead of a second surface.
  const openDetails = (tab?: ChatDetailsTabId) => {
    if (tab) setActiveTab(tab);
    setIsDetailsOpen(true);
  };
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
          {/*
            Desktop keeps the integration controls (eg the msgmate coder's
            workspace Shell and Open-Chat TUI launchers) on the same line as the
            rest of the widget so the header stays one compact row (ci#831); on
            small screens they fall back to their own wrapped row below.
          */}
          {headerActions ? (
            <div className="hidden shrink-0 items-center justify-end gap-1 md:flex">
              {headerActions}
            </div>
          ) : null}
          <Button
            variant="ghost"
            size="sm"
            onClick={() => openDetails()}
            aria-haspopup="dialog"
            aria-controls={CHAT_DETAILS_DIALOG_ID}
            title="Show chat details"
            className="hidden h-7 shrink-0 gap-1 rounded-full px-2 text-xs text-muted-foreground hover:bg-muted hover:text-foreground sm:inline-flex"
          >
            Details
            <Maximize2 className="size-3.5" aria-hidden />
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
                  <DropdownMenuItem key={tab} onClick={() => openDetails(tab)}>
                    <Icon className="mr-2 h-3.5 w-3.5 text-muted-foreground" aria-hidden />
                    {label}
                  </DropdownMenuItem>
                );
              })}
              {extraMenuItems}
              <DropdownMenuItem onClick={() => openDetails()}>Show details</DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>

        {/* Small screens keep the integration controls on their own wrapped row;
            desktop renders them inline in the header row above (ci#831). */}
        {headerActions ? (
          <div className="mt-2 flex flex-wrap items-center justify-end gap-1 md:hidden">{headerActions}</div>
        ) : null}
      </Card>

      <ChatDetailsDialog
        open={isDetailsOpen}
        onOpenChange={setIsDetailsOpen}
        chat={chat}
        user={user}
        isSharedView={isSharedView}
        activeTab={activeTab}
        onTabChange={setActiveTab}
      />
    </div>
  );
}
