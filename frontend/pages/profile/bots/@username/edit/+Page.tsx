import { useEffect, useMemo, useState, type KeyboardEvent } from "react";
import useSWR from "swr";
import { usePageContext } from "vike-react/usePageContext";
import PlatformLayout from "@/components/PlatformLayout";
import { fetcher } from "@/lib/utils";
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Text,
  TextTypes,
} from "@open-chat-go/ui";

type BotDetails = {
  uuid: string;
  bot_user_uuid: string;
  bot_username: string;
  name: string;
  description?: string;
  default_shared_config?: Record<string, unknown>;
  is_public: boolean;
  is_active: boolean;
};

type TabId = "overview" | "fields" | "editor";

const TABS: ReadonlyArray<{ id: TabId; label: string }> = [
  { id: "overview", label: "Overview" },
  { id: "fields", label: "Fields" },
  { id: "editor", label: "JSON editor" },
];

/** Numeric config keys that get a dedicated input in the structured view. */
const NUMBER_FIELDS: ReadonlyArray<{ key: string; label: string; hint?: string }> = [
  { key: "temperature", label: "Temperature", hint: "0 - 2" },
  { key: "max_tokens", label: "Max tokens" },
  { key: "context", label: "Context window" },
];

/** String config keys that get a dedicated input in the structured view. */
const TEXT_FIELDS: ReadonlyArray<{ key: string; label: string; hint?: string }> = [
  { key: "model", label: "Model", hint: "eg gpt-5.2-mini" },
  { key: "backend", label: "Backend", hint: "eg openai, anthropic" },
  { key: "endpoint", label: "Endpoint", hint: "optional custom base URL" },
];

function anthropicPrefillWarning(config: Record<string, unknown> | null): string {
  if (!config) return "";
  const backend = String(config.backend ?? "").toLowerCase();
  const model = String(config.model ?? "").toLowerCase();
  if (backend !== "anthropic") return "";

  const isSonnet46 = model.includes("sonnet") && (model.includes("4-6") || model.includes("4.6"));
  if (!isSonnet46) return "";

  return "This Anthropic model rejects assistant-prefill style turns and can fail before tools run. Prefer Claude Haiku 4.5 or another model compatible with prefills.";
}

const inputClass =
  "h-9 w-full rounded-md border border-border bg-background px-2.5 text-sm outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring/50";

/**
 * Structured bot configuration surface (ci#749): the single JSON textarea is
 * split into an overview, a handful of typed fields and the raw editor, with a
 * sticky save bar so the action stays reachable on narrow/mobile viewports.
 */
