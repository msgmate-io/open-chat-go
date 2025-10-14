import { ChatBase } from "@open-chat-go/ui";
import { ActionTasksView } from "@/components/chat/ActionTasksView";
import { navigate } from "vike/client/router";

export default function Page() {
  return (
    <ChatBase chatUUID={null} navigateTo={(to: string) => navigate(to)}>
      <ActionTasksView navigateTo={(to: string) => navigate(to)} />
    </ChatBase>
  );
}
