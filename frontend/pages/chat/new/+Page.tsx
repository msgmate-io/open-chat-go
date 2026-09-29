import { ChatBase } from "@open-chat-go/ui";
import { EntitiesOverview } from "@/components/chat/EntitiesOverview";
import { navigate } from "vike/client/router";

export default function Page() {
  return (
    <ChatBase chatUUID={null} navigateTo={(to: string) => navigate(to)}>
      <EntitiesOverview navigateTo={(to: string) => navigate(to)} routeMode="all" />
    </ChatBase>
  );
}
