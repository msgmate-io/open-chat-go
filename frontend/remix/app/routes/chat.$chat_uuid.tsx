import { ChatBase } from "@/components/chat/ChatBase";
import { useParams } from "@remix-run/react";
import { useNavigate } from "@remix-run/react";
import { MessagesView } from "@/components/chat/MessagesView";

export default function ChatPage() {
  const { chat_uuid } = useParams();
  const chatUUID = chat_uuid || null;
  const navigate = useNavigate()

  return <ChatBase chatUUID={chatUUID} navigateTo={(to: string) => {navigate(to)}}>
      <MessagesView chatUUID={chatUUID} />
    </ChatBase>
}
