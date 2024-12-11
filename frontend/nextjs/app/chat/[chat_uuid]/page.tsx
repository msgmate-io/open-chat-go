import Chat from "@/next-components/Chat";

export default function ChatPage() {
  return <Chat />
}

export function generateStaticParams() {
  return [{ chat_uuid: "123" }];
}