import React from "react";
import { Badge, Button, Card, CardContent, CardHeader } from "@open-chat-go/ui";
import { Boxes, Cpu, Pencil, Server, SlidersHorizontal, Wrench } from "lucide-react";
import { cn } from "@/lib/utils";

export type StartBotRecord = {
    uuid: string;
    owner_user_uuid?: string;
    integration_name?: string;
    description?: string;
    is_public?: boolean;
    default_shared_config?: Record<string, unknown>;
};

function asString(value: unknown): string {
    return typeof value === "string" ? value.trim() : "";
}

function asStringArray(value: unknown): string[] {
    if (!Array.isArray(value)) {
        return [];
    }
    return value
        .map((entry) => (typeof entry === "string" ? entry.trim() : ""))
        .filter(Boolean);
}

function toTitleCase(value: string): string {
    return value
        .replace(/[_-]+/g, " ")
        .replace(/\s+/g, " ")
        .trim()
        .replace(/\b\w/g, (char) => char.toUpperCase());
}

function MetaItem({
    icon,
    label,
    value,
}: {
    icon: React.ReactNode;
    label: string;
    value: string;
}) {
    return (
        <div className="flex min-w-0 items-center gap-2 rounded-lg border border-border/60 bg-muted/20 px-2.5 py-2">
            <span className="text-muted-foreground shrink-0">{icon}</span>
            <div className="min-w-0">
                <div className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">{label}</div>
                <div className="truncate text-xs font-medium text-foreground" title={value}>
                    {value}
                </div>
            </div>
        </div>
    );
}

/**
 * Bot profile summary shown on the "start a chat with a bot" screen. It
 * replaces the previous generic example prompts with the bot's own details and
 * gives direct access to the bot editor and its providing integration.
 */
export function BotStartDetails({
    name,
    contactDescription,
    modelDescription,
    botRecord,
    selectedModel,
    selectedModelConfig,
    isOwned,
    navigateTo,
}: {
    name?: string;
    contactDescription?: string;
    modelDescription?: string;
    botRecord?: StartBotRecord;
    selectedModel: string;
    selectedModelConfig: Record<string, unknown>;
    isOwned: boolean;
    navigateTo: (to: string) => void;
}) {
    const config = selectedModelConfig ?? {};
    const description =
        asString(modelDescription) ||
        asString(botRecord?.description) ||
        asString(contactDescription) ||
        "An automated assistant ready to help.";

    const model = asString(config.model) || selectedModel;
    const backend = asString(config.backend);
    const chatBackend = asString(config.chat_backend);
    const tools = asStringArray(config.tools);
    const integrations = asStringArray(config.integrations);
    const systemPrompt = asString(config.system_prompt);

    const integrationName = asString(botRecord?.integration_name);
    const canEdit = isOwned && Boolean(botRecord?.uuid);
    const canOpenIntegration = Boolean(integrationName);

    const initials = (name || "?").slice(0, 2).toUpperCase();

    return (
        <Card className="w-full border-border/70 bg-card/90 shadow-sm">
            <CardHeader className="gap-3 pb-3">
                <div className="flex items-start gap-3">
                    <div
                        className={cn(
                            "flex size-12 shrink-0 items-center justify-center rounded-full border text-base font-semibold",
                            "border-primary/30 bg-primary/10 text-primary",
                        )}
                    >
                        {initials}
                    </div>
                    <div className="min-w-0 flex-1">
                        <div className="flex flex-wrap items-center gap-1.5">
                            <span className="truncate text-base font-semibold text-foreground">
                                {name || "Bot"}
                            </span>
                            {isOwned ? <Badge variant="secondary">Owned</Badge> : null}
                            {botRecord?.is_public ? <Badge variant="outline">Public</Badge> : null}
                            {integrationName ? (
                                <Badge variant="outline">{toTitleCase(integrationName)}</Badge>
                            ) : null}
                        </div>
                        <p className="mt-1 line-clamp-3 text-sm leading-snug text-muted-foreground">
                            {description}
                        </p>
                    </div>
                </div>
            </CardHeader>

            <CardContent className="space-y-3 pt-0">
                <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
                    {model ? (
                        <MetaItem icon={<Cpu className="size-3.5" />} label="Model" value={model} />
                    ) : null}
                    {backend ? (
                        <MetaItem
                            icon={<Server className="size-3.5" />}
                            label="Backend"
                            value={chatBackend ? `${backend} · ${chatBackend}` : backend}
                        />
                    ) : null}
                    {tools.length > 0 ? (
                        <MetaItem
                            icon={<Wrench className="size-3.5" />}
                            label="Tools"
                            value={`${tools.length} configured`}
                        />
                    ) : null}
                    {integrations.length > 0 ? (
                        <MetaItem
                            icon={<Boxes className="size-3.5" />}
                            label="Integrations"
                            value={integrations.map(toTitleCase).join(", ")}
                        />
                    ) : null}
                </div>

                {systemPrompt || tools.length > 0 ? (
                    <details className="group rounded-lg border border-border/60 bg-muted/10">
                        <summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-2 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground">
                            <SlidersHorizontal className="size-3.5" />
                            Configuration details
                            <span className="ml-auto text-[10px] group-open:hidden">Show</span>
                            <span className="ml-auto hidden text-[10px] group-open:inline">Hide</span>
                        </summary>
                        <div className="space-y-3 border-t border-border/60 px-3 py-3">
                            {systemPrompt ? (
                                <div className="space-y-1">
                                    <div className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                                        System prompt
                                    </div>
                                    <pre className="max-h-40 overflow-y-auto whitespace-pre-wrap break-words rounded-md bg-background/60 p-2 text-xs leading-snug text-foreground">
                                        {systemPrompt}
                                    </pre>
                                </div>
                            ) : null}
                            {tools.length > 0 ? (
                                <div className="space-y-1">
                                    <div className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                                        Enabled tools
                                    </div>
                                    <div className="flex flex-wrap gap-1.5">
                                        {tools.map((tool) => (
                                            <Badge key={tool} variant="outline" className="font-mono text-[10px]">
                                                {tool}
                                            </Badge>
                                        ))}
                                    </div>
                                </div>
                            ) : null}
                        </div>
                    </details>
                ) : null}

                {canEdit || canOpenIntegration ? (
                    <div className="flex flex-wrap gap-2 pt-0.5">
                        {canEdit ? (
                            <Button
                                type="button"
                                size="sm"
                                variant="outline"
                                className="gap-1.5"
                                onClick={() => navigateTo(`/chats/bots/${encodeURIComponent(botRecord?.uuid || "")}/edit`)}
                            >
                                <Pencil className="size-3.5" />
                                Edit bot
                            </Button>
                        ) : null}
                        {canOpenIntegration ? (
                            <Button
                                type="button"
                                size="sm"
                                variant="ghost"
                                className="gap-1.5"
                                onClick={() => navigateTo(`/integrations/${encodeURIComponent(integrationName)}/`)}
                            >
                                <Boxes className="size-3.5" />
                                Open {toTitleCase(integrationName)} integration
                            </Button>
                        ) : null}
                    </div>
                ) : null}
            </CardContent>
        </Card>
    );
}
