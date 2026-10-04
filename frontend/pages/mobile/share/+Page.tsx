import { useEffect, useMemo, useRef, useState } from "react";
import { navigate } from "vike/client/router";
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Input,
  LoadingSpinner,
  Text,
  TextTypes,
  clearPendingMobileSharedItems,
  isMobileAppRuntime,
  readPendingMobileSharedFileBase64,
  readPendingMobileSharedItems,
  type PendingMobileSharedItem,
} from "@open-chat-go/ui";
import { File as FileIcon, Image as ImageIcon, Link as LinkIcon, X } from "lucide-react";
import { setPendingMobileShare, type MobileShareAttachment } from "@/lib/mobile-share-store";

type BotContact = {
  contactToken: string;
  name: string;
  description: string;
};

type ContactsResponse = { rows?: Array<{ contact_token?: string; name?: string; is_automated?: boolean; profile_data?: Record<string, unknown> }> };
type BotsResponse = { rows?: Array<{ bot_contact_token?: string; name?: string; description?: string }> };

const MAX_UPLOAD_BYTES = 5 * 1024 * 1024;

const allowedUploadTypes = new Set([
  "image/jpeg",
  "image/png",
  "image/gif",
  "image/webp",
  "application/pdf",
  "text/plain",
  "application/msword",
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  "application/vnd.ms-excel",
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
]);

function base64ToBytes(encoded: string): ArrayBuffer {
  const binary = atob(encoded);
  const buffer = new ArrayBuffer(binary.length);
  const bytes = new Uint8Array(buffer);
  for (let i = 0; i < binary.length; i += 1) {
    bytes[i] = binary.charCodeAt(i);
  }
  return buffer;
}

