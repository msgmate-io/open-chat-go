"use client";

import { ChevronDown } from "lucide-react";
import { Badge } from "../badge";
import { Button } from "../button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../card";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "../collapsible";
import { BotMessageShell, PendingMessageItem, UserMessageShell } from "../chat";
import { Text, TextTypes } from "../text";

type MessageField = {
  name: string;
  type: string;
  use: string;
};

type MessageTypeItem = {
  label: string;
  description: string;
};

type ExampleMessage = {
  id: string;
  type: "user" | "assistant" | "pending";
  title: string;
  note: string;
  payload: Record<string, unknown>;
  preview: string;
};

const REAL_CHAT_LIST_SHAPE = {
  limit: 40,
  page: 1,
  sort: "Id desc",
  total_pages: 2,
  rows: [
    {
      uuid: "6204c095-0a2e-4cfc-8dbd-dd6d8106e465",
      send_at: "2026-06-10 10:32:41.042285626 +0000 UTC",
      sender_uuid: "1878459f-1605-4f76-8f8a-0581a6fc161e",
      text: "Would you like to know the current weather in a specific location?",
      reasoning: [""],
      tool_calls: [],
      meta_data: {
        cancelled: false,
        finished: true,
        thinking_time: "0s",
        token_usage: { completion_tokens: 517, prompt_tokens: 887, total_tokens: 1404 },
        total_time: "3.102s",
      },
    },
    {
      uuid: "853b92f9-dc19-4f73-a1a0-4cc5ce63a04c",
      send_at: "2026-06-10 10:30:37.194344994 +0000 UTC",
      sender_uuid: "1878459f-1605-4f76-8f8a-0581a6fc161e",
      text: "The current time is 2026-06-10T10:30:34Z (UTC).",
      reasoning: [""],
      tool_calls: [
        {
          arguments: {},
          id: "chatcmpl-tool-98c82cc48181415c",
          name: "get_current_time",
          result: "2026-06-10T10:30:34Z",
        },
      ],
      meta_data: {
        cancelled: false,
        finished: true,
        thinking_time: "0s",
        token_usage: { completion_tokens: 466, prompt_tokens: 845, total_tokens: 1311 },
        total_time: "4.063s",
      },
    },
  ],
};

const MESSAGE_FIELDS: MessageField[] = [
  { name: "uuid", type: "string", use: "Stable message identifier used in APIs, logs, and UI keys." },
  { name: "send_at", type: "datetime string", use: "Creation timestamp used for ordering, grouping, and time labels." },
  { name: "sender_uuid", type: "string", use: "Maps message ownership and is used to style user vs assistant rows." },
  { name: "text", type: "string", use: "Primary visible content rendered in the message bubble." },
  {
    name: "reasoning",
    type: "string[] | null",
    use: "API field for assistant thoughts; in `/chat/<chat_uuid>` this is mapped to `thoughts` before rendering. Real chats often send `[\"\"]`.",
  },
  {
    name: "tool_calls",
    type: "{name,arguments,result?}[] | null",
    use: "Tool execution trace rendered in the bot message collapsible while generating and after completion. Entries can include `id`.",
  },
  {
    name: "meta_data",
    type: "object | null",
    use: "Controls rendering behavior (`finished`, `thinking_time`, `cancelled`) and metrics (`token_usage`, `total_time`). `total_time` is frequently a string like `\"3.102s\"`.",
  },
  { name: "data_type", type: "string", use: "Storage/content mode (default `text`), useful if future media types are introduced." },
  { name: "read_at", type: "datetime | null", use: "Read receipt timestamp for delivery/read UX states." },
];

const MESSAGE_TYPES: MessageTypeItem[] = [
  {
    label: "user",
    description: "Input prompt row from the human participant.",
  },
  {
    label: "assistant",
    description: "Bot response row that can include thoughts, tool calls, and completion metadata.",
  },
  {
    label: "pending",
    description: "Transient UI row used while websocket updates stream in.",
  },
];

