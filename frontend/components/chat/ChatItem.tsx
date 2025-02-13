import { useState, useContext } from "react";
import { cn } from "@/components/utils";
import { OnlineIndicator } from "@/components/chat/OnlineIndicator";
import { UnreadBadge } from "@/components/chat/UnreadBadge";
import { ChatSettings } from "@/components/chat/ChatSettings";
import { DropdownMenuTrigger } from "@/components/DropdownMenu";
import {
    Card,
    CardDescription,
    CardHeader,
} from "@/components/Card";
import { Button } from "@/components/Button";

function DotsHorizontal({ colorClass = "text-black-200" }) {
    return (
        <svg className={colorClass} width="20" height="20" viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg">
            <path d="M5 10C5 10.8284 4.32843 11.5 3.5 11.5C2.67157 11.5 2 10.8284 2 10C2 9.17157 2.67157 8.5 3.5 8.5C4.32843 8.5 5 9.17157 5 10ZM11.5 10C11.5 10.8284 10.8284 11.5 10 11.5C9.17157 11.5 8.5 10.8284 8.5 10C8.5 9.17157 9.17157 8.5 10 8.5C10.8284 8.5 11.5 9.17157 11.5 10ZM17.5 11.5C18.3284 11.5 19 10.8284 19 10C19 9.17157 18.3284 8.5 17.5 8.5C16.6716 8.5 16 9.17157 16 10C16 10.8284 16.6716 11.5 17.5 11.5Z" fill="currentColor" fillRule="evenodd" clipRule="evenodd">
            </path>
        </svg>
    )
}

export function ChatItem({ chat, isSelected = false }: {
    chat: any,
    isSelected?: boolean
}) {
    const [settingsOpen, setSettingsOpen] = useState(false)

    return (
        <ChatSettings chat={chat} open={settingsOpen} setOpen={setSettingsOpen}>
            <Card className={cn(
                "bg-secondary hover:bg-accent p-0 border-0",
                settingsOpen && "pointer-events-none hover:bg-accent",
                isSelected && "bg-accent"
            )} key={chat.uuid} onClick={() => {
                if (!settingsOpen) {
                    // TODO: navigate
                }
            }}>
                <CardHeader className="p-0 px-2">
                    <CardDescription className="flex flex-row">
                        <div className="flex flex-grow">{chat?.settings?.title ? chat?.settings?.title : `${chat.partner.first_name} ${chat.partner.second_name}`}</div>
                        <div className="relative h-full">
                            <div className="absolute right-0 flex flex-row">
                                {/* <UnreadBadge unreadCount={0} /> TODO add the actual count */}
                                {!chat?.partner?.is_bot && <OnlineIndicator isOnline={chat?.partner?.is_online} />}
                                <Button className="flex h-6 w-6 p-0 content-center items-center justify-center bg-transparent shadow-none hover:bg-base-200" onClick={(e) => {
                                    setSettingsOpen(!settingsOpen)
                                    e.stopPropagation()
                                }}><DotsHorizontal /></Button>
                            </div>
                        </div>
                    </CardDescription>
                    <CardDescription className="flex text-nowrap text-lg whitespace-nowrap overflow-x-hidden">{chat.newest_message.text}</CardDescription>
                </CardHeader>
            </Card>
            <DropdownMenuTrigger className="h-0 bg-error z-10 display-none"></DropdownMenuTrigger>
        </ChatSettings>
    )
}

export function ChatItemCompact({ chat = null, isSelected = false, navigateTo = (to) => {} }: {
    chat: any,
    isSelected?: boolean,
    navigateTo: (to: string) => void
}) {
    const [settingsOpen, setSettingsOpen] = useState(false)


    //const content = chat?.settings?.title ? chat?.settings?.title : (chat.partner.is_bot ? chat.newest_message.text : `${chat.partner.first_name} ${chat.partner.second_name}`)
    var content = chat?.latest_message?.text || "No messages yet"

    return (
        <ChatSettings chat={chat} open={settingsOpen} setOpen={setSettingsOpen}>
            <Card className={cn(
                "bg-secondary hover:bg-accent p-0 border-0",
                settingsOpen && "pointer-events-none hover:bg-accent",
                isSelected && "bg-accent"
            )}
                key={chat?.uuid} onClick={() => {
                    if (!settingsOpen) {
                        navigateTo(`/chat/${chat?.uuid}`)
                    }
                }}>
                <div className="p-[5px] px-2">
                    <div className="">
                        <div className='flex text-nowrap text-lg whitespace-nowrap overflow-x-hidden'>
                            {content}
                            <div className="absolute right-4 flex flex-row">
                                {/* <UnreadBadge unreadCount={0} /> TODO add the actual count */}
                                {/* {!chat?.partner?.is_bot && <OnlineIndicator isOnline={false} />} */}
                                <Button variant="ghost" className="flex h-4 w-6 p-0 content-center items-center justify-center bg-transparent shadow-none hover:bg-base-200" onClick={(e) => {
                                    setSettingsOpen(!settingsOpen)
                                    e.stopPropagation()
                                }}><DotsHorizontal /></Button>
                            </div>
                        </div>
                    </div>
                </div>
            </Card>
            <DropdownMenuTrigger className="h-0 bg-error z-10 display-none"></DropdownMenuTrigger>
        </ChatSettings>
    )
}
