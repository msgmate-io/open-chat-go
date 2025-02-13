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
import { create } from "zustand";
import { devtools } from "zustand/middleware";
import { persist } from "zustand/middleware";
import { cookiesStorage } from "@/lib/utils";
import { CollapseIndicator } from "../CollapseIndicator";

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
    const { isSm: biggerThanSm } = useBreakpoint('sm');
    const [, setRightCollapsed] = useState(false);

    // TODO load the correct default layout from the cookie!
    const defaultLayoutLeft = null
    const defaultLayoutRight = null
    const mobileConfig = useMobileConfig(chatUUID, defaultLayoutLeft, defaultLayoutRight);
    const desktopConfig = useDesktopConfig(defaultLayoutLeft, defaultLayoutRight);

    console.log("chatId", chatUUID);

    useEffect(() => {
        if (!biggerThanSm) {
            if (chatUUID) {
                leftPannelRef.current.collapse();
                setLeftCollapsed(true);
            } else if (!chatUUID) {
                rightPannelRef.current.collapse();
                setRightCollapsed(true);
            }
        }
    }, [biggerThanSm, chatUUID, leftPannelRef, rightPannelRef]);

    const onLeftPannelCollapseChanged = () => {
        setLeftCollapsed(leftPannelRef.current.isCollapsed());
    };

    const onToggleCollapse = () => {
        setLeftCollapsed(!leftPannelRef.current.isCollapsed());
    };

    const onLayout = (sizes: any) => {
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
                <div className="flex flex-col h-full bg-secondary relative">
                    {left}
                </div>
            </ResizablePanel>
            {biggerThanSm && <ResizableHandle id="resize-handle" withHandle />}
            {/**biggerThanSm && <CollapseIndicator leftPannelCollapsed={leftPannelRef.current?.isCollapsed()} onToggleCollapse={onToggleCollapse} />**/}
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

interface SidePanelState {
  isCollapsed: boolean
  panelRef: React.MutableRefObject<any> | null
  setCollapsed: (collapsed: boolean) => void
  setPanelRef: (ref: React.MutableRefObject<any>) => void
  toggle: () => void
}

export const useSidePanelCollapse = create<SidePanelState>()(
  devtools(
    persist(
      (set, get) => ({
        isCollapsed: false,
        panelRef: null,
        setPanelRef: (ref) => set({ panelRef: ref }),
        setCollapsed: (collapsed) => {
          const { panelRef } = get();
          if (panelRef?.current) {
            if (collapsed) {
              panelRef.current.collapse();
            } else {
              panelRef.current.expand();
            }
          }
          set({ isCollapsed: collapsed });
        },
        toggle: () => {
          const { isCollapsed, panelRef } = get();
          if (panelRef?.current) {
            if (isCollapsed) {
              panelRef.current.expand();
            } else {
              panelRef.current.collapse();
            }
          }
          set({ isCollapsed: !isCollapsed });
        },
      }),
      {
        name: 'side-panel-store',
        storage: cookiesStorage<{ isCollapsed: boolean }>(),
        partialize: (state) => ({ isCollapsed: state.isCollapsed }), // Only persist isCollapsed state
      },
    ),
  ),
)

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
    chatUUID=null,
    navigateTo
}: {
    children: ReactNode,
    chatUUID: string | null,
    navigateTo: (to: string) => void
}) {
    const leftPannelCollapsed = useSidePanelCollapse(state => state.isCollapsed);
    const setLeftCollapsed = useSidePanelCollapse(state => state.setCollapsed);
    const leftPannelRef = useRef<any>(null);
    const rightPannelRef = useRef<any>(null);
    const setPanelRef = useSidePanelCollapse(state => state.setPanelRef);

    const onToggleCollapse = useSidePanelCollapse(state => state.toggle);

    useEffect(() => {
        setPanelRef(leftPannelRef);
    }, [leftPannelRef]);

    return <>
        <div className="flex h-screen">
            <ResizableChatLayout
                chatUUID={chatUUID}
                leftPannelRef={leftPannelRef}
                rightPannelRef={rightPannelRef}
                setLeftCollapsed={setLeftCollapsed}
                left={<ChatsList chatUUID={chatUUID} 
                    leftPannelCollapsed={leftPannelCollapsed} 
                    onToggleCollapse={onToggleCollapse} 
                    navigateTo={navigateTo} 
                />}
                right={children}
            />
        </div>
    </>

}