"use client"

import { ChatBase } from "@/components/chat/ChatBase";
import useSWR from 'swr'
const fetcher = (...args) => fetch(...args).then(res => res.json())

function ListChats(){
  const { data: chats, error, isLoading } = useSWR('/api/v1/chats/list', fetcher)
  return <div>{JSON.stringify(chats)}</div>
}

export default function ChatPage() {
  const { data: user, error, isLoading, mutate } = useSWR('/api/v1/user/self', fetcher)
  return (
    <div className="">
      <ChatBase/>
      <main className="">
        Hello From Inside chat
        <div>{JSON.stringify(user)}</div>
        <div><ListChats/></div>
        <button onClick={() => {
          mutate((prevData) => {
            return {
              ...prevData,
              uuid: "hello"
            }
          },
          { revalidate: false })
        }}>
          Click to mutate uuid to 'hello'
        </button>
      </main>
    </div>
  );
}
