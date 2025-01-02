import useSWR, { mutate } from "swr"
import { MessageItem } from "@/components/chat/MessageItem"
import React, { useEffect, useState, useRef, forwardRef } from 'react';
import { cn } from "@/components/utils";
import { Card } from "@/components/Card";
import { Textarea } from "../Textarea";
import { useMediaQuery } from 'react-responsive';
import {
    Button
} from "@/components/Button";

import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuLabel,
    DropdownMenuSeparator,
    DropdownMenuTrigger
} from "@/components/DropdownMenu";
const fetcher = (...args: [RequestInfo, RequestInit?]) => fetch(...args).then(res => res.json())

export const SendMessageButton = ({ 
        onClick, 
        isLoading 
    }: {
        onClick: () => void,
        isLoading: boolean
    }) => {

    return <Button
        onClick={onClick}
        disabled={isLoading}
        className="ml-2 bg-base-300 text-white p-2 rounded-full flex items-center justify-center"
    >
        <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><line x1="5" y1="12" x2="19" y2="12" /><polyline points="12 5 19 12 12 19" /></svg>
    </Button>
}

export const CollapseIndicator = ({
    leftPannelCollapsed,
    onToggleCollapse
}: {
    leftPannelCollapsed: boolean,
    onToggleCollapse: () => void
}) => {
    const isSm = useMediaQuery({ query: '(max-width: 640px)' })

    const onClick = () => {
        console.log("onToggleCollapse")
        if (!isSm) {
            onToggleCollapse()
        } else {
            // navigate(null, { chat: null }) TODO
        }
    }

    return <div className={cn("flex p-2 rounded-xl hover:bg-accent", {
        "hover:bg-base-100": !leftPannelCollapsed,
        "hover:bg-base-300": leftPannelCollapsed
    })} onClick={onClick}>
        <CollapseSvgIcon />
    </div>
}

const CollapseSvgIcon = () => (
    <svg
        xmlns="http://www.w3.org/2000/svg"
        width="24"
        height="24"
        fill="none"
        viewBox="0 0 24 24"
        className="icon-xl-heavy"
    >
        <path
            fill="currentColor"
            fillRule="evenodd"
            d="M8.857 3h6.286c1.084 0 1.958 0 2.666.058.729.06 1.369.185 1.961.487a5 5 0 0 1 2.185 2.185c.302.592.428 1.233.487 1.961.058.708.058 1.582.058 2.666v3.286c0 1.084 0 1.958-.058 2.666-.06.729-.185 1.369-.487 1.961a5 5 0 0 1-2.185 2.185c-.592.302-1.232.428-1.961.487C17.1 21 16.227 21 15.143 21H8.857c-1.084 0-1.958 0-2.666-.058-.728-.06-1.369-.185-1.96-.487a5 5 0 0 1-2.186-2.185c-.302-.592-.428-1.232-.487-1.961C1.5 15.6 1.5 14.727 1.5 13.643v-3.286c0-1.084 0-1.958.058-2.666.06-.728.185-1.369.487-1.96A5 5 0 0 1 4.23 3.544c.592-.302 1.233-.428 1.961-.487C6.9 3 7.773 3 8.857 3M6.354 5.051c-.605.05-.953.142-1.216.276a3 3 0 0 0-1.311 1.311c-.134.263-.226.611-.276 1.216-.05.617-.051 1.41-.051 2.546v3.2c0 1.137 0 1.929.051 2.546.05.605.142.953.276 1.216a3 3 0 0 0 1.311 1.311c.263.134.611.226 1.216.276.617.05 1.41.051 2.546.051h.6V5h-.6c-1.137 0-1.929 0-2.546.051M11.5 5v14h3.6c1.137 0 1.929 0 2.546-.051.605-.05.953-.142 1.216-.276a3 3 0 0 0 1.311-1.311c.134-.263.226-.611.276-1.216.05-.617.051-1.41.051-2.546v-3.2c0-1.137 0-1.929-.051-2.546-.05-.605-.142-.953-.276-1.216a3 3 0 0 0-1.311-1.311c-.263-.134-.611-.226-1.216-.276C17.029 5.001 16.236 5 15.1 5zM5 8.5a1 1 0 0 1 1-1h1a1 1 0 1 1 0 2H6a1 1 0 0 1-1-1M5 12a1 1 0 0 1 1-1h1a1 1 0 1 1 0 2H6a1 1 0 0 1-1-1"
            clipRule="evenodd"
        ></path>
    </svg>
);

interface MessageViewInputProps {
    isLoading?: boolean,
    onSendMessage?: () => void,
    isBotResponding?: boolean,
    stopBotResponse?: () => void,
    text: string,
    setText: (text: string) => void,
    maxHeight?: number,
    minHeight?: number
}

export const ToggleInputModeButton = () => {
    const [open, setOpen] = useState(false);

    return <DropdownMenu open={open} onOpenChange={setOpen}>
        <DropdownMenuTrigger className=""></DropdownMenuTrigger>
        {/**<img className="h-9 m-3 hover:ring-base-100 rounded-full ring-2 ring-base-300 dark:ring-gray-500" src={"TODO_image"} alt="Bordered avatar" onClick={() => {
            setOpen(!open);
        }} />*/}
        <DropdownMenuContent className="w-56 pointer-events-none border-0 shadow-xl">
            <DropdownMenuLabel className="h-6">Chat Settings</DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuLabel>
                hello
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
        </DropdownMenuContent>
    </DropdownMenu>

}

