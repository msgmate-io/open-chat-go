import useSWR, { mutate } from "swr"
import { MessageItem } from "@/components/chat/MessageItem"
import React, { useEffect, useState, useRef, forwardRef } from 'react';
import { CollapseIndicator } from "@/components/CollapseIndicator";
import { usePartialMessageStore } from "@/components/chat/PartialMessages";
import { MessageInput } from "@/components/chat/MessageInput";
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
    }

    const onStopBotResponse = () => {
    }

    return <div className="flex flex-col h-full w-full lg:max-w-[900px] relative">
        <div ref={scrollRef} className="flex flex-col flex-grow gap-2 items-center content-center overflow-y-auto relative pb-4 pt-2">
            {messages && messages.rows.map((message: any) => <MessageItem key={`msg_${message.uuid}`} message={message} chat={chat} selfIsSender={user?.uuid === message.sender_uuid} isBotChat={true} />).reverse()}
            {chatUUID && partialMessages?.[chatUUID] && <MessageItem key={`msg_${chatUUID}`} message={{text: partialMessages[chatUUID]}} chat={chat} selfIsSender={user?.uuid === chat.sender_uuid} isBotChat={true} />}
        </div>
        {!hideInput && <MessageInput text={text} setText={setText} isLoading={false} isBotResponding={false} stopBotResponse={onStopBotResponse} onSendMessage={onSendMessage} ref={inputRef} />}
    </div>
}


export function MessagesView({ 
        chatUUID = null, 
        leftPannelCollapsed = false, 
        onToggleCollapse = () => {}
    }: {
        chatUUID: string | null,
        leftPannelCollapsed?: boolean,
        onToggleCollapse?: () => void
    }) {

    const { data: chat } = useSWR(`/api/v1/chats/${chatUUID}`, fetcher)
    const { data: messages, mutate: mutateMessages } = useSWR(`/api/v1/chats/${chatUUID}/messages/list`, fetcher)
    const { data: user } = useSWR(`/api/v1/user/self`, fetcher)

    return <>
        <div className="flex flex-col h-full w-full content-center items-center">
            {leftPannelCollapsed && <div className="w-full flex items-center content-center justify-left">
                <div className="absolute top-0 mt-2 ml-2 z-40">
                    <CollapseIndicator leftPannelCollapsed={leftPannelCollapsed} onToggleCollapse={onToggleCollapse} />
                </div>
            </div>}
            <div className="absolute left-0 p-2 flex items-center content-center justify-left z-30"></div>
            <MessagesScroll chatUUID={chatUUID} user={user} hideInput={false} messages={messages} chat={chat} />
        </div>
    </>
}
