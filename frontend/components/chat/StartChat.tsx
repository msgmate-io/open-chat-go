import React, { useRef, useState } from 'react';
import { Button } from '@/components/Button';
import { BotSelector } from './BotSelector';
import { LoadingSpinner } from '@/components/LoadingSpinner';
import { NewBotChatCard } from './NewBotChat';
import useSWR, { mutate } from "swr"
import { CollapseIndicator } from "@/components/CollapseIndicator";
import { MessageInput } from "@/components/chat/MessageInput";
import { cn } from "@/components/utils";

export function StartChat({
    contactToken,
    leftPannelCollapsed,
    onToggleCollapse,
    navigateTo
}: {
    contactToken: string,
    leftPannelCollapsed: boolean,
    onToggleCollapse: () => void,
    navigateTo: (path: string) => void
}) {
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
        setBotConfig((prev) => ({
            ...prev,
            model
        }))
    }

    return <>
        <div className="flex flex-col h-full w-full content-center items-center">
            <div className="absolute left-0 p-2 flex items-center content-center justify-left z-30">
                {leftPannelCollapsed && <>
                    <CollapseIndicator leftPannelCollapsed={leftPannelCollapsed} onToggleCollapse={onToggleCollapse} />
                    <BotSelector selectedModel={botConfig?.model} setSelectedModel={setSelectedModel} />
                </>}
                {!leftPannelCollapsed && <>
                    <BotSelector selectedModel={botConfig?.model} setSelectedModel={setSelectedModel} />
                </>}
            </div>
            <div className="absolute right-0 p-2 flex items-center content-center justify-left z-10">
                <div>
                    <Button variant={'outline'} className={cn('hover:bg-base-100 hover:text-base-content rounded-3xl', {
                        'bg-base-content text-base-100': advancedOpen,
                    })} onClick={() => {
                        console.log('clicked')
                        setAdvancedOpen(!advancedOpen)
                    }}>Advanced</Button>
                </div>
            </div>
            <div className="flex flex-col h-full w-full lg:max-w-[900px] relativ">
                <div className="flex flex-col flex-grow gap-2 items-center content-center overflow-y-auto justify-center">
                    {contactToken}
                    {/*{isLoading && <LoadingSpinner size={48} className="text-content" />}*/}
                    {true && <>
                        {/*false && <UserChatCard onChangePassword={onChangePassword} userId={userId} isLoading={isLoading} profile={profile} />*/}
                        {true && <>
                            {!advancedOpen && <NewBotChatCard />}
                            {/*advancedOpen && <AdvancedChatSettings botConfig={botConfig} setBotConfig={setBotConfig} />*/}
                        </>}
                    </>}
                </div>
                <MessageInput ref={inputRef} onSendMessage={() => {
                    onCreateChat(text)
                }} text={text} setText={setText} />
            </div>
        </div>
    </>
}


