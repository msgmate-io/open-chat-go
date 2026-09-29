import { useEffect, useMemo, useState, type FocusEvent } from "react";
import { ChatBase } from "@open-chat-go/ui";
import { SearchableMultiSelectSection } from "@/components/chat/SearchableMultiSelectSection";
import { ToolInitFields } from "@/components/chat/ToolInitFields";
import { ExpandableTextareaField } from "@/components/chat/ExpandableTextareaField";
import { fetcher } from "@/lib/utils";
import {
  asToolInitMap,
  buildToolInitPayloadForSelectedTools,
  getMissingRequiredToolInitFields,
  pickToolInitForTools,
  resolveRequiredToolInitDescriptors,
} from "@/lib/tool-init";
import useSWR from "swr";
import { navigate } from "vike/client/router";
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  Input,
  LoadingSpinner,
  Separator,
  Text,
  TextTypes,
} from "@open-chat-go/ui";

type PermissionsResponse = { rows: string[] };
type SelfUser = { is_admin?: boolean };

type ModelsResponse = {
  rows: Array<{
    model_id: string;
    configuration?: { backend?: string; endpoint?: string };
  }>;
};

type ToolsResponse = {
  rows: Array<{
    name: string;
    description?: string;
    type?: string;
    requires_init?: boolean;
    init_schema?: Record<string, unknown>;
  }>;
};

type CreateBotResponse = {
  bot: { name: string; bot_contact_token: string };
  generated_password?: string;
};

type MCPServerRow = {
  name: string;
  enabled: boolean;
  auth_connected: boolean;
};

type MCPServersResponse = { rows: MCPServerRow[] };
type MCPDiscoverResponse = { tools?: Array<{ name?: string }> };

const uniq = (values: string[]) => Array.from(new Set(values.map((v) => v.trim()).filter(Boolean)));

