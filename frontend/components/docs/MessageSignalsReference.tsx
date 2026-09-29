import { ChevronDown } from "lucide-react";
import { Badge, Button, Card, CardContent, CardDescription, CardHeader, CardTitle, Collapsible, CollapsibleContent, CollapsibleTrigger } from "@open-chat-go/ui";

type SignalExample = {
  type: string;
  source_url: string;
  note: string;
  payload: Record<string, unknown>;
};

const SIGNAL_EXAMPLES: SignalExample[] = [
  {
    type: "start_partial_message",
    source_url: "https://github.com/msgmate-io/open-chat-go/blob/main/backend/api/websocket/messages.go#L83",
    note: "Marks the beginning of assistant streaming for a chat.",
    payload: {
      type: "start_partial_message",
      content: {
        chat_uuid: "<chat_uuid>",
        sender_uuid: "<sender_uuid>",
      },
    },
  },
  {
    type: "new_partial_message",
    source_url: "https://github.com/msgmate-io/open-chat-go/blob/main/backend/api/websocket/messages.go#L115",
    note: "Streams partial text/thoughts/tool-calls while the assistant is still generating.",
    payload: {
      type: "new_partial_message",
      content: {
        chat_uuid: "<chat_uuid>",
        sender_uuid: "<sender_uuid>",
        text: "...partial assistant text...",
        reasoning: [],
        meta_data: {
          finished: false,
        },
        tool_calls: [],
      },
    },
  },
  {
    type: "end_partial_message",
    source_url: "https://github.com/msgmate-io/open-chat-go/blob/main/backend/api/websocket/messages.go#L99",
    note: "Signals the end of the partial streaming phase.",
    payload: {
      type: "end_partial_message",
      content: {
        chat_uuid: "<chat_uuid>",
        sender_uuid: "<sender_uuid>",
      },
    },
  },
  {
    type: "new_message",
    source_url: "https://github.com/msgmate-io/open-chat-go/blob/main/backend/api/websocket/messages.go#L141",
    note: "Emits the finalized persisted message payload.",
    payload: {
      type: "new_message",
      content: {
        chat_uuid: "<chat_uuid>",
        sender_uuid: "<sender_uuid>",
        text: "...final assistant text...",
        reasoning: [],
        meta_data: {
          finished: true,
          token_usage: {
            prompt_tokens: 123,
            completion_tokens: 45,
            total_tokens: 168,
          },
          total_time: "2.345s",
        },
        tool_calls: [],
      },
    },
  },
  {
    type: "interrupt_signal",
    source_url: "https://github.com/msgmate-io/open-chat-go/blob/main/backend/api/websocket/messages.go#L167",
    note: "Sent when a user interrupts generation (`/api/v1/chats/{chat_uuid}/signals/interrupt`).",
    payload: {
      type: "interrupt_signal",
      content: {
        chat_uuid: "<chat_uuid>",
        sender_uuid: "<sender_uuid>",
      },
    },
  },
];

function SignalJson({ data }: { data: unknown }) {
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

export function MessageSignalsReference() {
  return (
    <div className="space-y-4">
      <Card>
        <CardHeader className="border-b bg-muted/30">
          <CardTitle className="text-base">Signal Stream Reference</CardTitle>
          <CardDescription>
            Signal builders are in
            {" "}
            <a className="underline" href="https://github.com/msgmate-io/open-chat-go/blob/main/backend/api/websocket/messages.go" target="_blank" rel="noreferrer">backend/api/websocket/messages.go</a>
            {" "}
            and the primary consumer is
            {" "}
            <a className="underline" href="https://github.com/msgmate-io/open-chat-go/blob/main/frontend/components/WebsocketHandler.tsx" target="_blank" rel="noreferrer">frontend/components/WebsocketHandler.tsx</a>
            .
          </CardDescription>
        </CardHeader>
      </Card>

      {SIGNAL_EXAMPLES.map((item) => (
        <Card key={item.type}>
          <CardHeader className="border-b bg-muted/30">
            <div className="flex items-center gap-2">
              <CardTitle className="text-base">{item.type}</CardTitle>
              <Badge variant="outline" className="text-[10px] uppercase">signal</Badge>
            </div>
            <CardDescription>{item.note}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2 p-3">
            <Button asChild variant="outline" size="sm" className="h-7 px-2 text-xs">
              <a href={item.source_url} target="_blank" rel="noreferrer">Source</a>
            </Button>
            <SignalJson data={item.payload} />
          </CardContent>
        </Card>
      ))}
    </div>
  );
}
