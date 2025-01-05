import React from 'react';
import { Badge } from "@/components/Badge"

export function UnreadBadge({
    unreadCount
}: {
    unreadCount: number
}) {
    return unreadCount > 0 ?
        <Badge className="bg-transparent flex flex-row items-center content-center justify-center text-black h-6 w-10 px-0 hover:bg-transparent">{`${unreadCount}x📮`}</Badge>
        : <></>
}