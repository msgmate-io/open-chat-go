import { ChatBase } from "@open-chat-go/ui";
import { ApiKeysOverview } from "@/components/chat/ApiKeysOverview";
import { navigate } from "vike/client/router";

export default function Page() {
  return (
    <ChatBase chatUUID={null} navigateTo={(to: string) => navigate(to)}>
      <ApiKeysOverview />
    </ChatBase>
  );
}
