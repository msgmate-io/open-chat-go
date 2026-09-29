import { useEffect, useMemo, useState } from "react";
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

function anthropicPrefillWarning(config: Record<string, unknown> | null): string {
  if (!config) return "";
  const backend = String(config.backend ?? "").toLowerCase();
  const model = String(config.model ?? "").toLowerCase();
  if (backend !== "anthropic") return "";

  const isSonnet46 = model.includes("sonnet") && (model.includes("4-6") || model.includes("4.6"));
  if (!isSonnet46) return "";

  return "This Anthropic model rejects assistant-prefill style turns and can fail before tools run. Prefer Claude Haiku 4.5 or another model compatible with prefills.";
}

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

  return (
    <PlatformLayout
      descriptor="Edit Bot Config"
      basePath="/profile"
      baseTitle="Profile"
      parent={{ title: "Bots", href: "/profile/bots" }}
    >
      <div className="space-y-4">
        <div>
          <Text type={TextTypes.Heading5} tag="h1" bold>
            Edit Bot Config
          </Text>
          <Text type={TextTypes.Body5} color="muted">
            Edit and save the runtime JSON config for this bot.
          </Text>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>{bot?.name || username}</CardTitle>
            <CardDescription className="flex flex-wrap items-center gap-2">
              <span className="font-mono">@{bot?.bot_username || username}</span>
              <Badge variant={bot?.is_active ? "secondary" : "outline"}>{bot?.is_active ? "Active" : "Inactive"}</Badge>
              <Badge variant={bot?.is_public ? "secondary" : "outline"}>{bot?.is_public ? "Public" : "Private"}</Badge>
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            {isLoading ? (
              <div className="h-72 animate-pulse rounded bg-muted" />
            ) : error ? (
              <Text type={TextTypes.Body6} color="destructive">
                Failed to load bot.
              </Text>
            ) : (
              <>
                <textarea
                  className="h-[28rem] w-full rounded-md border border-border bg-background p-3 font-mono text-sm"
                  value={configText}
                  onChange={(event) => {
                    setConfigText(event.target.value);
                    setSaveError("");
                    setSaveSuccess("");
                  }}
                />
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
                <div className="flex items-center gap-2">
                  <Button variant="outline" onClick={() => {
                    setConfigText(initialConfigText);
                    setSaveError("");
                    setSaveSuccess("");
                  }}>
                    Reset
                  </Button>
                  <Button onClick={handleSave} disabled={isSaving || !isDirty}>
                    {isSaving ? "Saving..." : "Save"}
                  </Button>
                </div>
              </>
            )}
          </CardContent>
        </Card>
      </div>
    </PlatformLayout>
  );
}
