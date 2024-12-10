import React from 'react';
import { Badge } from "@/components/Badge";

export function OnlineIndicator({
    isOnline
}: {
    isOnline: boolean
}) {
    return isOnline ?
        <Badge className="bg-transparent flex items-center content-center justify-center h-6 w-6 hover:bg-transparent">🟢</Badge>
        : <Badge className="bg-transparent flex items-center content-center justify-center h-6 w-6 hover:bg-transparent">🔴</Badge>
}