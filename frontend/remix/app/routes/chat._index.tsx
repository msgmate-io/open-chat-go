import { ChatBase } from "@/components/chat/ChatBase";
import { useNavigate } from "@remix-run/react";

export default function ChatPage() {
  const navigate = useNavigate()
  return (
    <ChatBase chatUUID={null} navigateTo={(to: string) => {navigate(to)}}>
        no chat selected 
    </ChatBase>
  );
}

// Disable server-side rendering for this route
export const handle = {
  hydrate: true,
}; 