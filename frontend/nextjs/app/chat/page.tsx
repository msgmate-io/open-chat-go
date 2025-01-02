"use client"

import { ChatBase } from "@/components/chat/ChatBase";
import { useRouter } from "next/navigation";

export default function ChatPage() {
  const router = useRouter();

  return (
    <ChatBase chatUUID={null} navigateTo={(to: string) => {router.push(to)}}>Hi there</ChatBase>
  );
}