export default function BotConfigEditPage() {
  const pageContext = usePageContext();
  const username = pageContext.routeParams.username as string;
  const identifier = encodeURIComponent(username || "");

  const { data: bot, isLoading, error, mutate } = useSWR<BotDetails>(
    identifier ? `/api/v1/bots/${identifier}` : null,
    fetcher,
  );

  const initialConfigText = useMemo(
    () => JSON.stringify(bot?.default_shared_config ?? {}, null, 2),
    [bot],
  );
  const [configText, setConfigText] = useState("{}");
  const [saveError, setSaveError] = useState("");
  const [saveSuccess, setSaveSuccess] = useState("");
  const [isSaving, setIsSaving] = useState(false);
  const [activeTab, setActiveTab] = useState<TabId>("overview");

  const configWarning = useMemo(() => {
    try {
      const parsed = JSON.parse(configText);
      if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") return "";
      return anthropicPrefillWarning(parsed as Record<string, unknown>);
    } catch {
      return anthropicPrefillWarning((bot?.default_shared_config as Record<string, unknown>) ?? null);
    }
  }, [bot?.default_shared_config, configText]);

  useEffect(() => {
    setConfigText(initialConfigText);
  }, [initialConfigText]);

  const isDirty = configText !== initialConfigText;

  /** Parsed config when the editor holds valid JSON, otherwise null. */
  const parsedConfig = useMemo<Record<string, unknown> | null>(() => {
    try {
      const parsed = JSON.parse(configText);
      if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") return null;
      return parsed as Record<string, unknown>;
    } catch {
      return null;
    }
  }, [configText]);

  const configuredTools = useMemo(() => {
    const tools = (parsedConfig ?? bot?.default_shared_config ?? {})?.tools;
    return Array.isArray(tools) ? (tools as unknown[]).map((tool) => String(tool)) : [];
  }, [bot?.default_shared_config, parsedConfig]);

  /** Patch one structured field, keeping the raw editor in sync. */
  const patchField = (key: string, raw: string) => {
    if (!parsedConfig) return;
    const next: Record<string, unknown> = { ...parsedConfig };
    const trimmed = raw.trim();
    if (trimmed === "") {
      delete next[key];
    } else if (NUMBER_FIELDS.some((field) => field.key === key)) {
      const numeric = Number(trimmed);
      if (Number.isNaN(numeric)) return;
      next[key] = numeric;
    } else {
      next[key] = raw;
    }
    setConfigText(JSON.stringify(next, null, 2));
    setSaveError("");
    setSaveSuccess("");
  };

  const handleSave = async () => {
    setSaveError("");
    setSaveSuccess("");
    setIsSaving(true);

    let parsed: Record<string, unknown>;
    try {
      parsed = JSON.parse(configText);
    } catch {
      setSaveError("Config must be valid JSON.");
      setIsSaving(false);
      return;
    }

    if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") {
      setSaveError("Config must be a JSON object.");
      setIsSaving(false);
      return;
    }

    try {
      const response = await fetch(`/api/v1/bots/${identifier}/config`, {
        method: "PUT",
        credentials: "include",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify(parsed),
      });

      if (!response.ok) {
        const message = await response.text();
        throw new Error(message || `Failed to save config (${response.status})`);
      }

      await mutate();
      setSaveSuccess("Bot config saved.");
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : "Failed to save bot config.");
    } finally {
      setIsSaving(false);
    }
  };

  const reset = () => {
    setConfigText(initialConfigText);
    setSaveError("");
    setSaveSuccess("");
  };

  /** Roving-tabindex keyboard support so the sections work on web and native shells. */
  const onTablistKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    const tabs = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]'));
    if (tabs.length === 0) return;
    const currentIndex = tabs.indexOf(document.activeElement as HTMLButtonElement);
    const start = currentIndex < 0 ? 0 : currentIndex;
    let nextIndex = start;
    if (event.key === "ArrowLeft") nextIndex = (start - 1 + tabs.length) % tabs.length;
    else if (event.key === "ArrowRight") nextIndex = (start + 1) % tabs.length;
    else if (event.key === "End") nextIndex = tabs.length - 1;
    else nextIndex = 0;
    const next = tabs[nextIndex];
    if (!next) return;
    event.preventDefault();
    next.focus();
    setActiveTab(TABS[nextIndex]?.id ?? "overview");
  };

  const activeConfig =
    parsedConfig ?? (bot?.default_shared_config as Record<string, unknown>) ?? {};

  return (
    <PlatformLayout
      descriptor="Edit Bot Config"
      basePath="/profile"
      baseTitle="Profile"
      parent={{ title: "Bots", href: "/profile/bots" }}
    >
      <div className="space-y-4 pb-24">
        <div>
          <Text type={TextTypes.Heading5} tag="h1" bold>
            Edit Bot Config
          </Text>
          <Text type={TextTypes.Body5} color="muted">
            Inspect the bot runtime, tweak common fields, or edit the raw JSON config.
          </Text>
        </div>

        <Card>
          <CardHeader>
            <CardTitle className="break-words">{bot?.name || username}</CardTitle>
            <CardDescription className="flex flex-wrap items-center gap-2">
              <span className="font-mono break-all">@{bot?.bot_username || username}</span>
              <Badge variant={bot?.is_active ? "secondary" : "outline"}>
                {bot?.is_active ? "Active" : "Inactive"}
              </Badge>
              <Badge variant={bot?.is_public ? "secondary" : "outline"}>
                {bot?.is_public ? "Public" : "Private"}
              </Badge>
              {isDirty ? <Badge variant="outline">Unsaved changes</Badge> : null}
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <div
              role="tablist"
              aria-label="Bot config sections"
              onKeyDown={onTablistKeyDown}
              className="surface-sunken scrollbar-hidden -mx-1 flex gap-1 overflow-x-auto p-1"
            >
              {TABS.map((tab) => {
                const isActive = tab.id === activeTab;
                return (
                  <button
                    key={tab.id}
                    type="button"
                    role="tab"
                    id={`bot-config-tab-${tab.id}`}
                    aria-selected={isActive}
                    aria-controls={`bot-config-panel-${tab.id}`}
                    tabIndex={isActive ? 0 : -1}
                    onClick={() => setActiveTab(tab.id)}
                    className={
                      "shrink-0 rounded-md px-3 py-1.5 text-xs font-medium transition-[color,box-shadow,background-color] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 " +
                      (isActive
                        ? "bg-background text-foreground shadow-sm"
                        : "text-muted-foreground hover:bg-background/60 hover:text-foreground")
                    }
                  >
                    {tab.label}
                  </button>
                );
              })}
            </div>

            {isLoading ? (
              <div className="h-72 animate-pulse rounded bg-muted" />
            ) : error ? (
              <Text type={TextTypes.Body6} color="destructive">
                Failed to load bot.
              </Text>
            ) : (
              <div
                role="tabpanel"
                id={`bot-config-panel-${activeTab}`}
                aria-labelledby={`bot-config-tab-${activeTab}`}
                tabIndex={0}
                className="focus-visible:outline-none"
              >
                {activeTab === "overview" ? (
                  <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                    <div className="rounded-xl border border-border/70 bg-card/90 p-3">
                      <Text type={TextTypes.Body6} bold>
                        Runtime
                      </Text>
                      <dl className="mt-2 space-y-1 text-sm">
                        {[
                          ["Model", String(activeConfig.model ?? "-")],
                          ["Backend", String(activeConfig.backend ?? "-")],
                          ["Endpoint", String(activeConfig.endpoint ?? "-")],
                          ["Temperature", String(activeConfig.temperature ?? "-")],
                          ["Max tokens", String(activeConfig.max_tokens ?? "-")],
                        ].map(([key, value]) => (
                          <div key={key} className="flex items-start justify-between gap-3">
                            <dt className="text-muted-foreground">{key}</dt>
                            <dd className="min-w-0 break-all text-right">{value}</dd>
                          </div>
                        ))}
                      </dl>
                    </div>

                    <div className="rounded-xl border border-border/70 bg-card/90 p-3">
                      <Text type={TextTypes.Body6} bold>
                        Tools ({configuredTools.length})
                      </Text>
                      {configuredTools.length === 0 ? (
                        <Text type={TextTypes.Body7} color="muted" className="mt-2">
                          No tools configured for this bot.
                        </Text>
                      ) : (
                        <ul className="mt-2 max-h-40 space-y-1 overflow-y-auto pr-1">
                          {configuredTools.map((tool) => (
                            <li
                              key={tool}
                              className="font-mono text-[11px] break-all text-muted-foreground"
                            >
                              {tool}
                            </li>
                          ))}
                        </ul>
                      )}
                    </div>

                    <div className="rounded-xl border border-border/70 bg-card/90 p-3 md:col-span-2">
                      <Text type={TextTypes.Body6} bold>
                        System prompt
                      </Text>
                      {activeConfig.system_prompt ? (
                        <p className="mt-2 max-h-40 overflow-y-auto whitespace-pre-wrap text-sm text-muted-foreground">
                          {String(activeConfig.system_prompt)}
                        </p>
                      ) : (
                        <Text type={TextTypes.Body7} color="muted" className="mt-2">
                          No system prompt configured.
                        </Text>
                      )}
                    </div>
                  </div>
                ) : null}

                {activeTab === "fields" ? (
                  parsedConfig ? (
                    <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                      {[...TEXT_FIELDS, ...NUMBER_FIELDS].map((field) => (
                        <label key={field.key} className="space-y-1">
                          <span className="text-xs font-medium text-foreground">{field.label}</span>
                          <input
                            className={inputClass}
                            value={
                              activeConfig[field.key] === undefined ||
                              activeConfig[field.key] === null
                                ? ""
                                : String(activeConfig[field.key])
                            }
                            onChange={(event) => patchField(field.key, event.target.value)}
                          />
                          {field.hint ? (
                            <span className="block text-[11px] text-muted-foreground">
                              {field.hint}
                            </span>
                          ) : null}
                        </label>
                      ))}
                      <label className="space-y-1 md:col-span-2">
                        <span className="text-xs font-medium text-foreground">System prompt</span>
                        <textarea
                          className="min-h-32 w-full rounded-md border border-border bg-background p-2.5 text-sm outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring/50"
                          value={String(activeConfig.system_prompt ?? "")}
                          onChange={(event) => patchField("system_prompt", event.target.value)}
                        />
                      </label>
                    </div>
                  ) : (
                    <div className="space-y-2">
                      <Text type={TextTypes.Body6} color="destructive">
                        The JSON editor currently holds invalid JSON.
                      </Text>
                      <Button variant="outline" size="sm" onClick={() => setActiveTab("editor")}>
                        Fix JSON first
                      </Button>
                    </div>
                  )
                ) : null}

                {activeTab === "editor" ? (
                  <div className="space-y-3">
                    <textarea
                      className="h-[26rem] w-full rounded-md border border-border bg-background p-3 font-mono text-xs outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring/50"
                      value={configText}
                      onChange={(event) => {
                        setConfigText(event.target.value);
                        setSaveError("");
                        setSaveSuccess("");
                      }}
                    />
                    <Text type={TextTypes.Body7} color="muted">
                      Raw <code className="font-mono">default_shared_config</code> JSON as stored for
                      this bot.
                    </Text>
                  </div>
                ) : null}
              </div>
            )}

            {configWarning ? (
              <Text type={TextTypes.Body6} color="destructive">
                {configWarning}
              </Text>
            ) : null}
            {saveError ? (
              <Text type={TextTypes.Body6} color="destructive">
                {saveError}
              </Text>
            ) : null}
            {saveSuccess ? (
              <Text type={TextTypes.Body6} className="text-green-700">
                {saveSuccess}
              </Text>
            ) : null}
          </CardContent>
        </Card>

        {isLoading || error ? null : (
          <div className="sticky bottom-0 z-10 flex flex-wrap items-center justify-between gap-2 border-t border-border/70 bg-background/95 py-3 backdrop-blur">
            <Text type={TextTypes.Body7} color="muted">
              {isDirty ? "Unsaved changes" : "Config is up to date"}
            </Text>
            <div className="flex items-center gap-2">
              <Button variant="outline" onClick={reset} disabled={!isDirty || isSaving}>
                Reset
              </Button>
              <Button onClick={handleSave} disabled={isSaving || !isDirty}>
                {isSaving ? "Saving..." : "Save"}
              </Button>
            </div>
          </div>
        )}
      </div>
    </PlatformLayout>
  );
}
