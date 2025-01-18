import useSWR, { mutate } from "swr"
import { MessageItem } from "@/components/chat/MessageItem"
import React, { useEffect, useState, useRef, forwardRef } from 'react';
import { CollapseIndicator } from "@/components/CollapseIndicator";
import { usePartialMessageStore } from "@/components/chat/PartialMessages";
import { MessageInput } from "@/components/chat/MessageInput";
import { useSidePanelCollapse } from "./ChatBase";
import { BotDisplay } from "./BotSelector";
const fetcher = (...args: [RequestInfo, RequestInit?]) => fetch(...args).then(res => res.json())

export function MessagesScroll({ 
    chatUUID,
    user,
    hideInput = false,
    messages,
    chat,
}: {
    messages: any,
    chat: any,
    chatUUID: string | null,
    user: any,
    hideInput: boolean;
}) {
    const [text, setText] = useState("");
    const [stickScroll, setStickScroll] = useState(false)                                                                                                                                                            
    const { partialMessages, addPartialMessage, removePartialMessage } = usePartialMessageStore()                                                                                                                    
    const [isSendingMessage, setIsSendingMessage] = useState(false)

    const scrollRef = useRef<HTMLDivElement>(null)
    const inputRef = useRef<HTMLTextAreaElement>(null)
    
    const scrollToBottom = () => {
        if (scrollRef.current) {
            scrollRef.current.scrollTop = scrollRef.current.scrollHeight
        }
    }
                                                                                                                                                                                                                      
    useEffect(() => {
        if(chatUUID && partialMessages?.[chatUUID] && stickScroll){
            scrollToBottom()
        }
    }, [partialMessages])

    useEffect(() => {
        scrollToBottom()
    }, [messages])

    const onSendMessage = async () => {
        setIsSendingMessage(true)
        const res = await fetch(`/api/v1/chats/${chatUUID}/messages/send`, {
            method: "POST",
            body: JSON.stringify({
                text: text
            })
        })

        if(res.ok){
            const newMessage = await res.json()
            console.log("newMessage", newMessage)
            mutate(`/api/v1/chats/${chatUUID}/messages/list`, {
                ...messages,
                rows: [newMessage,...messages.rows]
            }, false)
        }
        setStickScroll(true)
        setIsSendingMessage(false)
    }

    const onStopBotResponse = () => {
        fetch(`/api/v1/chats/${chatUUID}/signals/interrupt`, {
            method: "POST",
        })
        console.log("interrupt sent")
    }
    
    const isBotResponding = chatUUID ? partialMessages?.[chatUUID]?.length > 0 : false

    return <div className="flex flex-col h-full w-full lg:max-w-[900px] relative">
        <div ref={scrollRef} className="flex flex-col flex-grow gap-2 items-center content-center overflow-y-auto relative pb-4 pt-2">
            {messages && messages.rows.map((message: any) => <MessageItem key={`msg_${message.uuid}`} message={message} chat={chat} selfIsSender={user?.uuid === message.sender_uuid} isBotChat={true} />).reverse()}
            {chatUUID && partialMessages?.[chatUUID] && <MessageItem key={`msg_${chatUUID}`} message={{text: partialMessages[chatUUID]}} chat={chat} selfIsSender={user?.uuid === chat.sender_uuid} isBotChat={true} />}
        </div>
        {!hideInput && <MessageInput text={text} setText={setText} isLoading={isSendingMessage} isBotResponding={isBotResponding} stopBotResponse={onStopBotResponse} onSendMessage={onSendMessage} ref={inputRef} />}
    </div>
}


export function MessagesView({ 
        chatUUID = null, 
    }: {
        chatUUID: string | null,
    }) {

    const { data: chat } = useSWR(`/api/v1/chats/${chatUUID}`, fetcher)
    const { data: messages, mutate: mutateMessages } = useSWR(`/api/v1/chats/${chatUUID}/messages/list`, fetcher)
    const { data: user } = useSWR(`/api/v1/user/self`, fetcher)
    const { data: contact } = useSWR(`/api/v1/chats/${chatUUID}/contact`, fetcher)

    const leftPannelCollapsed = useSidePanelCollapse(state => state.isCollapsed);
    const onToggleCollapse = useSidePanelCollapse(state => state.toggle);
    
    console.log("contact", contact)
    console.log("chat", chat)

    return <>
        <div className="flex flex-col h-full w-full content-center items-center">
            <div className="w-full flex items-center content-center justify-left">
                <div className="absolute top-0 mt-2 ml-2 z-40">
                    {leftPannelCollapsed && <CollapseIndicator leftPannelCollapsed={leftPannelCollapsed} onToggleCollapse={onToggleCollapse} />}
                    <BotDisplay selectedModel={chat?.config?.model} />
                </div>
            </div>
            <div className="absolute left-0 p-2 flex items-center content-center justify-left z-30"></div>
            <MessagesScroll chatUUID={chatUUID} user={user} hideInput={false} messages={messages} chat={chat} />
        </div>
    </>
}