function formatSize(bytes: number): string {
  if (!bytes || bytes <= 0) {
    return "";
  }
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function toTitleCase(value: string): string {
  return value
    .replace(/[_-]+/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .replace(/\b\w/g, (char) => char.toUpperCase());
}

export default function Page() {
  const [items, setItems] = useState<PendingMobileSharedItem[]>([]);
  const [attachments, setAttachments] = useState<MobileShareAttachment[]>([]);
  const [note, setNote] = useState("");
  const [botContacts, setBotContacts] = useState<BotContact[]>([]);
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState("Preparing shared content...");
  const [isUploading, setIsUploading] = useState(true);
  const [isLoadingBots, setIsLoadingBots] = useState(true);
  const [isStarting, setIsStarting] = useState(false);
  const objectUrls = useRef<string[]>([]);

  useEffect(() => {
    if (!isMobileAppRuntime()) {
      navigate("/chat/new");
      return;
    }

    const result = readPendingMobileSharedItems();
    if (!result.ok || result.items.length === 0) {
      clearPendingMobileSharedItems();
      navigate("/chat/new");
      return;
    }

    setItems(result.items);
    void prepareItems(result.items);
    void loadBots();

    return () => {
      objectUrls.current.forEach((url) => URL.revokeObjectURL(url));
      objectUrls.current = [];
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const prepareItems = async (sharedItems: PendingMobileSharedItem[]) => {
    const nextAttachments: MobileShareAttachment[] = [];
    const noteParts: string[] = [];
    const errors: string[] = [];

    for (const item of sharedItems) {
      if (item.kind !== "file") {
        const text = (item.text || item.name || "").trim();
        if (text) {
          noteParts.push(text);
        }
        continue;
      }

      const mimeType = item.mimeType || "application/octet-stream";
      if (!allowedUploadTypes.has(mimeType)) {
        errors.push(`${item.name}: unsupported file type`);
        continue;
      }
      if (item.size > MAX_UPLOAD_BYTES) {
        errors.push(`${item.name}: file is larger than 5MB`);
        continue;
      }

      const content = readPendingMobileSharedFileBase64(item.index, MAX_UPLOAD_BYTES);
      if (!content.ok || !content.data) {
        errors.push(`${item.name}: ${content.message || "could not read file"}`);
        continue;
      }

      try {
        const bytes = base64ToBytes(content.data);
        const file = new File([bytes], item.name, { type: mimeType });
        const formData = new FormData();
        formData.append("file", file);

        const response = await fetch("/api/v1/files/upload", {
          method: "POST",
          body: formData,
          credentials: "include",
        });
        if (!response.ok) {
          throw new Error((await response.text()) || `Upload failed (${response.status})`);
        }
        const uploaded = (await response.json()) as {
          file_id: string;
          file_name: string;
          size: number;
          mime_type: string;
        };

        let previewUrl: string | undefined;
        if (item.isImage) {
          previewUrl = URL.createObjectURL(file);
          objectUrls.current.push(previewUrl);
        }

        nextAttachments.push({
          fileId: uploaded.file_id,
          fileName: uploaded.file_name,
          displayName: item.name,
          mimeType: uploaded.mime_type || mimeType,
          size: uploaded.size,
          previewUrl,
        });
      } catch (error) {
        errors.push(`${item.name}: ${error instanceof Error ? error.message : "upload failed"}`);
      }
    }

    setAttachments(nextAttachments);
    setNote(noteParts.join("\n"));
    setIsUploading(false);
    setStatus(errors.length > 0 ? errors.join(" · ") : "");
  };

  const loadBots = async () => {
    setIsLoadingBots(true);
    try {
      const [contactsResponse, botsResponse] = await Promise.all([
        fetch("/api/v1/contacts/list?limit=200&page=1", { credentials: "include" }),
        fetch("/api/v1/bots/list?include_public=true&limit=200&page=1", { credentials: "include" }),
      ]);

      const contacts: ContactsResponse = contactsResponse.ok ? await contactsResponse.json() : { rows: [] };
      const bots: BotsResponse = botsResponse.ok ? await botsResponse.json() : { rows: [] };

      const byToken = new Map<string, BotContact>();

      for (const contact of contacts.rows ?? []) {
        const token = (contact.contact_token ?? "").trim();
        if (!token || !contact.is_automated) {
          continue;
        }
        const description =
          typeof contact.profile_data?.description === "string"
            ? String(contact.profile_data.description).trim()
            : "";
        byToken.set(token, {
          contactToken: token,
          name: toTitleCase(contact.name || "Bot"),
          description: description || "Automated assistant",
        });
      }

      for (const bot of bots.rows ?? []) {
        const token = (bot.bot_contact_token ?? "").trim();
        if (!token) {
          continue;
        }
        const existing = byToken.get(token);
        byToken.set(token, {
          contactToken: token,
          name: toTitleCase(bot.name || existing?.name || "Bot"),
          description: (bot.description || existing?.description || "AI bot ready to chat.").trim(),
        });
      }

      setBotContacts(Array.from(byToken.values()).sort((a, b) => a.name.localeCompare(b.name)));
    } catch {
      setBotContacts([]);
    } finally {
      setIsLoadingBots(false);
    }
  };

  const filteredBots = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    if (!normalized) {
      return botContacts;
    }
    return botContacts.filter((bot) =>
      `${bot.name} ${bot.description}`.toLowerCase().includes(normalized),
    );
  }, [botContacts, query]);

  const cancel = () => {
    clearPendingMobileSharedItems();
    navigate("/chat/new");
  };

  const startWithBot = (contactToken: string) => {
    if (isStarting) {
      return;
    }
    setIsStarting(true);
    setPendingMobileShare({ attachments, note: note.trim() });
    clearPendingMobileSharedItems();
    navigate(`/chat/new/${encodeURIComponent(contactToken)}`);
  };

  const fileItems = items.filter((item) => item.kind === "file");
  const linkItems = items.filter((item) => item.kind !== "file");

  return (
    <div className="min-h-full bg-background text-foreground">
      <div className="mx-auto flex w-full max-w-4xl flex-col gap-4 px-4 py-6 sm:py-10">
        <div className="rounded-3xl border border-border bg-card p-5 shadow-sm sm:p-6">
          <Text type={TextTypes.Heading5} tag="h1" bold>
            Share to Open Chat
          </Text>
          <Text type={TextTypes.Body6} color="muted" className="mt-1">
            Review the shared content, then pick a bot to start a chat with it attached.
          </Text>

          <div className="mt-4 space-y-2">
            {fileItems.map((item) => {
              const attachment = attachments.find((entry) => entry.displayName === item.name);
              return (
                <div
                  key={`file-${item.index}`}
                  className="flex items-center gap-3 rounded-xl border border-border bg-muted/20 p-3"
                >
                  {attachment?.previewUrl ? (
                    <img
                      src={attachment.previewUrl}
                      alt={item.name}
                      className="size-12 shrink-0 rounded-lg border border-border object-cover"
                    />
                  ) : (
                    <div className="flex size-12 shrink-0 items-center justify-center rounded-lg border border-border bg-background">
                      {item.isImage ? <ImageIcon className="size-5" /> : <FileIcon className="size-5" />}
                    </div>
                  )}
                  <div className="min-w-0 flex-1">
                    <Text type={TextTypes.Body6} bold className="truncate">
                      {item.name}
                    </Text>
                    <Text type={TextTypes.Body7} color="muted">
                      {[formatSize(item.size), item.mimeType].filter(Boolean).join(" · ")}
                    </Text>
                  </div>
                  {attachment ? (
                    <Badge variant="secondary">Attached</Badge>
                  ) : (
                    <Badge variant="outline">{isUploading ? "Uploading" : "Skipped"}</Badge>
                  )}
                </div>
              );
            })}

            {linkItems.length > 0 ? (
              <div className="rounded-xl border border-border bg-muted/20 p-3">
                <div className="flex items-center gap-2">
                  <LinkIcon className="size-4" />
                  <Text type={TextTypes.Body6} bold>
                    Shared link / text
                  </Text>
                </div>
                <Text type={TextTypes.Body7} color="muted" className="mt-1 break-all">
                  {linkItems.map((item) => item.text || item.name).filter(Boolean).join("\n")}
                </Text>
              </div>
            ) : null}
          </div>

          <div className="mt-4 space-y-1">
            <Text type={TextTypes.Body7} color="muted">
              Message (optional)
            </Text>
            <Input
              value={note}
              onChange={(event) => setNote(event.target.value)}
              placeholder="Add a message for the bot"
              className="border"
            />
          </div>

          {status ? (
            <Text type={TextTypes.Body7} color="muted" className="mt-3">
              {status}
            </Text>
          ) : null}
        </div>

        <Card className="border-border/70">
          <CardHeader className="gap-2">
            <CardTitle className="text-lg">Choose a bot</CardTitle>
            <CardDescription>Select the bot that should receive the shared content.</CardDescription>
            <Input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search bots"
              className="border"
            />
          </CardHeader>
          <CardContent>
            {isLoadingBots ? (
              <div className="flex h-24 items-center justify-center">
                <LoadingSpinner />
              </div>
            ) : filteredBots.length === 0 ? (
              <Text type={TextTypes.Body6} color="muted">
                No bots available. Sign in to your server and try again.
              </Text>
            ) : (
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                {filteredBots.map((bot) => (
                  <button
                    key={bot.contactToken}
                    type="button"
                    disabled={isStarting}
                    onClick={() => startWithBot(bot.contactToken)}
                    className="rounded-xl border border-border bg-card p-3 text-left transition-colors hover:border-primary/60 hover:bg-primary/5 disabled:opacity-60"
                  >
                    <Text type={TextTypes.Body6} bold className="truncate">
                      {bot.name}
                    </Text>
                    <Text type={TextTypes.Body7} color="muted" className="line-clamp-2">
                      {bot.description}
                    </Text>
                  </button>
                ))}
              </div>
            )}
          </CardContent>
        </Card>

        <div className="flex justify-end">
          <Button type="button" variant="outline" onClick={cancel}>
            <X className="mr-1 size-4" />
            Cancel
          </Button>
        </div>
      </div>
    </div>
  );
}