const EXAMPLES: ExampleMessage[] = [
  {
    id: "ex-user",
    type: "user",
    title: "User text message",
    note: "Typical end-user input message.",
    preview: "User bubble with markdown text and optional attachments.",
    payload: {
      uuid: "msg_01JXUSERA4V9",
      send_at: "2026-06-14T15:02:17Z",
      sender_uuid: "usr_01HX9A9K2",
      text: "Can you summarize the incident timeline?",
      reasoning: null,
      tool_calls: null,
      meta_data: { client: "web", locale: "en-US" },
      data_type: "text",
      read_at: null,
      role: "user",
    },
  },
  {
    id: "ex-assistant",
    type: "assistant",
    title: "Assistant text message (no thoughts/tools)",
    note: "Finished bot message with no reasoning/tool-call payload.",
    preview: "Bot bubble and footer stats from `meta_data.token_usage` + `meta_data.total_time`.",
    payload: {
      uuid: "msg_01JXASSIST9Z2",
      send_at: "2026-06-14T15:02:22Z",
      sender_uuid: "bot_01HXMODEL7",
      text: "Here is the timeline: 09:02 alert triggered, 09:07 mitigation applied, 09:24 recovery confirmed.",
      reasoning: null,
      tool_calls: null,
      meta_data: {
        token_usage: { prompt_tokens: 64, completion_tokens: 34, total_tokens: 98 },
        total_time: 1.42,
      },
      data_type: "text",
      read_at: "2026-06-14T15:02:30Z",
      role: "assistant"
    },
  },
  {
    id: "ex-assistant-thinking-tools",
    type: "assistant",
    title: "Assistant final message with tool call result",
    note: "Persisted row shape from `/api/v1/chats/<uuid>/messages/list`.",
    preview: "Shows tool-calls collapsible and footer stats for a completed message.",
    payload: {
      uuid: "853b92f9-dc19-4f73-a1a0-4cc5ce63a04c",
      send_at: "2026-06-10 10:30:37.194344994 +0000 UTC",
      sender_uuid: "1878459f-1605-4f76-8f8a-0581a6fc161e",
      text: "The current time is 2026-06-10T10:30:34Z (UTC).",
      reasoning: [""],
      thoughts: [""],
      tool_calls: [
        {
          arguments: {},
          id: "chatcmpl-tool-98c82cc48181415c",
          name: "get_current_time",
          result: "2026-06-10T10:30:34Z",
        },
      ],
      meta_data: {
        cancelled: false,
        finished: true,
        thinking_time: "0s",
        token_usage: { completion_tokens: 466, prompt_tokens: 845, total_tokens: 1311 },
        total_time: "4.063s",
      },
      data_type: "text",
      read_at: null,
      role: "assistant",
    },
  },
  {
    id: "ex-pending",
    type: "pending",
    title: "Pending/generating state",
    note: "Transient state shown while waiting for a final assistant message.",
    preview: "Displayed when partial websocket state exists but no completed DB message yet.",
    payload: {
      ui_state: "generating",
      preview: "Reasoning...",
      role: "assistant",
    },
  },
];

function MessageJsonCollapsible({ payload }: { payload: Record<string, unknown> }) {
  return (
    <Collapsible>
      <CollapsibleTrigger asChild>
        <Button type="button" variant="outline" size="sm" className="mt-2 h-7 px-2 text-xs">
          View JSON
          <ChevronDown className="ml-1 size-3" />
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <pre className="mt-2 overflow-auto rounded-md border bg-muted/30 p-3 text-xs leading-5">
          <code>{JSON.stringify(payload, null, 2)}</code>
        </pre>
      </CollapsibleContent>
    </Collapsible>
  );
}

