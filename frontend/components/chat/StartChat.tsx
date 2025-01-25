import React, { useRef, useState } from 'react';
import { Button } from '@/components/Button';
import { BotSelector } from './BotSelector';
import { LoadingSpinner } from '@/components/LoadingSpinner';
import { NewBotChatCard } from './NewBotChat';
import { CollapseIndicator } from "@/components/CollapseIndicator";
import { MessageInput } from "@/components/chat/MessageInput";
import { cn } from "@/components/utils";
import { useSidePanelCollapse } from "@/components/chat/ChatBase";
import useSWR from "swr";
import { fetcher } from "@/lib/utils";

export function StartChat({
    contactToken,
    navigateTo
}: {
    contactToken: string,
    navigateTo: (path: string) => void
}) {

    const { data: contact } = useSWR(`/api/v1/contacts/${contactToken}`, fetcher)
    const leftPannelCollapsed = useSidePanelCollapse(state => state.isCollapsed);
    const onToggleCollapse = useSidePanelCollapse(state => state.toggle);
    const [advancedOpen, setAdvancedOpen] = useState(false)

    const [text, setText] = useState("");

    // Bot config
    const [botConfig, setBotConfig] = useState({
        model: "meta-llama/Llama-3.3-70B-Instruct-Turbo",
        endpoint: "https://api.deepinfra.com/v1/openai",
        context: 5,
        systemPrompt: "Your are the advanced AI Agent, Hal. Here to fulfill any of the users requests.",
    })

    const inputRef = useRef<HTMLInputElement>(null)

    const onCreateChat = (text: string) => {
        // TODO: Implement
        fetch(`/api/v1/chats/create`, {
            method: 'POST',
            body: JSON.stringify({
                contact_token: contactToken,
                first_message: text,
                shared_config: botConfig
            })
        }).then(res => res.json()).then(data => {
            console.log("data", data)
            // mutate(`/api/v1/chats/list`)
            navigateTo(`/chat/${data.uuid}`)
        })
    }

    const setSelectedModel = (model: string) => {
        const contactConfig = contact?.profile_data?.models?.find(m => m.title === model)
        console.log("contactConfig", contactConfig)
        setBotConfig((prev) => ({
            ...prev,
            model,
            ...contactConfig?.configuration
        }))
    }

    return <>
        <div className="flex flex-col h-full w-full content-center items-center">
            <div className="absolute left-0 p-2 flex items-center content-center justify-left z-30">
                {leftPannelCollapsed && <>
                    <CollapseIndicator leftPannelCollapsed={leftPannelCollapsed} onToggleCollapse={onToggleCollapse} />
                    <BotSelector contact={contact} selectedModel={botConfig?.model} setSelectedModel={setSelectedModel} />
                </>}
                {!leftPannelCollapsed && <>
                    <BotSelector contact={contact} selectedModel={botConfig?.model} setSelectedModel={setSelectedModel} />
                </>}
            </div>
            <div className="absolute right-0 p-2 flex items-center content-center justify-left z-10">
                    {/**TODO advanced settings */}
            </div>
            <div className="flex flex-col h-full w-full lg:max-w-[900px] relativ">
                <div className="flex flex-col flex-grow gap-2 items-center content-center overflow-y-auto justify-center">
                    {/*{isLoading && <LoadingSpinner size={48} className="text-content" />}*/}
                    <NewBotChatCard startChat={onCreateChat}/>
                </div>
                <MessageInput ref={inputRef} contact={contact} botConfig={botConfig} onSendMessage={() => {
                    onCreateChat(text)
                }} text={text} setText={setText} />
            </div>
        </div>
    </>
}


