import { useMemo, useState } from "react";
import useSWR from "swr";
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
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  Text,
  TextTypes,
} from "@open-chat-go/ui";
import { Bot } from "lucide-react";

type BotRow = {
  uuid: string;
  bot_user_uuid: string;
  bot_username: string;
  bot_contact_token: string;
  name: string;
  description?: string;
  default_shared_config?: {
    model?: string;
    backend?: string;
    endpoint?: string;
    tools?: string[];
    [key: string]: unknown;
  };
  is_public: boolean;
  is_active: boolean;
};

type BotsListResponse = {
  limit: number;
  page: number;
  total_pages: number;
  rows: BotRow[];
};

export default function ProfileBotsPage() {
  const { data: bots, isLoading, error } = useSWR<BotsListResponse>(
    "/api/v1/bots/list?limit=200&page=1",
    fetcher,
  );
  const [selectedBot, setSelectedBot] = useState<BotRow | null>(null);
  const selectedBotConfig = useMemo(
    () => JSON.stringify(selectedBot?.default_shared_config ?? {}, null, 2),
    [selectedBot],
  );

  return (
    <PlatformLayout descriptor="Bots" basePath="/profile" baseTitle="Profile">
      <div className="space-y-4">
        <div>
          <Text type={TextTypes.Heading5} tag="h1" bold>
            Bots
          </Text>
          <Text type={TextTypes.Body5} color="muted">
            Manage your owned bot runtimes and inspect each bot configuration.
          </Text>
        </div>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Bot className="h-5 w-5" />
              Owned Bots
            </CardTitle>
            <CardDescription>
              {bots?.rows?.length ?? 0} bot(s) found
            </CardDescription>
          </CardHeader>
          <CardContent>
            {isLoading ? (
              <div className="space-y-2">
                <div className="h-16 animate-pulse rounded bg-muted" />
                <div className="h-16 animate-pulse rounded bg-muted" />
              </div>
            ) : error ? (
              <Text type={TextTypes.Body6} color="muted">
                Failed to load bots.
              </Text>
            ) : (bots?.rows?.length ?? 0) === 0 ? (
              <Text type={TextTypes.Body6} color="muted">
                No bots found for your account yet.
              </Text>
            ) : (
              <div className="space-y-3">
                {(bots?.rows ?? []).map((bot) => (
                  <div key={bot.uuid} className="rounded-lg border border-border/70 bg-card/80 p-3">
                    <div className="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
                      <div className="space-y-2">
                        <div className="flex flex-wrap items-center gap-2">
                          <Text type={TextTypes.Body5} bold>
                            {bot.name}
                          </Text>
                          <Badge variant={bot.is_active ? "secondary" : "outline"}>
                            {bot.is_active ? "Active" : "Inactive"}
                          </Badge>
                          <Badge variant={bot.is_public ? "secondary" : "outline"}>
                            {bot.is_public ? "Public" : "Private"}
                          </Badge>
                        </div>
                        <Text type={TextTypes.Body6} color="muted">
                          {bot.description?.trim() || "No description provided."}
                        </Text>
                        <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
                          <Text type={TextTypes.Body7} color="muted">
                            Model: {String(bot.default_shared_config?.model || "-")}
                          </Text>
                          <Text type={TextTypes.Body7} color="muted">
                            Backend: {String(bot.default_shared_config?.backend || "-")}
                          </Text>
                          <Text type={TextTypes.Body7} color="muted">
                            Tools: {(bot.default_shared_config?.tools ?? []).length}
                          </Text>
                        </div>
                        <Text type={TextTypes.Body7} color="muted" className="font-mono break-all">
                          Contact token: {bot.bot_contact_token}
                        </Text>
                      </div>
                      <div className="flex shrink-0 items-center gap-2">
                        <Button
                          variant="default"
                          size="sm"
                          onClick={() => {
                            const username = encodeURIComponent(bot.bot_username || bot.name);
                            window.location.href = `/profile/bots/${username}/edit`;
                          }}
                        >
                          Edit
                        </Button>
                        <Button variant="outline" size="sm" onClick={() => setSelectedBot(bot)}>
                          View Bot Config
                        </Button>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </CardContent>
        </Card>

        <Dialog open={Boolean(selectedBot)} onOpenChange={(open) => {
          if (!open) setSelectedBot(null);
        }}>
          <DialogContent className="max-h-[88vh] max-w-4xl overflow-y-auto">
            <DialogHeader>
              <DialogTitle className="break-all">{selectedBot?.name || "Bot Config"}</DialogTitle>
              <DialogDescription className="break-words">
                Bot user UUID: {selectedBot?.bot_user_uuid || "-"}
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-3">
              <div className="rounded-md border border-border/70 bg-muted/20 p-3">
                <Text type={TextTypes.Body6} bold>
                  Runtime Config
                </Text>
                <pre className="mt-2 max-h-[52vh] overflow-auto rounded bg-background p-3 text-xs leading-5">
                  {selectedBotConfig}
                </pre>
              </div>
            </div>
          </DialogContent>
        </Dialog>
      </div>
    </PlatformLayout>
  );
}