function MessageExample({ item }: { item: ExampleMessage }) {
  const thinkingTime = typeof item.payload.meta_data === "object" && item.payload.meta_data !== null
    ? String((item.payload.meta_data as { thinking_time?: string }).thinking_time ?? "")
    : "";

  const toolCalls = Array.isArray(item.payload.tool_calls)
    ? (item.payload.tool_calls as Array<{ name?: string }>).map((tc) => tc.name).filter(Boolean)
    : [];

  return (
    <Card className="overflow-hidden">
      <CardHeader className="space-y-2 border-b bg-muted/30">
        <div className="flex items-center gap-2">
          <CardTitle className="text-base">{item.title}</CardTitle>
          <Badge variant="outline" className="text-[10px] uppercase">
            {item.type}
          </Badge>
        </div>
        <CardDescription>{item.note}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-2 p-3">
        {item.type === "user" ? <UserMessageShell senderLabel="You">{String(item.payload.text)}</UserMessageShell> : null}
        {item.type === "assistant" ? (
          <BotMessageShell senderLabel="Assistant">
            <Text type={TextTypes.Body6}>{String(item.payload.text || "") || "(no final text yet)"}</Text>
            {thinkingTime ? (
              <div className="mt-2 rounded-md border bg-muted/20 p-2">
                <Text type={TextTypes.Body7} color="muted">
                  Thinking... ({thinkingTime})
                </Text>
              </div>
            ) : null}
            {toolCalls.length > 0 ? (
              <div className="mt-2 rounded-md border bg-muted/20 p-2">
                <Text type={TextTypes.Body7} color="muted">
                  Calling tools: {toolCalls.join(", ")}
                </Text>
              </div>
            ) : null}
          </BotMessageShell>
        ) : null}
        {item.type === "pending" ? <PendingMessageItem text="Assistant is generating..." /> : null}
        <Text type={TextTypes.Body7} color="muted">
          {item.preview}
        </Text>
        <MessageJsonCollapsible payload={item.payload} />
      </CardContent>
    </Card>
  );
}

export function MessageModelReference() {
  return (
    <div className="space-y-5">
      <Card>
        <CardHeader className="border-b bg-muted/30">
          <CardTitle className="text-base">How `/chat/&lt;chat_uuid&gt;` maps messages</CardTitle>
          <CardDescription>The page maps API rows to the `MessageItem` props used for rendering.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 p-3">
          <pre className="overflow-auto rounded-md border bg-muted/30 p-3 text-xs leading-5">
            <code>{`{
  text: message.text || "",
  thoughts: message.reasoning || [],
  tool_calls: message.tool_calls || [],
  meta_data: message.meta_data || {}
}`}</code>
          </pre>
          <Text type={TextTypes.Body7} color="muted">
            `reasoning` is persisted in the backend response, but chat rendering reads it as `thoughts`.
          </Text>
          <Text type={TextTypes.Body7} color="muted">
            In real data, assistant rows often have `reasoning: ["\"]` (empty thought), user rows usually have `reasoning: null`, and `tool_calls` can be empty even for completed assistant messages.
          </Text>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="border-b bg-muted/30">
          <CardTitle className="text-base">Real list response shape</CardTitle>
          <CardDescription>Condensed, real-world `/messages/list` response with assistant completion metadata and tool calls.</CardDescription>
        </CardHeader>
        <CardContent className="p-3">
          <MessageJsonCollapsible payload={REAL_CHAT_LIST_SHAPE as unknown as Record<string, unknown>} />
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="border-b bg-muted/30">
          <CardTitle className="text-base">Message types</CardTitle>
          <CardDescription>Types that matter directly for rendering in the chat interaction page.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 p-3">
          {MESSAGE_TYPES.map((item) => (
            <div key={item.label} className="rounded-md border bg-muted/20 p-3">
              <div className="mb-1 flex items-center gap-2">
                <Badge variant="secondary" className="text-[10px] uppercase">
                  {item.label}
                </Badge>
              </div>
              <Text type={TextTypes.Body6}>{item.description}</Text>
            </div>
          ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="border-b bg-muted/30">
          <CardTitle className="text-base">Message model fields</CardTitle>
          <CardDescription>Database/API fields and what each one is used for in practice.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 p-3">
          {MESSAGE_FIELDS.map((field) => (
            <div key={field.name} className="rounded-md border bg-muted/20 p-3">
              <div className="flex flex-wrap items-center gap-2">
                <code className="text-xs font-medium">{field.name}</code>
                <code className="text-[11px] text-muted-foreground">{field.type}</code>
              </div>
              <Text type={TextTypes.Body6} className="mt-1">
                {field.use}
              </Text>
            </div>
          ))}
        </CardContent>
      </Card>

      <div className="space-y-4">
        {EXAMPLES.map((item) => (
          <MessageExample key={item.id} item={item} />
        ))}
      </div>
    </div>
  );
}
