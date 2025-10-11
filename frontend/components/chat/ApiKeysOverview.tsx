import { useMemo, useState } from "react";
import useSWR, { mutate } from "swr";
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
  Input,
  LoadingSpinner,
  Text,
  TextTypes,
} from "@open-chat-go/ui";
import { fetcher } from "@/lib/utils";

type PermissionsResponse = {
  rows: string[];
};

type SelfUser = {
  uuid?: string;
};

type BotRow = {
  uuid: string;
  owner_user_uuid?: string;
  name: string;
  bot_contact_token: string;
};

type BotsResponse = {
  rows: BotRow[];
};

type AccessTokenListItem = {
  uuid: string;
  name: string;
  display_name?: string;
  scope?: "account" | "bot";
  bot_uuid?: string;
  token_prefix: string;
  audience?: string;
  scopes?: string;
  created_at: string;
  last_used_at?: string;
  expires_at?: string;
  revoked_at?: string;
};

type TokensResponse = {
  page?: number;
  limit?: number;
  total_pages?: number;
  rows: AccessTokenListItem[];
};

type DraftEditor = {
  name: string;
  expiresAtLocal: string;
};

type Section = {
  key: string;
  title: string;
  description: string;
  botUUID?: string;
  rows: AccessTokenListItem[];
};

const ACCOUNT_SECTION_KEY = "account";
const BROWSER_SECTION_KEY = "browser-sessions";
const BROWSER_AUDIENCE = "browser-api";

function isBrowserToken(row: AccessTokenListItem): boolean {
  return row.audience === BROWSER_AUDIENCE;
}

function isTokenActive(row: AccessTokenListItem): boolean {
  return !row.revoked_at && (!row.expires_at || new Date(row.expires_at) > new Date());
}

function browserTokenLabel(row: AccessTokenListItem): string {
  const raw = row.display_name || row.name || "";
  const parts = raw.split("|");
  const label = parts.length > 1 ? parts.slice(1).join("|").trim() : "";
  return label || "Browser session";
}

function toLocalDateTimeInputValue(date: Date): string {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  const hour = String(date.getHours()).padStart(2, "0");
  const minute = String(date.getMinutes()).padStart(2, "0");
  return `${year}-${month}-${day}T${hour}:${minute}`;
}

function defaultExpiryLocalValue(): string {
  const date = new Date();
  date.setDate(date.getDate() + 30);
  return toLocalDateTimeInputValue(date);
}

function localInputToRFC3339(value: string): string {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) {
    return "";
  }
  return parsed.toISOString();
}

function formatDate(value?: string): string {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  return date.toLocaleString();
}

