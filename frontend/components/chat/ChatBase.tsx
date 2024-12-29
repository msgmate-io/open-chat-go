"use client"

import { useState, useRef, useEffect, forwardRef, useImperativeHandle, ReactNode } from "react";
import { ChatsList } from "@/components/chat/ChatsList";
import { useBreakpoint } from "@/components/utils";
import { Cookies } from "typescript-cookie";
import {
    ResizableHandle,
    ResizablePanel,
    ResizablePanelGroup
} from "@/components/chat/Resizable";
  
function useMobileConfig(chatId: any, defaultLeftSize = null, defaultRightSize = null) {
    return {
        left: {
            minSize: chatId ? 0 : 100,
            defaultSize: defaultLeftSize || (chatId ? 0 : 100),
            collapsedSize: 0,
            collapsible: true,
        },
        right: {
            minSize: !chatId ? 0 : 100,
            defaultSize: defaultRightSize || (!chatId ? 0 : 100),
            collapsedSize: 0,
            collapsible: true,
        },
    };
}

function useDesktopConfig(defaultLeftSize = null, defaultRightSize = null) {
    return {
        left: {
            minSize: 18,
            defaultSize: defaultLeftSize || 25,
            collapsedSize: 0,
            collapsible: true,
        },
        right: {
            minSize: 60,
            defaultSize: defaultRightSize || 75,
            collapsible: true,
            collapsedSize: 60,
        },
    };
}

export const ResizableChatLayout = forwardRef(({
    chatUUID,
    left,
    right,
    leftPannelRef,
    rightPannelRef,
    setLeftCollapsed
}: {
    chatUUID: string | null,
    left: any,
    right: any,
    leftPannelRef: any,
    rightPannelRef: any,
    setLeftCollapsed: any
}, ref) => {
    const frontend = null
    const { isSm: biggerThanSm } = useBreakpoint('sm');
    const [, setRightCollapsed] = useState(false);

    const layout = frontend?.resizableLayout;

    let defaultLayout;
    if (layout) {
        console.log("DEF layout", layout);
        defaultLayout = JSON.parse(layout);
    }
    const defaultLayoutLeft = defaultLayout ? defaultLayout[0] : null;
    const defaultLayoutRight = defaultLayout ? defaultLayout[1] : null;
    const mobileConfig = useMobileConfig(chatUUID, defaultLayoutLeft, defaultLayoutRight);
    const desktopConfig = useDesktopConfig(defaultLayoutLeft, defaultLayoutRight);

    console.log("chatId", chatUUID);
    console.log("FRONTEND", frontend);

    useEffect(() => {
        if (!biggerThanSm) {
            // layout changed to mobile
            if (chatUUID) {
                // chat selected -> hide left panel
                leftPannelRef.current.collapse();
                setLeftCollapsed(true);
            } else if (!chatUUID) {
                // chat not selected -> hide right panel
                rightPannelRef.current.collapse();
                setRightCollapsed(true);
            }
        }
    }, [biggerThanSm, chatUUID, leftPannelRef, rightPannelRef]);

    const onLeftPannelCollapseChanged = () => {
        setLeftCollapsed(leftPannelRef.current.isCollapsed());
    };

    const onLayout = (sizes) => {
        console.log("onLayout", sizes);
        Cookies.set('react-resizable-panels-layout', JSON.stringify(sizes));
    };

    useImperativeHandle(ref, () => ({
        leftPannelRef: leftPannelRef.current,
        rightPannelRef: rightPannelRef.current,
    }));

    return (
        <ResizablePanelGroup
            direction="horizontal"
            className=""
            id="group"
            onLayout={onLayout}
        >
            <ResizablePanel
                onCollapse={onLeftPannelCollapseChanged}
                onExpand={onLeftPannelCollapseChanged}
                ref={leftPannelRef}
                id="left-panel"
                {...(biggerThanSm ? desktopConfig.left : mobileConfig.left)}
                order={1}
            >
                <div className="flex flex-col h-full bg-base-200 relative">
                    {left}
                </div>
            </ResizablePanel>
            {biggerThanSm && <ResizableHandle id="resize-handle" withHandle />}
            {/*biggerThanSm && <CollapseIndicator isCollapsed={leftPannelRef.current?.isCollapsed()} onToggle={onToggleCollapse} />*/}
            <ResizablePanel
                ref={rightPannelRef}
                id="right-panel"
                {...(biggerThanSm ? desktopConfig.right : mobileConfig.right)}
                order={2}
            >
                <div className="flex h-full items-center justify-center content-center relative">
                    {right}
                </div>
            </ResizablePanel>
        </ResizablePanelGroup>
    );
});


//                   Hello there
//                   {/*(chatId && !(chatMessageViews.indexOf(chatId) !== -1)) && <MessagesView
//                        chatId={chatId}
//                       leftPannelCollapsed={leftPannelCollapsed}
//                      onToggleCollapse={onToggleCollapse} />*/}
//                    {/*chatId === "new" && <NewChatOverview leftPannelCollapsed={leftPannelCollapsed} onToggleCollapse={onToggleCollapse} />*/}
//                   {/*chatId === "create" && <StartChatCard initUserName={null} userId={userId} leftPannelCollapsed={leftPannelCollapsed} onToggleCollapse={onToggleCollapse} />*/}
//                    {/*chatId === "createAudio" && <CreateAudioChatCard />*/}
export function ChatBase({
    children,
    chatUUID=null
}: {
    children: ReactNode,
    chatUUID: string | null
}) {
    const [leftPannelCollapsed, setLeftCollapsed] = useState(false);
    const leftPannelRef = useRef<any>(null);
    const rightPannelRef = useRef<any>(null);

    const onToggleCollapse = () => {
        const isCollapsed = leftPannelRef.current.isCollapsed();
        if (isCollapsed) {
            leftPannelRef.current.expand();
        } else {
            leftPannelRef.current.collapse();
        }
        setLeftCollapsed(!isCollapsed);
    };


    return <>
        <div className="flex h-screen">
            <ResizableChatLayout
                chatUUID={chatUUID}
                leftPannelRef={leftPannelRef}
                rightPannelRef={rightPannelRef}
                setLeftCollapsed={setLeftCollapsed}
                left={<ChatsList leftPannelCollapsed={leftPannelCollapsed} onToggleCollapse={onToggleCollapse} />}
                right={children}
            />
        </div>
    </>

}