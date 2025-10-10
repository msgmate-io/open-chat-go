import { ChevronDown } from "lucide-react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@open-chat-go/ui";
import { Badge, Button, Collapsible, CollapsibleContent, CollapsibleTrigger, Text, TextTypes } from "@open-chat-go/ui";
import { MessageItem, PendingMessageItem } from "@open-chat-go/ui";

const chat = {
  partner: {
    name: "HAL",
    is_bot: true,
  },
};

const assistantWithToolCalls = {
  uuid: "853b92f9-dc19-4f73-a1a0-4cc5ce63a04c",
  text: "The current time is **2026-06-10T10:30:34Z** (UTC).",
  thoughts: ["I should call the time tool to answer exactly."],
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
    thinking_time: "0.2s",
    token_usage: {
      completion_tokens: 466,
      prompt_tokens: 845,
      total_tokens: 1311,
    },
    total_time: "4.063s",
    confirmable_actions: [
      {
        action_id: "chatcmpl-tool-confirm-1",
        source_tool_name: "create_confirmable_action_suggestion",
        target_tool_name: "get_weather",
        status: "pending",
        title: "Fetch weather for Berlin",
        description: "Review the input before running the actual weather tool.",
        confirm_label: "Run weather tool",
        input: { city: "Berlin", unit: "celsius" },
      },
    ],
  },
};

const assistantStreaming = {
  uuid: "streaming-example-1",
  text: "",
  thoughts: ["Need to fetch weather for the requested city."],
  tool_calls: [
    {
      arguments: { city: "Berlin", unit: "celsius" },
      name: "get_weather",
    },
  ],
  is_generating: true,
  meta_data: {
    finished: false,
    thinking_time: "1.1s",
    total_time: "1.1s",
  },
};

const assistantBooting = {
  uuid: "streaming-example-booting",
  text: "",
  thoughts: [],
  tool_calls: [],
  is_generating: true,
  meta_data: {
    finished: false,
    thinking_time: "0s",
    total_time: "0s",
  },
};

const userMessage = {
  uuid: "c2f20cd5-7ec8-4197-a4fd-68848b679627",
  text: "Very interesting",
  meta_data: {},
};

function JsonBlock({ data }: { data: unknown }) {
  return (
    <Collapsible>
      <CollapsibleTrigger asChild>
        <Button variant="outline" size="sm" className="h-7 px-2 text-xs">
          View JSON
          <ChevronDown className="ml-1 size-3" />
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <pre className="mt-2 overflow-auto rounded-md border bg-muted/30 p-3 text-xs leading-5">
          <code>{JSON.stringify(data, null, 2)}</code>
        </pre>
      </CollapsibleContent>
    </Collapsible>
  );
}

export function MessageModelReference() {
  return (
    <div className="space-y-5">
      <Card>
        <CardHeader className="border-b bg-muted/30">
          <CardTitle className="text-base">Rendered like `/chat/&lt;chat_uuid&gt;`</CardTitle>
          <CardDescription>
            These examples use the same `MessageItem` component as the live chat page, so thoughts and tool-calls sections behave the same.
          </CardDescription>
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
            Important: persisted API field is `reasoning`; chat UI renders it as `thoughts`.
          </Text>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="border-b bg-muted/30">
          <div className="flex items-center gap-2">
            <CardTitle className="text-base">Assistant final message with tool call</CardTitle>
            <Badge variant="secondary" className="text-[10px] uppercase">assistant</Badge>
          </div>
          <CardDescription>Open the thoughts/tool-calls sections in the bubble below.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 p-3">
          <MessageItem message={assistantWithToolCalls} chat={chat} chatUUID="demo-chat" isBotChat />
          <JsonBlock
            data={{
              uuid: assistantWithToolCalls.uuid,
              text: assistantWithToolCalls.text,
              reasoning: assistantWithToolCalls.thoughts,
              tool_calls: assistantWithToolCalls.tool_calls,
              meta_data: assistantWithToolCalls.meta_data,
            }}
          />
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="border-b bg-muted/30">
          <div className="flex items-center gap-2">
            <CardTitle className="text-base">Assistant streaming state</CardTitle>
            <Badge variant="outline" className="text-[10px] uppercase">pending</Badge>
          </div>
          <CardDescription>Shows the open thoughts/tool-calls sections while response is in progress.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 p-3">
          <MessageItem message={assistantStreaming} chat={chat} chatUUID="demo-chat" isBotChat />
          <PendingMessageItem text="Assistant is generating..." />
          <JsonBlock
            data={{
              uuid: assistantStreaming.uuid,
              text: assistantStreaming.text,
              reasoning: assistantStreaming.thoughts,
              tool_calls: assistantStreaming.tool_calls,
              meta_data: assistantStreaming.meta_data,
            }}
          />
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="border-b bg-muted/30">
          <div className="flex items-center gap-2">
            <CardTitle className="text-base">Assistant booting animation</CardTitle>
            <Badge variant="outline" className="text-[10px] uppercase">booting</Badge>
          </div>
          <CardDescription>Same dot animation shown in chat before thoughts/tool-calls/text arrive.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 p-3">
          <MessageItem message={assistantBooting} chat={chat} chatUUID="demo-chat" isBotChat />
          <JsonBlock
            data={{
              uuid: assistantBooting.uuid,
              text: assistantBooting.text,
              reasoning: assistantBooting.thoughts,
              tool_calls: assistantBooting.tool_calls,
              meta_data: assistantBooting.meta_data,
            }}
          />
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="border-b bg-muted/30">
          <div className="flex items-center gap-2">
            <CardTitle className="text-base">User message</CardTitle>
            <Badge variant="outline" className="text-[10px] uppercase">user</Badge>
          </div>
        </CardHeader>
        <CardContent className="space-y-2 p-3">
          <MessageItem message={userMessage} chat={chat} chatUUID="demo-chat" selfIsSender isBotChat />
          <JsonBlock data={{ ...userMessage, reasoning: null, tool_calls: [], meta_data: {} }} />
        </CardContent>
      </Card>
    </div>
  );
}
