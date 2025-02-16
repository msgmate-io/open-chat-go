import useSWR, { mutate } from "swr"
import { MessageItem } from "@/components/chat/MessageItem"
import React, { useEffect, useState, useRef, forwardRef, useCallback } from 'react';
import { CollapseIndicator } from "@/components/CollapseIndicator";
import { usePartialMessageStore } from "@/components/chat/PartialMessages";
import { MessageInput } from "@/components/chat/MessageInput";
import { useSidePanelCollapse } from "./ChatBase";
import { BotDisplay } from "./BotSelector";
import { PendingMessageItem } from "./PendingMessageItem";
const fetcher = (...args: [RequestInfo, RequestInit?]) => fetch(...args).then(res => res.json())

const ScrollButton = React.memo(({ scrollRef }: { scrollRef: React.RefObject<HTMLDivElement> }) => {
    const [shouldShow, setShouldShow] = useState(false);
    const scrollTimeoutRef = useRef<NodeJS.Timeout | null>(null);

    const scrollToBottom = () => {
        if (scrollRef.current) {
            const scrollElement = scrollRef.current;
            const maxScroll = scrollElement.scrollHeight - scrollElement.clientHeight;
            scrollElement.scrollTo({
                top: maxScroll,
                behavior: 'smooth'
            });
        }
    };

    const handleScroll = useCallback(() => {
        if (!scrollRef.current) return;
        
        if (scrollTimeoutRef.current) {
            clearTimeout(scrollTimeoutRef.current);
        }
        
        scrollTimeoutRef.current = setTimeout(() => {
            const { scrollTop, scrollHeight, clientHeight } = scrollRef.current!;
            const distance = Math.abs(scrollHeight - clientHeight - scrollTop);
            setShouldShow(distance >= 150);
        }, 100);
    }, []);

    useEffect(() => {
        const scrollElement = scrollRef.current;
        if (scrollElement) {
            scrollElement.addEventListener('scroll', handleScroll);
            return () => scrollElement.removeEventListener('scroll', handleScroll);
        }
    }, [handleScroll]);

    useEffect(() => {
        return () => {
            if (scrollTimeoutRef.current) {
                clearTimeout(scrollTimeoutRef.current);
            }
        };
    }, []);

    return (
        <button 
            onClick={scrollToBottom}
            className={`fixed bottom-36 right-8 bg-gray-800 hover:bg-gray-700 text-white rounded-full p-3 shadow-lg transition-all duration-300 transform ${
                shouldShow ? 'opacity-100 pointer-events-auto translate-y-0' : 'opacity-0 pointer-events-none translate-y-4'
            }`}
        >
            <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 14l-7 7m0 0l-7-7m7 7V3" />
            </svg>
        </button>
    );
});

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
    const { partialMessages, addPartialMessage, removePartialMessage } = usePartialMessageStore()
    const [isSendingMessage, setIsSendingMessage] = useState(false)

    const scrollRef = useRef<HTMLDivElement>(null);
    const inputRef = useRef<HTMLTextAreaElement>(null);

    useEffect(() => {
        if (scrollRef.current) {
            const scrollElement = scrollRef.current;
            const maxScroll = scrollElement.scrollHeight - scrollElement.clientHeight;
            scrollElement.scrollTop = maxScroll;
        }
    }, [messages, partialMessages]);

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
        setIsSendingMessage(false)
    }

    const onStopBotResponse = () => {
        fetch(`/api/v1/chats/${chatUUID}/signals/interrupt`, {
            method: "POST",
        })
        console.log("interrupt sent")
    }
    
    const isBotResponding = chatUUID ? (partialMessages?.[chatUUID]?.text.length > 0 || partialMessages?.[chatUUID]?.thoughts.length > 0) : false

    return <div className="flex flex-col h-full w-full lg:max-w-[900px] relative">
        <div ref={scrollRef} className="flex flex-col flex-grow gap-2 items-center content-center overflow-y-auto relative pb-4 pt-2">
            {messages && messages.rows.map((message: any) => <MessageItem key={`msg_${message.uuid}`} message={{text: message.text, thoughts: message.reasoning, meta_data: message?.meta_data, tool_calls: message?.tool_calls}} chat={chat} selfIsSender={user?.uuid === message.sender_uuid} isBotChat={true} />).reverse()}
            {chatUUID && partialMessages?.[chatUUID] && <MessageItem key={`msg_${chatUUID}`} message={{text: partialMessages[chatUUID]?.text, thoughts: partialMessages[chatUUID]?.thoughts, meta_data: partialMessages[chatUUID]?.meta_data, tool_calls: partialMessages[chatUUID]?.tool_calls, is_generating: true}} chat={chat} selfIsSender={user?.uuid === chat.sender_uuid} isBotChat={true} />}
        </div>
        <ScrollButton scrollRef={scrollRef} />
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
                <div className="absolute top-0 mt-2 ml-2 z-40 flex">
                    {leftPannelCollapsed && <CollapseIndicator leftPannelCollapsed={leftPannelCollapsed} onToggleCollapse={onToggleCollapse} />}
                    <BotDisplay selectedModel={chat?.config?.model} />
                </div>
            </div>
            <div className="absolute left-0 p-2 flex items-center content-center justify-left z-30"></div>
            <MessagesScroll chatUUID={chatUUID} user={user} hideInput={false} messages={messages} chat={chat} />
        </div>
    </>
}
