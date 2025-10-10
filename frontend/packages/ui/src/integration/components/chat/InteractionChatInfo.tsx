import React, { useMemo, useState } from "react";
import useSWR from "swr";
import { fetcher } from "../../lib/utils";
import { FileCode2, Info, EllipsisVertical } from "lucide-react";
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
  Text,
  TextTypes,
} from "@open-chat-go/ui";
import { CollapseIndicator } from "../CollapseIndicator";

type ToolRow = {
  name: string;
  function_name: string;
  description: string;
  type: string;
  requires_init?: boolean;
  requires_confirmation?: boolean;
  stop_on_first_confirmable_tool_call?: boolean;
  confirmation_block_message?: string;
  required?: string[];
  call_schema?: Record<string, unknown>;
  init_schema?: Record<string, unknown>;
};

type ToolsResponse = {
  rows: ToolRow[];
};

function normalizeToolName(toolName: string): string {
  if (!toolName.includes(":")) return toolName;
  const parts = toolName.split(":");
  return parts[parts.length - 1] || toolName;
}

function SchemaBlock({ title, schema }: { title: string; schema: Record<string, unknown> | undefined }) {
  const pretty = JSON.stringify(schema ?? { type: "object", properties: {} }, null, 2);
  return (
    <div className="rounded-xl border border-border/70 bg-card/90 p-3">
      <div className="mb-2 flex items-center gap-2">
        <FileCode2 className="h-4 w-4 text-muted-foreground" />
        <Text type={TextTypes.Body6} bold>{title}</Text>
      </div>
      <pre className="max-h-72 overflow-auto rounded-lg border border-border/60 bg-muted/30 p-3 text-xs leading-5">
        {pretty}
      </pre>
    </div>
  );
}

interface InteractionChatInfoProps {
  chat: any;
  user?: any;
  isSharedView?: boolean;
  showMobileHeaderControls?: boolean;
  leftPannelCollapsed?: boolean;
  onToggleSidebar?: () => void;
}

