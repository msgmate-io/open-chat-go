import React from "react"
import { ChatBase } from "@/components/chat/ChatBase";
import { navigate } from 'vike/client/router'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/Card";
import { cn, fetcher } from "@/lib/utils";
import { ExploreChatsIcon } from "@/components/chat/DefaultChats";
import logoUrl from "@/assets/logo.png"
import useSWR from "swr";

export function ContactCard({
    navigateTo,
    defaultBotContact
}: {
    navigateTo: (to: string) => void,
    defaultBotContact: any
}) {
    return <>
  <Card className={cn(
    "bg-background hover:bg-secondary border-1")}
    onClick={() => {
        if (defaultBotContact) {
            navigateTo(`/chat/new/${defaultBotContact.contact_token}`)
        }
    }}
  >
    <CardHeader>
        <CardTitle className="">Hal 9025</CardTitle>
        <CardDescription>
          Default bot, can what with any ai backend using the openai api specification, <br></br>
          e.g.: 'lamma.cpp', 'localai.io', 'deepinfra', 'openai', 'groq' ...
        </CardDescription>
      </CardHeader>
      <CardContent>
      </CardContent>
  </Card>
</>
}

export default function Page() {
  
  const { data: contacts } = useSWR(`/api/v1/contacts/list`, fetcher)
  const defaultBotContact = contacts?.rows.find((contact: any) => contact.name === "bot")

  return <ChatBase chatUUID={null} navigateTo={(to: string) => {navigate(to)}}>
    <div className="flex flex-col w-full gap-2 content-center items-center">
      <h1 className="text-3xl font-bold text-foreground">Your Agents</h1>
      <br></br>
      <ContactCard navigateTo={(to: string) => {navigate(to)}} defaultBotContact={defaultBotContact} />
    </div>
  </ChatBase>
}