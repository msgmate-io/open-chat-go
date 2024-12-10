import { ChatBase } from "@/components/chat/ChatBase";
import { useParams } from "@remix-run/react";
import { useNavigate } from "@remix-run/react";
import { MessagesView } from "@/components/chat/MessagesView";

export default function ChatPage() {
  const { chat_uuid } = useParams();
  const navigate = useNavigate()

  return <ChatBase chatUUID={chat_uuid || null} navigateTo={(to: string) => {navigate(to)}}>
      <MessagesView 
        chatUUID={chat_uuid || null} 
        leftPannelCollapsed={false} 
        onToggleCollapse={() => {}}/>
    </ChatBase>
}