export function InteractionChatInfo({
  chat,
  user,
  isSharedView = false,
  leftPannelCollapsed = false,
  onToggleSidebar,
}: InteractionChatInfoProps) {
  const [isExpanded, setIsExpanded] = useState(false);
  const [selectedToolName, setSelectedToolName] = useState<string | null>(null);
  const [showModelPayload, setShowModelPayload] = useState(false);
  const [showToolInitDialog, setShowToolInitDialog] = useState(false);
  const config = chat?.config ?? {};
  const partner = chat?.partner;
  const partnerInitial = partner?.name?.charAt(0)?.toUpperCase() ?? "B";
  const { data: toolsData } = useSWR<ToolsResponse>("/api/v1/tools?page=1&page_size=200", fetcher);
  const toolsByName = useMemo(() => {
    const rows = toolsData?.rows ?? [];
    const map = new Map<string, ToolRow>();
    rows.forEach((tool) => map.set(tool.name, tool));
    return map;
  }, [toolsData?.rows]);
  const selectedTool = selectedToolName ? toolsByName.get(normalizeToolName(selectedToolName)) : null;
  const selectedToolPayload = useMemo(() => {
    if (!selectedTool) return null;
    const requiredParams = selectedTool.required ?? [];
    return {
      type: "function",
      function: {
        name: selectedTool.name,
        description: selectedTool.description || "",
        parameters: {
          type: "object",
          properties: selectedTool.call_schema?.properties ?? {},
          required: requiredParams,
          description: "The parameters for the tool",
        },
      },
    };
  }, [selectedTool]);
  const integrationEntries = useMemo(() => {
    const toolInit = config?.tool_init;
    if (!toolInit || typeof toolInit !== "object") {
      return [] as Array<[string, any]>;
    }
    return Object.entries(toolInit);
  }, [config?.tool_init]);

  const isInteractionChat = chat?.chat_type === "interaction";
  const canViewOnConversation = user?.is_admin === true;

  if (!chat || (!isInteractionChat && !canViewOnConversation)) {
    return null;
  }

  return (
    <div className="w-full px-2 pb-2 pt-2.5 md:px-4 md:pt-3">
      <Card className="mx-auto w-full border border-border/70 bg-card/95 p-2.5 shadow-sm md:max-w-[900px] md:p-3">
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
            <p className="truncate text-sm font-semibold text-foreground">
              {partner?.name || "Bot"} {isInteractionChat ? "interaction" : "conversation"}
            </p>
            <BotDisplay
              selectedModel={config.model}
              className="max-w-[14rem] rounded-lg border-border/60 bg-card/80 px-2 py-0.5 text-xs shadow-none md:max-w-[18rem] md:px-2.5 md:py-1 md:text-[13px]"
            />
          </div>
          <Badge variant="secondary" className="hidden shrink-0 md:inline-flex">
            Active
          </Badge>
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
            <DropdownMenuContent align="end" className="w-44">
              {!isSharedView ? (
                <DropdownMenuItem onClick={() => setShowToolInitDialog(true)}>
                  View tool init
                </DropdownMenuItem>
              ) : null}
              <DropdownMenuItem
                onClick={() => setIsExpanded((prev) => !prev)}
                aria-expanded={isExpanded}
              >
                {isExpanded ? "Hide details" : "Show details"}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>

        {isExpanded && (
          <div className="mt-3 grid grid-cols-1 gap-2.5 md:grid-cols-2 xl:grid-cols-3">
            <div className="rounded-xl border border-border/70 bg-muted/30 p-3 text-sm">
              <p className="mb-2 font-medium text-foreground">Model</p>
              <div className="space-y-1 text-muted-foreground">
                <p>Model: <span className="text-foreground">{config.model ?? "-"}</span></p>
                <p>Backend: <span className="text-foreground">{config.backend ?? "-"}</span></p>
                <p>Max tokens: <span className="text-foreground">{config.max_tokens ?? "-"}</span></p>
                <p>Temperature: <span className="text-foreground">{config.temperature ?? "-"}</span></p>
                <p>Context: <span className="text-foreground">{config.context ?? "-"}</span></p>
              </div>
            </div>

            <div className="rounded-xl border border-border/70 bg-muted/30 p-3 text-sm">
                <p className="mb-2 font-medium text-foreground">Tools</p>
                <div className="flex flex-wrap gap-2">
                  {Array.isArray(config.tools) && config.tools.length > 0 ? (
                    config.tools.map((tool: string) => (
                      <Button
                        key={tool}
                        variant="outline"
                        size="sm"
                        className="h-7 px-2 text-xs"
                        onClick={() => setSelectedToolName(tool)}
                        title={`Open definition for ${normalizeToolName(tool)}`}
                      >
                        <Info className="mr-1 h-3.5 w-3.5" />
                        {tool}
                      </Button>
                    ))
                  ) : (
                    <p className="text-muted-foreground">No tools configured</p>
                )}
              </div>
            </div>

            <div className="rounded-xl border border-border/70 bg-muted/30 p-3 text-sm">
              <p className="mb-2 font-medium text-foreground">Integration</p>
              {integrationEntries.length > 0 ? (
                <div className="space-y-1 text-muted-foreground">
                  {integrationEntries.map(([name, value]) => (
                    <p key={name}>
                      {name.replace("signal_", "").replace(/_/g, " ")}: <span className="text-foreground">{typeof value === "object" ? "configured" : String(value)}</span>
                    </p>
                  ))}
                  {config?.tool_init?.signal_send_message?.recipient_phone ? (
                    <p>
                      Phone: <span className="text-foreground">{config.tool_init.signal_send_message.recipient_phone}</span>
                    </p>
                  ) : null}
                </div>
              ) : (
                <p className="text-muted-foreground">No integration details available</p>
              )}
            </div>

            {config?.system_prompt ? (
              <div className="rounded-xl border border-border/70 bg-muted/30 p-3 text-sm md:col-span-2 xl:col-span-3">
                <p className="mb-2 font-medium text-foreground">System prompt</p>
                <p className="max-h-28 overflow-y-auto whitespace-pre-wrap text-muted-foreground">
                  {config.system_prompt}
                </p>
              </div>
            ) : null}
          </div>
        )}
      </Card>

      <Dialog open={Boolean(selectedToolName)} onOpenChange={(open) => {
        if (!open) {
          setSelectedToolName(null);
          setShowModelPayload(false);
        }
      }}>
        <DialogContent className="max-h-[88vh] max-w-4xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle className="break-all">{normalizeToolName(selectedToolName || "") || "Tool"}</DialogTitle>
            <DialogDescription className="break-words">
              {selectedTool?.description || "No tool metadata found in the catalog API for this tool."}
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4">
            <div className="rounded-xl border border-border/70 bg-gradient-to-r from-muted/20 to-card p-3">
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant="outline">Type: {selectedTool?.type || "unknown"}</Badge>
                <Badge variant={selectedTool?.requires_init ? "secondary" : "outline"}>
                  {selectedTool?.requires_init ? "Init required" : "No init required"}
                </Badge>
                <Badge variant={selectedTool?.requires_confirmation ? "secondary" : "outline"}>
                  {selectedTool?.requires_confirmation ? "Requires confirmation" : "No confirmation"}
                </Badge>
                {selectedTool?.requires_confirmation ? (
                  <Badge variant={selectedTool?.stop_on_first_confirmable_tool_call ? "secondary" : "outline"}>
                    {selectedTool?.stop_on_first_confirmable_tool_call ? "Stop on first confirmable call" : "Continue after confirm request"}
                  </Badge>
                ) : null}
                {(selectedTool?.required ?? []).length > 0 ? (
                  <Badge variant="outline">Required input: {(selectedTool?.required ?? []).join(", ")}</Badge>
                ) : (
                  <Badge variant="outline">No required input params</Badge>
                )}
                <Button size="sm" variant="outline" onClick={() => setShowModelPayload((prev) => !prev)}>
                  {showModelPayload ? "Hide model API payload" : "View model API payload"}
                </Button>
              </div>
            </div>

            {showModelPayload ? (
              <div className="rounded-xl border border-border/70 bg-card/90 p-3">
                <Text type={TextTypes.Body6} bold>Exact tool object sent to the model API</Text>
                <Text type={TextTypes.Body7} color="muted" className="mt-1">
                  This is the full tool definition payload included in the `tools` array for chat completions.
                </Text>
                <pre className="mt-3 max-h-80 overflow-auto rounded-lg border border-border/60 bg-muted/30 p-3 text-xs leading-5">
                  {JSON.stringify(selectedToolPayload, null, 2)}
                </pre>
              </div>
            ) : null}

            {selectedTool?.requires_confirmation && selectedTool?.confirmation_block_message ? (
              <div className="rounded-xl border border-border/70 bg-card/90 p-3">
                <Text type={TextTypes.Body6} bold>Confirmation Block Message</Text>
                <Text type={TextTypes.Body7} color="muted" className="mt-1">
                  Message returned to the model while awaiting approval.
                </Text>
                <pre className="mt-3 max-h-48 overflow-auto rounded-lg border border-border/60 bg-muted/30 p-3 text-xs leading-5">
                  {selectedTool.confirmation_block_message}
                </pre>
              </div>
            ) : null}

            <div className="grid gap-3 md:grid-cols-2">
              <SchemaBlock title="Input Parameters (Call Schema)" schema={selectedTool?.call_schema} />
              <SchemaBlock title="Init Parameters (Init Schema)" schema={selectedTool?.init_schema} />
            </div>
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={showToolInitDialog} onOpenChange={setShowToolInitDialog}>
        <DialogContent className="max-h-[85vh] max-w-3xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle>Current tool init data</DialogTitle>
            <DialogDescription>
              This is the currently configured `tool_init` object used by this interaction.
            </DialogDescription>
          </DialogHeader>
          <pre className="max-h-[60vh] overflow-auto rounded-lg border border-border/60 bg-muted/30 p-3 text-xs leading-5">
            {JSON.stringify(config?.tool_init ?? {}, null, 2)}
          </pre>
        </DialogContent>
      </Dialog>
    </div>
  );
}
