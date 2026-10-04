import { ChatBase, WebsocketHandler } from "@open-chat-go/ui";
import { navigate } from 'vike/client/router'
import { Button, Text, TextTypes } from "@open-chat-go/ui";

export default function ChatPage() {
  return (
    <>
      <ChatBase chatUUID={null} navigateTo={(to: string) => { navigate(to) }} mobileViewMode="list">
        <div className="chat-empty-state mx-auto">
          <Text type={TextTypes.Heading5} tag="h1" bold>
            Select a conversation
          </Text>
          <Text type={TextTypes.Body6} color="muted">
            Pick a chat from the sidebar or start a new one.
          </Text>
          <Button onClick={() => navigate("/chat/new")} size="sm">
            Browse agents
          </Button>
        </div>
      </ChatBase>
      <WebsocketHandler />
    </>
  );
}
