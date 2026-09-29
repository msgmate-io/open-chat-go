"use client"

import { forwardRef, useImperativeHandle, type ReactNode } from "react"
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@open-chat-go/ui"
import { Cookies } from "typescript-cookie"
import { create } from "zustand"
import { devtools, persist } from "zustand/middleware"
import { cookiesStorage } from "../lib/utils"

export function isMobileViewport() {
    if (typeof window === "undefined") {
        return false
    }
    return window.matchMedia("(max-width: 640px)").matches
}

export function useDesktopConfig(defaultLeftSize: number | null = null, defaultRightSize: number | null = null) {
    return {
        left: {
            minSize: 18,
            defaultSize: defaultLeftSize ?? 25,
            collapsedSize: 0,
            collapsible: true,
        },
        right: {
            minSize: 60,
            defaultSize: defaultRightSize ?? 75,
            collapsible: true,
            collapsedSize: 60,
        },
    };
}

export function readSavedDesktopLayout(): { left: number; right: number } | null {
    if (typeof window === "undefined") {
        return null
    }

    const raw = Cookies.get("react-resizable-panels-layout")
    if (typeof raw !== "string" || !raw) {
        return null
    }

    try {
        const parsed = JSON.parse(raw)
        if (!Array.isArray(parsed) || parsed.length < 2) {
            return null
        }

        const left = Number(parsed[0])
        const right = Number(parsed[1])
        if (!Number.isFinite(left) || !Number.isFinite(right)) {
            return null
        }

        if (left < 0 || right < 0 || left + right <= 0) {
            return null
        }

        const total = left + right
        return {
            left: (left / total) * 100,
            right: (right / total) * 100,
        }
    } catch {
        return null
    }
}

export function StaticDesktopLayout({
    left,
    right,
    leftSize,
    rightSize,
    sidebarClassName = "chat-sidebar",
    mainClassName = "chat-main-pane",
}: {
    left: ReactNode,
    right: ReactNode,
    leftSize: number,
    rightSize: number,
    sidebarClassName?: string,
    mainClassName?: string,
}) {
    return (
        <div className="flex h-full w-full">
            <div className="h-full shrink-0" style={{ flexBasis: `${leftSize}%` }}>
                <div className={sidebarClassName}>
                    {left}
                </div>
            </div>
            <div className="h-full w-full min-w-0" style={{ flexBasis: `${rightSize}%` }}>
                <div className={mainClassName}>
                    {right}
                </div>
            </div>
        </div>
    )
}

export const ResizableTilingLayout = forwardRef(({
    left,
    right,
    groupRef,
    leftPannelRef,
    rightPannelRef,
    setLeftCollapsed,
    defaultLayoutLeft,
    defaultLayoutRight,
    sidebarClassName = "chat-sidebar",
    mainClassName = "chat-main-pane",
}: {
    left: ReactNode,
    right: ReactNode,
    groupRef: React.RefObject<any>,
    leftPannelRef: React.RefObject<{ collapse: () => void; expand: () => void; isCollapsed: () => boolean } | null>,
    rightPannelRef: React.RefObject<{ collapse: () => void; expand: () => void } | null>,
    setLeftCollapsed: (collapsed: boolean) => void
    defaultLayoutLeft: number | null,
    defaultLayoutRight: number | null,
    sidebarClassName?: string,
    mainClassName?: string,
}, ref) => {
    const desktopConfig = useDesktopConfig(defaultLayoutLeft, defaultLayoutRight);

    const onLeftPannelCollapseChanged = () => {
        try {
            setLeftCollapsed(leftPannelRef.current?.isCollapsed() ?? false);
        } catch {
            // Panel group may not be registered yet during initial mount.
        }
    };

    const onLayout = (sizes: number[]) => {
        Cookies.set('react-resizable-panels-layout', JSON.stringify(sizes));
    };

    useImperativeHandle(ref, () => ({
        leftPannelRef: leftPannelRef.current,
        rightPannelRef: rightPannelRef.current,
    }));

    return (
        <ResizablePanelGroup
            direction="horizontal"
            className="h-full w-full"
            id="group"
            groupRef={groupRef}
            onLayout={onLayout}
        >
            <ResizablePanel
                onCollapse={onLeftPannelCollapseChanged}
                onExpand={onLeftPannelCollapseChanged}
                ref={leftPannelRef as any}
                id="left-panel"
                {...desktopConfig.left}
            >
                <div className={sidebarClassName}>
                    {left}
                </div>
            </ResizablePanel>
            <ResizableHandle id="resize-handle" withHandle />
            <ResizablePanel
                ref={rightPannelRef as any}
                id="right-panel"
                {...desktopConfig.right}
            >
                <div className={mainClassName}>
                    {right}
                </div>
            </ResizablePanel>
        </ResizablePanelGroup>
    );
});

interface SidePanelState {
  isCollapsed: boolean
  panelRef: React.MutableRefObject<{ collapse: () => void; expand: () => void; isCollapsed: () => boolean } | null> | null
  setCollapsed: (collapsed: boolean) => void
  setPanelRef: (ref: React.MutableRefObject<{ collapse: () => void; expand: () => void; isCollapsed: () => boolean } | null>) => void
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
            try {
              if (collapsed) {
                panelRef.current.collapse();
              } else {
                panelRef.current.expand();
              }
            } catch {
              set({ isCollapsed: collapsed });
              return;
            }
          }
          set({ isCollapsed: collapsed });
        },
        toggle: () => {
          const { isCollapsed, panelRef } = get();
          if (panelRef?.current) {
            try {
              if (isCollapsed) {
                panelRef.current.expand();
              } else {
                panelRef.current.collapse();
              }
            } catch {
              return;
            }
          }
          set({ isCollapsed: !isCollapsed });
        },
      }),
      {
        name: 'side-panel-store',
        storage: cookiesStorage<{ isCollapsed: boolean }>(),
        partialize: (state) => ({ isCollapsed: state.isCollapsed }),
      },
    ),
  ),
)
