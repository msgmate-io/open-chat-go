import React, { useContext } from 'react';
import { Button } from "@/components/Button";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuLabel,
    DropdownMenuSeparator
} from "@/components/DropdownMenu";
import { Input } from "@/components/Input";

import { useEffect, useState } from "react";

export function ChatSettings({ 
    chat, 
    open, 
    setOpen, 
    children 
}: {
    chat: any,
    open: boolean,
    setOpen: any,
    children: any
}) {


    const [deleteDialogOpen, setDeleteDialogOpen] = useState(false)
    const [markedForDeletion, setMarkedForDeletion] = useState(false)

    const [extraName, setExtraName] = useState(chat?.settings?.title || "")
    const extraNameChanged = (extraName !== chat?.settings?.title) && extraName !== ""

    useEffect(() => {
        if (markedForDeletion) {
            //dispatch(deleteChat({ chatId: chat?.uuid }))
        }
    }, [markedForDeletion])

    const onSaveExtraTitle = () => {
        /*
        api.chatsSettingsCreate(chat?.uuid, { title: extraName }).then((res) => {
            //dispatch(updateChatSettings({ chatId: chat?.uuid, settings: res }))
        }).catch((error) => {
            toast.error(`Failed to save extra title: ${JSON.stringify(error)}`)
        })*/
    }

    const onResetExtraText = () => {
        setExtraName(chat?.settings?.title || "")
    }


    return <DropdownMenu open={open} onOpenChange={setOpen}>
        {children}
        <DropdownMenuContent className="w-56 pointer-events-none border-0 shadow-xl">
            <DropdownMenuLabel className="h-6">Chat Settings</DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuLabel className="flex flex-row gap-1">
                <Input type="text" value={extraName} onChange={(e) => setExtraName(e.target.value)} placeholder="Extra name" className="h-6 border-0" />
                {(!extraName || !extraNameChanged) && <Button className="h-6 px-1 bg-base-200 hover:bg-accent">
                    ✍️
                </Button>}
                {(extraName && extraNameChanged) && <Button className="h-6 px-1 bg-base-200 hover:bg-accent" onClick={onSaveExtraTitle}>
                    ✅
                </Button>}
                {chat?.settings?.title && !extraNameChanged && <Button className="h-6 px-1 bg-base-200 hover:bg-accent">
                    🪣
                </Button>}
                {!chat?.settings?.title && !extraNameChanged && <Button className="h-6 px-1 bg-base-200 hover:bg-accent">
                    🕳️
                </Button>}
                {extraNameChanged && <Button className="h-6 px-1 bg-base-200 hover:bg-accent" onClick={onResetExtraText}>
                    ↩
                </Button>}
            </DropdownMenuLabel>
            <DropdownMenuLabel>
                {/**<ViewChatJsonModal chat={chat} />**/}
            </DropdownMenuLabel>
            <DropdownMenuLabel>
                <Button variant="ghost" className="h-6 w-full" onClick={() => {
                    /**
                     * navigate(null, {
                    chat: chat.uuid,
                    chatType: "audio"
                     * })
                     */
                    // TODO navigate
                }}>Start Audio Chat</Button>
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
            {chat?.partner?.is_bot && <DropdownMenuLabel>
                {/**<DeleteChatModal chat={chat} dialogOpen={deleteDialogOpen} setDialogOpen={setDeleteDialogOpen} setMarkedForDeletion={setMarkedForDeletion} />**/}
            </DropdownMenuLabel>}
            <DropdownMenuSeparator />
        </DropdownMenuContent>
    </DropdownMenu>
}

