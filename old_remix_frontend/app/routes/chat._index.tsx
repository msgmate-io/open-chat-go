import { ChatBase } from "@/components/chat/ChatBase";
import { useNavigate } from "@remix-run/react";
import { LoaderFunctionArgs, redirect } from "@remix-run/node";
import { checkAuth } from "~/utils/auth";

export default function ChatPage() {
  const navigate = useNavigate()
  return (
    <ChatBase chatUUID={null} navigateTo={(to: string) => {navigate(to)}}>
        no chat selected 
    </ChatBase>
  );
}