import { ChatBase } from "@/components/chat/ChatBase";
import { AuthGuard } from "@/components/AuthGuard";

export default function ChatPage() {
  return (
    <ChatBase chatUUID={null}>Hi there</ChatBase>
  );
}
