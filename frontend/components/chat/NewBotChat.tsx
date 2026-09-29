import React from 'react';
import logoUrl from "@/assets/logo.png?inline"
import { Text, TextTypes } from "@open-chat-go/ui";

const CHAT_INTROS = [
    {
        title: "Let Hal Mixtral write you a simple script",
        description: "Hey Hal, can you write me a simple script to automate my daily tasks?"
    },
    {
        title: "Chat with Bots",
        description: "Explore and chat with bots. You can also create your own bot and share it with others."
    },
    {
        title: "Chat with Users",
        description: "Start chatting with other users. You can also create your own bot and share it with others."
    }
]

export function NewBotChatCard({
    startChat = (message: string) => {}
}: {
    startChat: (message: string) => void
}) {
    return <>
        <div className='flex flex-col relative w-full h-full content-center items-center justify-center'>
            <img
                src={logoUrl}
                className="w-[100px] md:w-[200px] lg:w-[300px] object-contain"
                alt="About services"
            />
            <div className="grid w-full grid-cols-1 gap-2 text-foreground md:grid-cols-3">
                {CHAT_INTROS.map((intro, i) => <div key={i} className="flex min-h-[116px] w-full cursor-pointer flex-col overflow-hidden rounded-2xl border border-border/60 p-2 transition-colors hover:bg-background" onClick={() => startChat(intro.description)}>
                    <Text type={TextTypes.Body6} tag="h2" bold className="py-1 leading-tight">
                        {intro.title}
                    </Text>
                    <Text type={TextTypes.Body6} className="max-h-16 overflow-y-auto break-words pr-1 text-sm leading-snug">
                        {intro.description}
                    </Text>
                </div>)}
            </div>
        </div>
    </>
}
