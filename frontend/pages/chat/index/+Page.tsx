import { ChatBase } from "@/components/chat/ChatBase";
import { navigate } from 'vike/client/router'

export default function ChatPage() {
  return <ChatBase chatUUID={null} navigateTo={(to: string) => {navigate(to)}}>
    no chat selected
    </ChatBase>
}