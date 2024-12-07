"use client"

import { useState, useRef, useEffect, forwardRef, useImperativeHandle } from "react";
import { ChatsList } from "@/components/chat/ChatsList";
import { useMediaQuery } from 'react-responsive';
import { Cookies } from "typescript-cookie";
import {
    ResizableHandle,
    ResizablePanel,
    ResizablePanelGroup
} from "@/components/chat/Resizable";


const screens = {
    sm: "640px",
    // => @media (min-width: 640px) { ... }
    md: "768px",
    // => @media (min-width: 768px) { ... }
    lg: "1024px",
    // => @media (min-width: 1024px) { ... }
    xl: "1280px",
    // => @media (min-width: 1280px) { ... }
    "2xl": "1536px",
    // => @media (min-width: 1536px) { ... }
}

type BreakpointKey = keyof typeof breakpoints;

const breakpoints = screens;
  

export function useBreakpoint<K extends BreakpointKey>(breakpointKey: K) {
    const [queryResult, setQueryResult] = useState(true);
    // https://stackoverflow.com/a/71098593
    const bool = useMediaQuery({
      query: `(min-width: ${breakpoints[breakpointKey]})`,
    });
  
    const capitalizedKey = breakpointKey[0].toUpperCase() + breakpointKey.substring(1);
    type Key = `is${Capitalize<K>}`;
    useEffect(() => {
      // Layout sizes can only be determined client-side
      // we return 'false' by default and just set it after hidration to avoid and SSR issues
      setQueryResult(bool);
    }, []);
  
    useEffect(() => {
      if (queryResult !== bool) {
        setQueryResult(bool);
      }
    }, [bool]);
    return {
      [`is${capitalizedKey}`]: queryResult,
    } as Record<Key, boolean>;
  }
  
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
    left,
    right,
    leftPannelRef,
    rightPannelRef,
    setLeftCollapsed
}: {
    left: any,
    right: any,
    leftPannelRef: any,
    rightPannelRef: any,
    setLeftCollapsed: any
}, ref) => {
    const frontend = null
    const chatId = "abc"
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
    const mobileConfig = useMobileConfig(chatId, defaultLayoutLeft, defaultLayoutRight);
    const desktopConfig = useDesktopConfig(defaultLayoutLeft, defaultLayoutRight);

    console.log("chatId", chatId);
    console.log("FRONTEND", frontend);

    useEffect(() => {
        if (!biggerThanSm) {
            // layout changed to mobile
            if (chatId) {
                // chat selected -> hide left panel
                leftPannelRef.current.collapse();
                setLeftCollapsed(true);
            } else if (!chatId) {
                // chat not selected -> hide right panel
                rightPannelRef.current.collapse();
                setRightCollapsed(true);
            }
        }
    }, [biggerThanSm, chatId, leftPannelRef, rightPannelRef]);

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


export function ChatBase() {
    const chatId = "abc"
    const userId = "abc"

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
                leftPannelRef={leftPannelRef}
                rightPannelRef={rightPannelRef}
                setLeftCollapsed={setLeftCollapsed}
                left={<ChatsList leftPannelCollapsed={leftPannelCollapsed} onToggleCollapse={onToggleCollapse} />}
                right={<>
                    Hello there
                    {/*(chatId && !(chatMessageViews.indexOf(chatId) !== -1)) && <MessagesView
                        chatId={chatId}
                        leftPannelCollapsed={leftPannelCollapsed}
                        onToggleCollapse={onToggleCollapse} />*/}
                    {/*chatId === "new" && <NewChatOverview leftPannelCollapsed={leftPannelCollapsed} onToggleCollapse={onToggleCollapse} />*/}
                    {/*chatId === "create" && <StartChatCard initUserName={null} userId={userId} leftPannelCollapsed={leftPannelCollapsed} onToggleCollapse={onToggleCollapse} />*/}
                    {/*chatId === "createAudio" && <CreateAudioChatCard />*/}
                </>}
            />
        </div>
    </>

}