export const MessageInput = forwardRef<
    HTMLTextAreaElement,
    MessageViewInputProps
>(({
    text, setText,
    isLoading = false,
    onSendMessage = () => { },
    isBotResponding = false,
    stopBotResponse = () => { },
    maxHeight = 300,
    minHeight = 30
}, ref: any) => {


    useEffect(() => {
        if (ref?.current) {
            const scrollHeight = ref.current.scrollHeight;
            let updatedHeight = Math.max(minHeight, Math.min(scrollHeight, maxHeight));
            if (text != "") {
                ref.current.style.height = 'inherit';
                ref.current.style.height = `${updatedHeight}px`;
                ref.current.style.overflowY = scrollHeight > maxHeight ? 'auto' : 'hidden';
            }
        }
    }, [ref, text, maxHeight, minHeight]);

    const handleTextChange = (e: any) => {
        setText(e.target.value);
    };

    const handleKeyPress = (e: any) => {
        if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            handleSendMessage();
        }
    };

    const resetInput = () => {
        if (ref?.current) {
            ref.current.style.height = 'auto';
            ref.current.style.height = `${minHeight}px`;
            ref.current.style.overflowY = 'hidden';
        }
    }

    useEffect(() => {
        // if ther is no newline character in the text, reset the input
        if (!text.includes('\n'))
            resetInput();
    }, [text]);

    const handleSendMessage = () => {
        onSendMessage();
        setText(''); // Clear the input text
        resetInput();
    };

    return <div className='flex flex-col content-center items-center justify-center'>
        <Card className="bg-base-200 pr-4 md:px-4 flex items-center rounded-3xl border-0 max-w-[900px] md:min-w-[800px] mb-2" key={"chatListHeader"}>
            <div className="flex pr-4">
                <ToggleInputModeButton />
            </div>
            <Textarea
                value={text}
                placeholder="Send message to Msgmate.io"
                onChange={handleTextChange}
                onKeyPress={handleKeyPress}
                className={`bg-base-200 rounded-2xl text-lg resize-none border-0 focus:border-0 outline-none focus:outline-none shadow-none focus:shadow-none h-[${minHeight}px]`}
                style={{
                    overflowY: 'hidden',
                    height: `${minHeight}px`,
                    maxHeight: `${maxHeight}px`,
                    minHeight: `${minHeight}px`
                }}
                ref={ref}
            />
            <SendMessageButton onClick={handleSendMessage} isLoading={isLoading} />
        </Card>
        <div className='flex grow items-center content-center justify-center text-sm hidden md:flex'>
            msgmate.io uses magic, be sceptical and verify information!
        </div>
    </div>
});


export function MessagesScroll({ 
    chat,
    user,
    messages, 
    hideInput = false 
}: {
    chat: any,
    user: any,
    messages: any,
    hideInput: boolean;
}) {
    const [text, setText] = useState("");

    const scrollRef = useRef<HTMLDivElement>(null)
    const inputRef = useRef<HTMLTextAreaElement>(null)

    const scrollToBottom = () => {
        if (scrollRef.current) {
            scrollRef.current.scrollTop = scrollRef.current.scrollHeight
        }
    }

    useEffect(() => {
        scrollToBottom()
    }, [messages])

    const onSendMessage = async () => {
        const res = await fetch(`/api/v1/chats/${chat.uuid}/messages/send`, {
            method: "POST",
            body: JSON.stringify({
                text: text
            })
        })

        if(res.ok){
            mutate(`/api/v1/chats/${chat.uuid}/messages/list`)
        }
        // TODO: some error or toast if the message sending failed
    }

    const onStopBotResponse = () => {
    }

    return <div className="flex flex-col h-full w-full lg:max-w-[900px] relative">
        <div ref={scrollRef} className="flex flex-col flex-grow gap-2 items-center content-center overflow-y-auto relative pb-4 pt-2">
            {messages && messages.rows.map((message: any) => <MessageItem key={`msg_${message.uuid}`} message={message} chat={chat} selfIsSender={user?.uuid === message.sender_uuid} isBotChat={true} />).reverse()}
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
    const { data: messages } = useSWR(`/api/v1/chats/${chatUUID}/messages/list`, fetcher)
    const { data: user } = useSWR(`/api/v1/user/self`, fetcher)

    return <>
        <div className="flex flex-col h-full w-full content-center items-center">
            {leftPannelCollapsed && <div className="w-full flex items-center content-center justify-left">
                <div className="absolute top-0 mt-2 ml-2 z-40">
                    <CollapseIndicator leftPannelCollapsed={leftPannelCollapsed} onToggleCollapse={onToggleCollapse} />
                </div>
            </div>}
            <div className="absolute left-0 p-2 flex items-center content-center justify-left z-30"></div>
            <MessagesScroll messages={messages} chat={chat} user={user} hideInput={false} />
        </div>
    </>
}
