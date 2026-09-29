import { usePageContext } from "vike-react/usePageContext";
import { VoiceChatPage } from "@open-chat-go/ui";

export default function ChatVoicePageRoute() {
  const pageContext = usePageContext();
  const chatUUID = String(pageContext.routeParams.chat_uuid || "");
  return <VoiceChatPage chatUUID={chatUUID} />;
}
