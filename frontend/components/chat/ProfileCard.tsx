import React from 'react';
import { ThemeSelector } from "@/components/chat/ThemeSelector";
import {
    Card,
} from "@/components/Card";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuSeparator,
    DropdownMenuShortcut,
    DropdownMenuTrigger,
} from "@/components/DropdownMenu";
import image from "@/assets/logo.png"
import { Cookies } from 'typescript-cookie';

function ProfileCardButton() {
    const profile = {
        first_name: "John",
        second_name: "Doe"
    }

    return <>
        <DropdownMenuTrigger asChild>
            <Card className="border-0 bg-base-200 hover:bg-base-300 p-0 flex" key={"chatListHeader"}>
                <div className="flex">
                    <img src={image} className="h-12" alt="logo" />
                </div>
                <div className="flex flex-grow items-center content-center justify-start pr-2">
                    <div className="p-2 flex flex-grow">{profile?.first_name} {profile?.second_name}</div>
                    <div>✍️</div>
                </div>
            </Card>
        </DropdownMenuTrigger>
    </>
}

export function ProfileCard({ navigateTo }: { navigateTo: (path: string) => void }) {
    const onLogout = () => {
        // TODO: logout
        fetch("/api/v1/user/logout", {
            method: "POST",
        }).then((res) => {
            console.log(res)
            if (res.ok) {
                navigateTo('/')
                Cookies.remove("is_authorized")
            }
        })
    }
    return <div className="shadow-xl">
        <DropdownMenu>
            <ProfileCardButton />
            <DropdownMenuContent className="w-56 border-0">
                <DropdownMenuLabel>My Account</DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem onClick={() => {
                    navigateTo('/')
                }}>Home Page</DropdownMenuItem>
                <DropdownMenuItem onClick={() => {
                    navigateTo('/nodes')
                }}>Nodes</DropdownMenuItem>
                <DropdownMenuItem>Docs</DropdownMenuItem>
                <DropdownMenuLabel><ThemeSelector /></DropdownMenuLabel>
                <DropdownMenuItem disabled>API</DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem onClick={onLogout}>
                    Log out
                    <DropdownMenuShortcut>⇧⌘Q</DropdownMenuShortcut>
                </DropdownMenuItem>
            </DropdownMenuContent>
        </DropdownMenu>
    </div>
}