export function ApiKeysOverview() {
  const [draftEditors, setDraftEditors] = useState<Record<string, DraftEditor>>({});
  const [creatingSectionKey, setCreatingSectionKey] = useState("");
  const [createError, setCreateError] = useState("");
  const [newToken, setNewToken] = useState<string | null>(null);
  const [newTokenSectionKey, setNewTokenSectionKey] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [revokingUUID, setRevokingUUID] = useState("");
  const [sectionOpen, setSectionOpen] = useState<Record<string, boolean>>({});

  const { data: permissions } = useSWR<PermissionsResponse>("/api/v1/user/permissions", fetcher);
  const canManageTokens = Boolean(permissions?.rows?.includes("create_api_tokens"));
  const { data: selfUser } = useSWR<SelfUser>("/api/v1/user/self", fetcher);
  const { data: bots, isLoading: botsLoading } = useSWR<BotsResponse>(
    canManageTokens ? "/api/v1/bots/list?include_public=true&limit=200&page=1" : null,
    fetcher,
  );

  const ownedBots = useMemo(() => {
    return (bots?.rows ?? [])
      .filter((bot) => Boolean(bot.owner_user_uuid) && bot.owner_user_uuid === selfUser?.uuid)
      .sort((a, b) => a.name.localeCompare(b.name));
  }, [bots?.rows, selfUser?.uuid]);

  const listURL = canManageTokens ? "/api/v1/user/access-tokens/list?page=1&limit=200" : null;

  const { data: tokens, isLoading: tokensLoading } = useSWR<TokensResponse>(listURL, fetcher);

  const botNameByUUID = useMemo(() => {
    const map = new Map<string, string>();
    for (const bot of ownedBots) {
      map.set(bot.uuid, bot.name);
    }
    return map;
  }, [ownedBots]);

  const browserRows = useMemo(() => {
    return (tokens?.rows ?? []).filter(isBrowserToken);
  }, [tokens?.rows]);

  const groupedSections = useMemo<Section[]>(() => {
    const rows = (tokens?.rows ?? []).filter((row) => !isBrowserToken(row));
    const accountRows = rows.filter((row) => (row.scope ?? "account") !== "bot");
    const byBot = new Map<string, AccessTokenListItem[]>();

    for (const row of rows) {
      if ((row.scope ?? "account") !== "bot" || !row.bot_uuid) {
        continue;
      }
      const bucket = byBot.get(row.bot_uuid) ?? [];
      bucket.push(row);
      byBot.set(row.bot_uuid, bucket);
    }

    const sections: Section[] = [
      {
        key: ACCOUNT_SECTION_KEY,
        title: "Your Account",
        description: "Keys that authorize this user account.",
        rows: accountRows,
      },
    ];

    for (const bot of ownedBots) {
      sections.push({
        key: bot.uuid,
        title: bot.name,
        description: "Keys tagged for this bot.",
        botUUID: bot.uuid,
        rows: byBot.get(bot.uuid) ?? [],
      });
      byBot.delete(bot.uuid);
    }

    for (const [botUUID, botRows] of byBot.entries()) {
      sections.push({
        key: botUUID,
        title: botNameByUUID.get(botUUID) ?? "Unknown Bot",
        description: "Keys tagged for this bot.",
        botUUID,
        rows: botRows,
      });
    }

    return sections;
  }, [botNameByUUID, ownedBots, tokens?.rows]);

  const activeCount = useMemo(() => {
    return (tokens?.rows ?? []).filter((row) => !isBrowserToken(row) && isTokenActive(row)).length;
  }, [tokens?.rows]);

  const activeBrowserCount = useMemo(() => {
    return browserRows.filter(isTokenActive).length;
  }, [browserRows]);

  const isSectionOpen = (sectionKey: string, defaultOpen: boolean) => sectionOpen[sectionKey] ?? defaultOpen;

  const toggleSection = (sectionKey: string, defaultOpen: boolean) => {
    setSectionOpen((prev) => ({ ...prev, [sectionKey]: !(prev[sectionKey] ?? defaultOpen) }));
  };

  const openEditor = (sectionKey: string) => {
    setCreateError("");
    setNewToken(null);
    setNewTokenSectionKey(null);
    setDraftEditors((prev) => {
      if (prev[sectionKey]) {
        return prev;
      }
      return {
        ...prev,
        [sectionKey]: {
          name: "",
          expiresAtLocal: defaultExpiryLocalValue(),
        },
      };
    });
  };

  const closeEditor = (sectionKey: string) => {
    setDraftEditors((prev) => {
      if (!prev[sectionKey]) {
        return prev;
      }
      const { [sectionKey]: _, ...rest } = prev;
      return rest;
    });
  };

  const patchEditor = (sectionKey: string, patch: Partial<DraftEditor>) => {
    setDraftEditors((prev) => {
      const current = prev[sectionKey];
      if (!current) {
        return prev;
      }
      return {
        ...prev,
        [sectionKey]: {
          ...current,
          ...patch,
        },
      };
    });
  };

  const handleCreateToken = async (section: Section) => {
    const editor = draftEditors[section.key];
    if (!editor || !editor.name.trim()) {
      return;
    }

    setCreatingSectionKey(section.key);
    setCreateError("");
    setNewToken(null);
    setNewTokenSectionKey(null);

    try {
      const payload: Record<string, string> = { name: editor.name.trim() };
      const expiresAtRFC3339 = localInputToRFC3339(editor.expiresAtLocal);
      if (expiresAtRFC3339) {
        payload.expires_at = expiresAtRFC3339;
      }
      if (section.botUUID) {
        payload.bot_uuid = section.botUUID;
      }

      const response = await fetch("/api/v1/user/access-tokens", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      if (!response.ok) {
        setCreateError(await response.text());
        return;
      }
      const created = await response.json();
      setNewToken(created?.token || null);
      setNewTokenSectionKey(created?.token ? section.key : null);
      await mutate(listURL || "");
      closeEditor(section.key);
    } catch (error) {
      setCreateError(error instanceof Error ? error.message : "Failed to create API key");
    } finally {
      setCreatingSectionKey("");
    }
  };

  const handleRevoke = async (tokenUUID: string) => {
    if (!tokenUUID) return;
    setRevokingUUID(tokenUUID);
    try {
      const response = await fetch(`/api/v1/user/access-tokens/${encodeURIComponent(tokenUUID)}/revoke`, {
        method: "POST",
      });
      if (!response.ok) {
        setCreateError(await response.text());
        return;
      }
      await mutate(listURL || "");
    } catch (error) {
      setCreateError(error instanceof Error ? error.message : "Failed to revoke API key");
    } finally {
      setRevokingUUID("");
    }
  };

  const handleCopyToken = async (token: string) => {
    try {
      if (!navigator.clipboard?.writeText) {
        throw new Error("Clipboard unavailable");
      }
      await navigator.clipboard.writeText(token);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  };

  const renderNewTokenBanner = (section: Section) => {
    if (!newToken || newTokenSectionKey !== section.key) {
      return null;
    }
    return (
      <div className="rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-black">
        <Text type={TextTypes.Body6} bold>
          Copy this key for {section.title} now (shown once)
        </Text>
        <code className="mt-1 block break-all text-xs">{newToken}</code>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="mt-2"
          onClick={() => handleCopyToken(newToken)}
        >
          {copied ? "Copied" : "Copy key"}
        </Button>
      </div>
    );
  };

  const renderTokenRow = (row: AccessTokenListItem) => {
    const browser = isBrowserToken(row);
    const scopes = (row.scopes ?? "")
      .split(",")
      .map((scope) => scope.trim())
      .filter(Boolean);
    return (
      <div key={row.uuid} className="rounded-lg border border-border/70 bg-card/80 p-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <Text type={TextTypes.Body6} bold>
              {browser ? browserTokenLabel(row) : row.display_name || row.name}
            </Text>
            <Text type={TextTypes.Body7} color="muted">
              Prefix: {row.token_prefix}
            </Text>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {browser ? (
              <Badge variant="outline">Browser</Badge>
            ) : (
              <Badge variant={(row.scope || "account") === "bot" ? "outline" : "secondary"}>
                {(row.scope || "account") === "bot" ? "Bot" : "Account"}
              </Badge>
            )}
            {browser && scopes.map((scope) => <Badge key={scope} variant="outline">{scope}</Badge>)}
            <Badge variant={row.revoked_at ? "outline" : "secondary"}>
              {row.revoked_at ? "Revoked" : "Active"}
            </Badge>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={Boolean(row.revoked_at) || revokingUUID === row.uuid}
              onClick={() => handleRevoke(row.uuid)}
            >
              {revokingUUID === row.uuid ? "Revoking..." : "Revoke"}
            </Button>
          </div>
        </div>
        <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1">
          <Text type={TextTypes.Body7} color="muted">
            Created: {formatDate(row.created_at)}
          </Text>
          <Text type={TextTypes.Body7} color="muted">
            Last used: {formatDate(row.last_used_at)}
          </Text>
          <Text type={TextTypes.Body7} color="muted">
            Expires: {formatDate(row.expires_at)}
          </Text>
        </div>
      </div>
    );
  };

  const renderEditorRow = (section: Section) => {
    const editor = draftEditors[section.key];
    if (!editor) {
      return null;
    }

    return (
      <div className="rounded-lg border border-dashed border-primary/40 bg-primary/5 p-3">
        <div className="grid grid-cols-1 gap-2 md:grid-cols-[2fr_1.4fr_auto] md:items-center">
          <Input
            placeholder="API key name"
            value={editor.name}
            onChange={(event) => patchEditor(section.key, { name: event.target.value })}
          />
          <input
            type="datetime-local"
            className="h-10 w-full rounded-md border bg-background px-3 text-sm"
            value={editor.expiresAtLocal}
            onChange={(event) => patchEditor(section.key, { expiresAtLocal: event.target.value })}
          />
          <div className="flex items-center gap-2 md:justify-end">
            <Button
              type="button"
              size="sm"
              onClick={() => handleCreateToken(section)}
              disabled={creatingSectionKey === section.key || !editor.name.trim()}
            >
              {creatingSectionKey === section.key ? "Creating..." : "Create"}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => closeEditor(section.key)}
              disabled={creatingSectionKey === section.key}
            >
              Cancel
            </Button>
          </div>
        </div>
      </div>
    );
  };

  const renderApiTokenSection = (section: Section) => {
    const open = isSectionOpen(section.key, true);
    return (
      <Collapsible
        key={section.key}
        open={open}
        onOpenChange={() => toggleSection(section.key, true)}
        className="rounded-lg border border-border/60 bg-background/40"
      >
        <CollapsibleTrigger asChild>
          <button
            type="button"
            className="flex w-full items-center justify-between gap-3 px-3 py-2 text-left active:bg-muted/40"
          >
            <div className="min-w-0">
              <Text type={TextTypes.Body6} bold>
                {section.title}
              </Text>
              <Text type={TextTypes.Body7} color="muted">
                {section.description}
              </Text>
            </div>
            <div className="flex items-center gap-2">
              <Badge variant="outline">{section.rows.length}</Badge>
              <Text type={TextTypes.Body7} color="muted">
                {open ? "Hide" : "Show"}
              </Text>
            </div>
          </button>
        </CollapsibleTrigger>
        <CollapsibleContent className="border-t border-border/60 px-3 pb-3 pt-2">
          <div className="space-y-2">
            <div className="flex justify-end">
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => openEditor(section.key)}
                disabled={Boolean(draftEditors[section.key]) || creatingSectionKey !== ""}
              >
                +
              </Button>
            </div>
            {renderEditorRow(section)}
            {renderNewTokenBanner(section)}
            {section.rows.length === 0 && !draftEditors[section.key] ? (
              <div className="rounded-md border border-dashed border-border/70 bg-muted/20 px-3 py-2">
                <Text type={TextTypes.Body7} color="muted">
                  No keys yet.
                </Text>
              </div>
            ) : null}
            {section.rows.map(renderTokenRow)}
          </div>
        </CollapsibleContent>
      </Collapsible>
    );
  };

  const renderBrowserSection = () => {
    const open = isSectionOpen(BROWSER_SECTION_KEY, false);
    return (
      <Collapsible
        key={BROWSER_SECTION_KEY}
        open={open}
        onOpenChange={() => toggleSection(BROWSER_SECTION_KEY, false)}
        className="rounded-lg border border-border/60 bg-background/40"
      >
        <CollapsibleTrigger asChild>
          <button
            type="button"
            className="flex w-full items-center justify-between gap-3 px-3 py-2 text-left active:bg-muted/40"
          >
            <div className="min-w-0">
              <Text type={TextTypes.Body6} bold>
                Browser Sessions
              </Text>
              <Text type={TextTypes.Body7} color="muted">
                Short-lived scoped tokens issued to embedded browser clients. They rotate automatically and do not
                count towards your API key limit.
              </Text>
            </div>
            <div className="flex items-center gap-2">
              <Badge variant="outline">{browserRows.length}</Badge>
              <Text type={TextTypes.Body7} color="muted">
                {open ? "Hide" : "Show"}
              </Text>
            </div>
          </button>
        </CollapsibleTrigger>
        <CollapsibleContent className="border-t border-border/60 px-3 pb-3 pt-2">
          <div className="space-y-2">
            {browserRows.length === 0 ? (
              <div className="rounded-md border border-dashed border-border/70 bg-muted/20 px-3 py-2">
                <Text type={TextTypes.Body7} color="muted">
                  No browser session tokens issued.
                </Text>
              </div>
            ) : null}
            {browserRows.map(renderTokenRow)}
          </div>
        </CollapsibleContent>
      </Collapsible>
    );
  };

  return (
    <div className="mx-auto flex h-full w-full max-w-6xl flex-col gap-4 px-4 py-6 md:px-6">
      <Card className="overflow-hidden border-border/70 bg-gradient-to-br from-background via-background to-muted/40">
        <CardHeader className="gap-3">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="min-w-0">
              <CardTitle className="text-2xl">API Keys</CardTitle>
              <CardDescription className="max-w-3xl text-sm md:text-base">
                Create and manage API keys for your account and bots you own.
              </CardDescription>
            </div>
            <div className="flex flex-wrap gap-2">
              <Badge variant="secondary">Account</Badge>
              <Badge variant="outline">Bot</Badge>
              <Badge variant="outline">Bearer</Badge>
              <Badge variant="outline">Active keys: {activeCount}</Badge>
              <Badge variant="outline">Browser sessions: {activeBrowserCount}</Badge>
            </div>
          </div>
        </CardHeader>
      </Card>

      {!canManageTokens ? (
        <Card className="border-destructive/30 bg-destructive/5">
          <CardHeader>
            <CardTitle className="text-base">Permission required</CardTitle>
            <CardDescription>You need the `create_api_tokens` permission to manage API keys.</CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <>
          <Card className="border-border/70 bg-card/70">
            <CardHeader>
              <CardTitle className="text-lg">API Key List</CardTitle>
              <CardDescription>Use header: `Authorization: Bearer &lt;token&gt;`</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              {tokensLoading ? (
                <div className="flex h-20 items-center justify-center">
                  <LoadingSpinner />
                </div>
              ) : groupedSections.length === 0 && browserRows.length === 0 ? (
                <Card className="border-dashed border-border/70 bg-muted/30">
                  <CardHeader>
                    <CardTitle className="text-base">No keys found</CardTitle>
                    <CardDescription>Create a key to start using API access.</CardDescription>
                  </CardHeader>
                </Card>
              ) : (
                <div className="space-y-3">
                  {groupedSections.map(renderApiTokenSection)}
                  {renderBrowserSection()}
                </div>
              )}

              {botsLoading ? (
                <Text type={TextTypes.Body7} color="muted">
                  Loading bot sections...
                </Text>
              ) : null}

              {createError ? (
                <Text type={TextTypes.Body7} color="destructive">
                  {createError}
                </Text>
              ) : null}
            </CardContent>
          </Card>
        </>
      )}
    </div>
  );
}
