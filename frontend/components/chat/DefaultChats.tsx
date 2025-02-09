import React, { useContext } from 'react';
import { cn } from '@/lib/utils';
import {
    Card
} from "@/components/Card";
import logoUrl from "@/assets/logo.png"

export const ExploreChatsIcon = () => (
    <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" fill="none" viewBox="0 0 24 24" className="icon-md">
        <path
            fill="currentColor"
            fillRule="evenodd"
            d="M6.75 4.5a2.25 2.25 0 1 0 0 4.5 2.25 2.25 0 0 0 0-4.5M2.5 6.75a4.25 4.25 0 1 1 8.5 0 4.25 4.25 0 0 1-8.5 0M17.25 4.5a2.25 2.25 0 1 0 0 4.5 2.25 2.25 0 0 0 0-4.5M13 6.75a4.25 4.25 0 1 1 8.5 0 4.25 4.25 0 0 1-8.5 0M6.75 15a2.25 2.25 0 1 0 0 4.5 2.25 2.25 0 0 0 0-4.5M2.5 17.25a4.25 4.25 0 1 1 8.5 0 4.25 4.25 0 0 1-8.5 0M17.25 15a2.25 2.25 0 1 0 0 4.5 2.25 2.25 0 0 0 0-4.5M13 17.25a4.25 4.25 0 1 1 8.5 0 4.25 4.25 0 0 1-8.5 0"
            clipRule="evenodd"
        />
    </svg>
);

export function DefaultChats({
    navigateTo,
    defaultBotContact
}: {
    navigateTo: (to: string) => void,
    defaultBotContact: any
}) {

    return <>
        <Card className={cn(
            "bg-base-200 hover:bg-base-300 p-2 border-0")}
            onClick={() => {
                // TODO navigateTo(null, { chat: "createAudio", userName: "hal" })
            }}
        >
            <div className="p-0">
                <div className="flex flex-row text-nowrap text-lg whitespace-nowrap overflow-x-hidden">
                    <div className='flex items-center content-center justify-start'>
                        <img src={logoUrl} className="h-8 w-auto" alt="logo" />
                        <div className="ml-2">Hal Audio Chat</div>
                    </div>
                </div>
            </div>
        </Card>
        <Card className={cn(
            "bg-base-200 hover:bg-base-300 p-2 border-0")}
            onClick={() => {
                if (defaultBotContact) {
                    navigateTo(`/chat/new/${defaultBotContact.contact_token}`)
                }
            }}
        >
            <div className="p-0">
                <div className="flex flex-row text-nowrap text-lg whitespace-nowrap overflow-x-hidden">
                    <div className='flex items-center content-center justify-start'>
                        <img src={logoUrl} className="h-8 w-auto" alt="logo" />
                        <div className="ml-2">Msgmate Hal Bot</div>
                    </div>
                </div>
            </div>
        </Card>
        <Card className={cn(
            "bg-base-200 hover:bg-base-300 p-2 border-0")}
            onClick={() => {
                // TODO navigate(null, { chat: "new" })
                navigateTo(`/chat/new`)
            }}
        >
            <div className="p-0">
                <div className="flex text-nowrap text-lg whitespace-nowrap overflow-x-hidden items-center content-center justify-start">
                    <div className='flex items-center content-center justify-start'>
                        <div className="h-8 w-8 flex items-center content-center justify-center"><ExploreChatsIcon /></div>
                        <div className="ml-2">Bots & Users Overview</div>
                    </div>
                </div>
            </div>
        </Card>
    </>
}