export default function Page() {
  const formScrollInset =
    "max(var(--openchat-keyboard-bottom, 0px), var(--openchat-keyboard-bottom-visual, 0px), var(--openchat-safe-bottom, 0px))";

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [backend, setBackend] = useState("litellm");
  const [endpoint, setEndpoint] = useState("https://litellm.t1m.me/v1");
  const [systemPrompt, setSystemPrompt] = useState("You are a helpful assistant.");
  const [temperature, setTemperature] = useState("0.7");
  const [maxTokens, setMaxTokens] = useState("4096");
  const [context, setContext] = useState("10");

  const [selectedModels, setSelectedModels] = useState<string[]>([]);
  const [customModel, setCustomModel] = useState("");
  const [isModelDialogOpen, setIsModelDialogOpen] = useState(false);
  const [isToolsDialogOpen, setIsToolsDialogOpen] = useState(false);

  const [selectedIntegrations, setSelectedIntegrations] = useState<string[]>([]);
  const [discoveredMCPTools, setDiscoveredMCPTools] = useState<Record<string, string[]>>({});
  const [selectedMCPTools, setSelectedMCPTools] = useState<string[]>([]);
  const [selectedCoreTools, setSelectedCoreTools] = useState<string[]>([]);
  const [manualToolsText, setManualToolsText] = useState("");
  const [discoveringByIntegration, setDiscoveringByIntegration] = useState<Record<string, boolean>>({});
  const [discoverStatusByIntegration, setDiscoverStatusByIntegration] = useState<Record<string, string>>({});
  const [toolInitData, setToolInitData] = useState<Record<string, Record<string, unknown>>>({});

  const [isSubmitting, setIsSubmitting] = useState(false);
  const [errorText, setErrorText] = useState("");
  const [created, setCreated] = useState<CreateBotResponse | null>(null);

  const { data: permissions, isLoading: permissionsLoading } = useSWR<PermissionsResponse>(
    "/api/v1/user/permissions",
    fetcher,
  );
  const { data: selfUser } = useSWR<SelfUser>("/api/v1/user/self", fetcher);
  const { data: models } = useSWR<ModelsResponse>("/api/v1/models?page=1&page_size=300", fetcher);
  const { data: toolsData } = useSWR<ToolsResponse>("/api/v1/tools?page=1&page_size=400", fetcher);
  const { data: mcpServers } = useSWR<MCPServersResponse>("/api/v1/integrations/mcp/servers", fetcher);

  const canCreateBots = Boolean(permissions?.rows?.includes("create_bots"));

  const availableModels = useMemo(() => {
    const ids = uniq((models?.rows ?? []).map((row) => row.model_id));
    ids.sort((a, b) => a.localeCompare(b));
    return ids;
  }, [models?.rows]);

  const suggestedBackends = useMemo(() => {
    const values = (models?.rows ?? [])
      .map((row) => row.configuration?.backend)
      .filter((value): value is string => Boolean(value));
    return uniq(values).slice(0, 12);
  }, [models?.rows]);

  const modelConfigByID = useMemo(() => {
    const out: Record<string, { backend?: string; endpoint?: string }> = {};
    for (const row of models?.rows ?? []) {
      out[row.model_id] = {
        backend: row.configuration?.backend,
        endpoint: row.configuration?.endpoint,
      };
    }
    return out;
  }, [models?.rows]);

  useEffect(() => {
    if (selectedModels.length === 0 && availableModels.length > 0) {
      const preferred = availableModels.find((item) => item === "qwen3-8b-instruct_vllm") || availableModels[0];
      setSelectedModels([preferred]);
    }
  }, [availableModels, selectedModels.length]);

  const availableIntegrations = useMemo(
    () => (mcpServers?.rows ?? []).filter((row) => row.enabled).sort((a, b) => a.name.localeCompare(b.name)),
    [mcpServers?.rows],
  );

  const availableMCPTools = useMemo(() => {
    const rows: string[] = [];
    for (const integrationName of selectedIntegrations) {
      rows.push(...(discoveredMCPTools[integrationName] ?? []));
    }
    rows.sort((a, b) => a.localeCompare(b));
    return uniq(rows);
  }, [discoveredMCPTools, selectedIntegrations]);

  const availableCoreTools = useMemo(() => {
    const rows = (toolsData?.rows ?? [])
      .map((row) => row.name)
      .filter((name) => !name.toLowerCase().startsWith("mcp:"));
    rows.sort((a, b) => a.localeCompare(b));
    return uniq(rows);
  }, [toolsData?.rows]);

  const allSelectedTools = useMemo(() => {
    const manual = manualToolsText
      .split(",")
      .map((value) => value.trim())
      .filter(Boolean);
    return uniq([...selectedCoreTools, ...selectedMCPTools, ...manual]);
  }, [manualToolsText, selectedCoreTools, selectedMCPTools]);

  const requiredToolInitDescriptors = useMemo(
    () => resolveRequiredToolInitDescriptors(allSelectedTools, toolsData?.rows ?? []),
    [allSelectedTools, toolsData?.rows],
  );

  useEffect(() => {
    setToolInitData((current) => {
      const next = pickToolInitForTools(requiredToolInitDescriptors, asToolInitMap(current));
      for (const descriptor of requiredToolInitDescriptors) {
        if (!next[descriptor.configuredName]) {
          next[descriptor.configuredName] = {};
        }
      }
      return next;
    });
  }, [requiredToolInitDescriptors]);

  const primaryModel = selectedModels[0] || "";

  useEffect(() => {
    if (!primaryModel) return;
    const config = modelConfigByID[primaryModel];
    if (!config) return;
    if (config.backend && config.backend.trim()) {
      setBackend(config.backend.trim());
    }
    if (config.endpoint && config.endpoint.trim()) {
      setEndpoint(config.endpoint.trim());
    }
  }, [modelConfigByID, primaryModel]);

  const toggleModel = (modelID: string) => {
    if (selectedModels.includes(modelID)) {
      if (selectedModels.length === 1) return;
      setSelectedModels((current) => current.filter((item) => item !== modelID));
      return;
    }
    setSelectedModels((current) => uniq([...current, modelID]));
  };

  const addCustomModel = () => {
    const normalized = customModel.trim();
    if (!normalized) return;
    setSelectedModels((current) => uniq([...current, normalized]));
    setCustomModel("");
  };

  const toggleIntegration = (integrationName: string) => {
    if (selectedIntegrations.includes(integrationName)) {
      setSelectedIntegrations((current) => current.filter((name) => name !== integrationName));
      setDiscoveredMCPTools((current) => {
        const next = { ...current };
        delete next[integrationName];
        return next;
      });
      setSelectedMCPTools((current) =>
        current.filter((toolName) => !toolName.startsWith(`mcp:${integrationName}:`)),
      );
      return;
    }
    setSelectedIntegrations((current) => uniq([...current, integrationName]));
  };

  const discoverIntegrationTools = async (integrationName: string) => {
    setDiscoveringByIntegration((current) => ({ ...current, [integrationName]: true }));
    setDiscoverStatusByIntegration((current) => ({ ...current, [integrationName]: "" }));
    setErrorText("");
    try {
      const response = await fetch(
        `/api/v1/integrations/mcp/servers/${encodeURIComponent(integrationName)}/discover`,
        {
          method: "POST",
          credentials: "include",
        },
      );
      if (!response.ok) {
        const message = await response.text();
        throw new Error(message || `Failed to discover tools for ${integrationName}.`);
      }
      const payload = (await response.json()) as MCPDiscoverResponse;
      const namespacedTools = uniq(
        (payload.tools ?? [])
          .map((tool) => String(tool?.name || "").trim())
          .filter(Boolean)
          .map((toolName) => `mcp:${integrationName}:${toolName}`),
      );
      setDiscoveredMCPTools((current) => ({ ...current, [integrationName]: namespacedTools }));
      setSelectedMCPTools((current) => uniq([...current, ...namespacedTools]));
      setDiscoverStatusByIntegration((current) => ({
        ...current,
        [integrationName]: `Loaded ${namespacedTools.length} tools.`,
      }));
    } catch (error) {
      const message = error instanceof Error ? error.message : "Failed to discover MCP tools.";
      setErrorText(message);
      setDiscoverStatusByIntegration((current) => ({
        ...current,
        [integrationName]: message,
      }));
    } finally {
      setDiscoveringByIntegration((current) => ({ ...current, [integrationName]: false }));
    }
  };

  const onCreateBot = async () => {
    if (!name.trim() || !primaryModel || !backend.trim()) {
      setErrorText("Name, at least one selected model, and backend are required.");
      return;
    }

    setIsSubmitting(true);
    setErrorText("");

    const defaultSharedConfig: Record<string, unknown> = {
      model: primaryModel,
      backend: backend.trim(),
    };
    if (selectedModels.length > 1) {
      defaultSharedConfig.model_candidates = selectedModels;
    }
    if (selectedIntegrations.length > 0) {
      defaultSharedConfig.integrations = selectedIntegrations;
    }
    if (allSelectedTools.length > 0) {
      defaultSharedConfig.tools = allSelectedTools;
    }
    if (requiredToolInitDescriptors.length > 0) {
      const missingRequiredFields = getMissingRequiredToolInitFields(requiredToolInitDescriptors, toolInitData);
      if (missingRequiredFields.length > 0) {
        setErrorText(`Missing required tool init fields: ${missingRequiredFields.join(", ")}`);
        setIsSubmitting(false);
        return;
      }
    }
    if (allSelectedTools.length > 0) {
      defaultSharedConfig.tool_init = buildToolInitPayloadForSelectedTools(allSelectedTools, toolInitData);
    }

    if (endpoint.trim()) {
      defaultSharedConfig.endpoint = endpoint.trim();
    }
    if (systemPrompt.trim()) {
      defaultSharedConfig.system_prompt = systemPrompt.trim();
    }
    if (temperature.trim()) {
      const parsedTemperature = Number(temperature);
      if (!Number.isFinite(parsedTemperature)) {
        setErrorText("Temperature must be a valid number.");
        setIsSubmitting(false);
        return;
      }
      defaultSharedConfig.temperature = parsedTemperature;
    }
    if (maxTokens.trim()) {
      const parsedMaxTokens = Number(maxTokens);
      if (!Number.isInteger(parsedMaxTokens) || parsedMaxTokens < 1) {
        setErrorText("Max tokens must be a positive integer.");
        setIsSubmitting(false);
        return;
      }
      defaultSharedConfig.max_tokens = parsedMaxTokens;
    }
    if (context.trim()) {
      const parsedContext = Number(context);
      if (!Number.isInteger(parsedContext) || parsedContext < 1) {
        setErrorText("Context must be a positive integer.");
        setIsSubmitting(false);
        return;
      }
      defaultSharedConfig.context = parsedContext;
    }

    try {
      const response = await fetch("/api/v1/bots", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: name.trim(),
          description: description.trim(),
          default_shared_config: defaultSharedConfig,
        }),
      });

      if (!response.ok) {
        const message = await response.text();
        throw new Error(message || `Failed to create bot (${response.status})`);
      }

      const payload = (await response.json()) as CreateBotResponse;
      setCreated(payload);
    } catch (error) {
      setErrorText(error instanceof Error ? error.message : "Failed to create bot.");
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleFormFocusCapture = (event: FocusEvent<HTMLDivElement>) => {
    const target = event.target;
    if (!(target instanceof HTMLElement)) {
      return;
    }

    const isMobileRuntime =
      typeof document !== "undefined" &&
      document.documentElement.getAttribute("data-openchat-runtime") === "mobile";
    if (!isMobileRuntime) {
      return;
    }

    const tag = target.tagName.toLowerCase();
    const isInputLike = tag === "input" || tag === "textarea" || tag === "select" || target.isContentEditable;
    if (!isInputLike) {
      return;
    }

    window.setTimeout(() => {
      target.scrollIntoView({ block: "center", inline: "nearest" });
    }, 120);
  };

  return (
    <ChatBase chatUUID={null} navigateTo={(to: string) => navigate(to)}>
      <div
        className="mx-auto flex h-full min-h-0 w-full max-w-4xl flex-col gap-4 overflow-y-auto px-4 pt-6 md:px-6"
        style={{
          paddingBottom: `calc(${formScrollInset} + 1.5rem)`,
          scrollPaddingBottom: `calc(${formScrollInset} + 1rem)`,
        }}
        onFocusCapture={handleFormFocusCapture}
      >
        <Card className="border-border/70 bg-gradient-to-br from-background via-background to-muted/30">
          <CardHeader>
            <CardTitle>Create Bot</CardTitle>
            <CardDescription>
              Configure runtime defaults, pick model presets, and choose tools for this bot.
            </CardDescription>
          </CardHeader>
        </Card>

        {permissionsLoading ? (
          <div className="flex h-40 items-center justify-center">
            <LoadingSpinner />
          </div>
        ) : !canCreateBots ? (
          <Card>
            <CardHeader>
              <CardTitle>Permission required</CardTitle>
              <CardDescription>You need the `create_bots` permission to create a bot.</CardDescription>
            </CardHeader>
            <CardContent>
              <Button variant="outline" onClick={() => navigate("/chats/bots")}>Back to bots</Button>
            </CardContent>
          </Card>
        ) : created ? (
          <Card className="border-primary/30">
            <CardHeader>
              <CardTitle>Bot created</CardTitle>
              <CardDescription>{created.bot.name} is ready. You can start chatting right away.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              {created.generated_password && selfUser?.is_admin ? (
                <div className="rounded-md border border-amber-300 bg-amber-50 p-3">
                  <Text type={TextTypes.Body6} bold>Generated password (shown once)</Text>
                  <Text type={TextTypes.Body7} className="break-all font-mono">{created.generated_password}</Text>
                </div>
              ) : null}
              <div className="flex flex-wrap gap-2">
                <Button onClick={() => navigate(`/chat/new/${created.bot.bot_contact_token}`)}>Open chat with bot</Button>
                <Button variant="outline" onClick={() => navigate("/chats/bots")}>Back to bots</Button>
              </div>
            </CardContent>
          </Card>
        ) : (
          <Card>
            <CardContent className="space-y-5 pt-6">
              <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                <div className="space-y-2 md:col-span-2">
                  <Text type={TextTypes.Body6} bold>Bot name</Text>
                  <Input value={name} onChange={(event) => setName(event.target.value)} placeholder="Support Bot" />
                  <Text type={TextTypes.Body7} color="muted">Display name shown in the bot list and chat header.</Text>
                </div>

                <div className="md:col-span-2">
                  <ExpandableTextareaField
                    label="Description"
                    value={description}
                    onChange={setDescription}
                    placeholder="Describe what this bot should help with"
                    minHeightClassName="min-h-11"
                    hint="Short summary shown in the bot list."
                  />
                </div>

                <button
                  type="button"
                  className="rounded-lg border border-border/70 bg-muted/20 p-4 text-left transition hover:bg-muted/30"
                  onClick={() => setIsModelDialogOpen(true)}
                >
                  <Text type={TextTypes.Body5} bold>Select Models</Text>
                  <Text type={TextTypes.Body6} color="muted">
                    {selectedModels.length} selected{primaryModel ? `, primary: ${primaryModel}` : ""}
                  </Text>
                </button>

                <button
                  type="button"
                  className="rounded-lg border border-border/70 bg-muted/20 p-4 text-left transition hover:bg-muted/30"
                  onClick={() => setIsToolsDialogOpen(true)}
                >
                  <Text type={TextTypes.Body5} bold>Select Tools</Text>
                  <Text type={TextTypes.Body6} color="muted">
                    {allSelectedTools.length} selected across core + MCP tools
                  </Text>
                </button>

                <div className="space-y-2 md:col-span-2">
                  <Text type={TextTypes.Body6} bold>Tool init parameters</Text>
                  <ToolInitFields
                    descriptors={requiredToolInitDescriptors}
                    value={toolInitData}
                    onChange={setToolInitData}
                    emptyHint="No selected tools currently require init parameters."
                  />
                </div>

                <Separator className="md:col-span-2" />

                <div className="space-y-2">
                  <Text type={TextTypes.Body6} bold>Backend</Text>
                  <Input
                    value={backend}
                    onChange={(event) => setBackend(event.target.value)}
                    placeholder="openai"
                    list="bot-create-backend-suggestions"
                  />
                  <Text type={TextTypes.Body7} color="muted">Provider backend used to route this bot&apos;s requests.</Text>
                  <datalist id="bot-create-backend-suggestions">
                    {suggestedBackends.map((suggestedBackend) => (
                      <option value={suggestedBackend} key={suggestedBackend} />
                    ))}
                  </datalist>
                </div>

                <div className="space-y-2">
                  <Text type={TextTypes.Body6} bold>Endpoint (optional)</Text>
                  <Input
                    value={endpoint}
                    onChange={(event) => setEndpoint(event.target.value)}
                    placeholder="https://api.openai.com/v1"
                  />
                  <Text type={TextTypes.Body7} color="muted">Custom API base URL; leave empty to use the backend default.</Text>
                </div>

                <Separator className="md:col-span-2" />

                <div className="md:col-span-2">
                  <ExpandableTextareaField
                    label="System prompt (optional)"
                    value={systemPrompt}
                    onChange={setSystemPrompt}
                    placeholder="You are a concise, helpful assistant focused on developer support."
                    minHeightClassName="min-h-32"
                    hint="Instructions that define how this bot behaves."
                    expandable
                  />
                </div>

                <div className="space-y-2">
                  <Text type={TextTypes.Body6} bold>Temperature (optional)</Text>
                  <Input value={temperature} onChange={(event) => setTemperature(event.target.value)} placeholder="0.7" />
                  <Text type={TextTypes.Body7} color="muted">Sampling randomness; lower is more deterministic.</Text>
                </div>

                <div className="space-y-2">
                  <Text type={TextTypes.Body6} bold>Max tokens (optional)</Text>
                  <Input value={maxTokens} onChange={(event) => setMaxTokens(event.target.value)} placeholder="1024" />
                  <Text type={TextTypes.Body7} color="muted">Maximum number of tokens generated per reply.</Text>
                </div>

                <div className="space-y-2">
                  <Text type={TextTypes.Body6} bold>Context (optional)</Text>
                  <Input value={context} onChange={(event) => setContext(event.target.value)} placeholder="8192" />
                  <Text type={TextTypes.Body7} color="muted">Context window size passed to the backend.</Text>
                </div>
              </div>

              {errorText ? <Text type={TextTypes.Body6} color="destructive">{errorText}</Text> : null}

              <div className="flex flex-wrap gap-2">
                <Button onClick={onCreateBot} disabled={isSubmitting}>{isSubmitting ? "Creating..." : "Create bot"}</Button>
                <Button variant="outline" onClick={() => navigate("/chats/bots")}>Cancel</Button>
              </div>
            </CardContent>
          </Card>
        )}
      </div>

      <Dialog open={isModelDialogOpen} onOpenChange={setIsModelDialogOpen}>
        <DialogContent className="max-h-[92vh] max-w-4xl overflow-y-auto rounded-2xl border-border/70 p-0">
          <DialogHeader>
            <DialogTitle className="px-6 pt-6">Select Models</DialogTitle>
            <DialogDescription className="px-6">
              Use a searchable multi-select list. The first selected model remains the primary runtime default.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 px-6 pb-6">
            <SearchableMultiSelectSection
              title="Available models"
              description="Search, then select one or more models."
              items={availableModels}
              selectedItems={selectedModels}
              onToggle={(modelID) => toggleModel(modelID)}
              emptyText="No models match your search."
              searchPlaceholder="Filter model IDs..."
              monospaceItems
              maxListHeightClassName="max-h-[50vh]"
            />

            <div className="rounded-xl border border-border/70 p-3">
              <Text type={TextTypes.Body6} bold>Custom model ID</Text>
              <div className="mt-2 flex gap-2">
                <Input value={customModel} onChange={(event) => setCustomModel(event.target.value)} placeholder="my-custom-model" />
                <Button variant="outline" onClick={addCustomModel}>Add</Button>
              </div>
            </div>

            <div className="flex flex-wrap gap-2 rounded-xl border border-border/70 bg-muted/10 p-3">
              {selectedModels.map((modelID, idx) => (
                <Badge key={modelID} variant={idx === 0 ? "secondary" : "outline"}>
                  {idx === 0 ? `Primary: ${modelID}` : modelID}
                </Badge>
              ))}
            </div>
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={isToolsDialogOpen} onOpenChange={setIsToolsDialogOpen}>
        <DialogContent className="max-h-[92vh] max-w-5xl overflow-y-auto rounded-2xl border-border/70 p-0">
          <DialogHeader>
            <DialogTitle className="px-6 pt-6">Select Tools</DialogTitle>
            <DialogDescription className="px-6">
              Core tools and MCP tools are listed in separate scrollable sections with independent search.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 px-6 pb-6">
            {errorText ? <Text type={TextTypes.Body6} color="destructive">{errorText}</Text> : null}

            <div className="rounded-xl border border-border/70 bg-muted/20 p-3">
              <Text type={TextTypes.Body6} bold>MCP Integrations</Text>
              <div className="mt-2 space-y-2">
                {availableIntegrations.length === 0 ? (
                  <Text type={TextTypes.Body6} color="muted">No enabled MCP servers found.</Text>
                ) : (
                  availableIntegrations.map((integration) => {
                    const selected = selectedIntegrations.includes(integration.name);
                    const discovering = Boolean(discoveringByIntegration[integration.name]);
                    const loadedCount = (discoveredMCPTools[integration.name] ?? []).length;
                    const discoverStatus = (discoverStatusByIntegration[integration.name] || "").trim();
                    return (
                      <div
                        className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border/60 bg-background px-3 py-2"
                        key={integration.name}
                      >
                        <div className="min-w-0 flex-1">
                          <label className="flex items-center gap-2 text-sm">
                            <input
                              type="checkbox"
                              checked={selected}
                              onChange={() => toggleIntegration(integration.name)}
                            />
                            <span className="font-mono">{integration.name}</span>
                            <span className="text-muted-foreground">{integration.auth_connected ? "connected" : "not connected"}</span>
                          </label>
                          {discoverStatus ? (
                            <Text
                              type={TextTypes.Body7}
                              color={discoverStatus.toLowerCase().startsWith("loaded") ? "muted" : "destructive"}
                            >
                              {discoverStatus}
                            </Text>
                          ) : null}
                        </div>
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => discoverIntegrationTools(integration.name)}
                          disabled={!selected || discovering}
                        >
                          {discovering ? "Loading..." : loadedCount > 0 ? `Reload tools (${loadedCount})` : "Load tools"}
                        </Button>
                      </div>
                    );
                  })
                )}
              </div>
            </div>

            <div className="space-y-3">
              <SearchableMultiSelectSection
                title="Core tools"
                description="Built-in tools available without MCP discovery."
                items={availableCoreTools}
                selectedItems={selectedCoreTools}
                onToggle={(toolName, checked) => {
                  if (checked) {
                    setSelectedCoreTools((current) => uniq([...current, toolName]));
                    return;
                  }
                  setSelectedCoreTools((current) => current.filter((name) => name !== toolName));
                }}
                emptyText="No core tools match your search."
                searchPlaceholder="Filter core tools..."
                collapsedByDefault={false}
                monospaceItems
              />

              <SearchableMultiSelectSection
                title="MCP tools"
                description="Discovered tools from enabled MCP integrations."
                items={availableMCPTools}
                selectedItems={selectedMCPTools}
                onToggle={(toolName, checked) => {
                  if (checked) {
                    setSelectedMCPTools((current) => uniq([...current, toolName]));
                    return;
                  }
                  setSelectedMCPTools((current) => current.filter((name) => name !== toolName));
                }}
                emptyText="Load tools from selected MCP integrations or adjust your search."
                searchPlaceholder="Filter MCP tools..."
                collapsedByDefault={false}
                monospaceItems
              />
            </div>

            <div className="space-y-1 rounded-xl border border-border/70 p-3">
              <Text type={TextTypes.Body6} bold>Additional tool names</Text>
              <Text type={TextTypes.Body7} color="muted">Comma-separated fallback tool names.</Text>
              <Input
                value={manualToolsText}
                onChange={(event) => setManualToolsText(event.target.value)}
                placeholder="search_web, get_weather"
              />
            </div>

            <div className="flex flex-wrap gap-2 rounded-xl border border-border/70 bg-muted/10 p-3">
              {allSelectedTools.map((toolName) => (
                <Badge key={toolName} variant="outline">{toolName}</Badge>
              ))}
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </ChatBase>
  );
}
