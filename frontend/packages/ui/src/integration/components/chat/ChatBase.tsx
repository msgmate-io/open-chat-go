"use client"

import { useRef, useEffect, useState, type ReactNode } from "react"
import { Button, ChatsList } from "@open-chat-go/ui"
import { PanelLeft } from "lucide-react"
import { usePageContext } from "vike-react/usePageContext"
import { ConnectedThemeSelector } from "../ConnectedThemeSelector"
import { IntegrationsNav } from "../integrations/IntegrationsNav"
import {
    isMobileViewport,
    readSavedDesktopLayout,
    ResizableTilingLayout,
    StaticDesktopLayout,
    useSidePanelCollapse,
} from "../ResizableTilingLayout"
import logoUrl from "../../assets/logo.png?inline"
import { getPageMetadata } from "../../lib/page-metadata"

export { useSidePanelCollapse }

export function ChatBase({
    children,
    chatUUID=null,
    navigateTo,
    mobileViewMode = "content",
    sidebarTitle,
    sidebar,
    sidebarTopSection,
    integrationName,
}: {
    children: ReactNode,
    chatUUID: string | null,
    navigateTo: (to: string) => void,
    mobileViewMode?: "list" | "content",
    sidebarTitle?: string,
    sidebar?: ReactNode,
    sidebarTopSection?: ReactNode,
    integrationName?: string | null,
}) {
    const leftPannelCollapsed = useSidePanelCollapse(state => state.isCollapsed);
    const setLeftCollapsed = useSidePanelCollapse(state => state.setCollapsed);
    const leftPannelRef = useRef<{ collapse: () => void; expand: () => void; isCollapsed: () => boolean } | null>(null);
    const rightPannelRef = useRef<{ collapse: () => void; expand: () => void } | null>(null);
    const groupRef = useRef<any>(null)
    const setPanelRef = useSidePanelCollapse(state => state.setPanelRef);
    const onToggleCollapse = useSidePanelCollapse(state => state.toggle);
    const [isMobile, setIsMobile] = useState(() => isMobileViewport())
    const [hasMounted, setHasMounted] = useState(false)
    const [desktopLayout, setDesktopLayout] = useState<{ left: number; right: number }>({ left: 25, right: 75 })
    const pageContext = usePageContext()
    const activePath = pageContext?.urlPathname || (typeof window !== "undefined" ? window.location.pathname : "")
    const isIntegrationMode = integrationName !== undefined
    const resolvedSidebarTitle = isIntegrationMode
        ? (integrationName ?? "Integrations")
        : sidebarTitle || getPageMetadata(typeof window !== "undefined" ? window.location.pathname : "").sidebarTitle

    const renderSidebar = ({
        collapsed,
        toggle,
        hideToggle = false,
    }: {
        collapsed: boolean
        toggle: () => void
        hideToggle?: boolean
    }) => {
        if (sidebar) {
            return sidebar
        }
        return (
            <ChatsList
                chatUUID={chatUUID}
                leftPannelCollapsed={collapsed}
                onToggleCollapse={toggle}
                navigateTo={navigateTo}
                themeSelector={<ConnectedThemeSelector />}
                avatarSrc={logoUrl}
                hideCollapseToggle={hideToggle}
                title={resolvedSidebarTitle}
                topSection={
                    sidebarTopSection ?? (isIntegrationMode ? (
                        <IntegrationsNav
                            integrationName={integrationName ?? null}
                            activePath={activePath}
                            navigateTo={navigateTo}
                        />
                    ) : undefined)
                }
                showDefaultChats={!isIntegrationMode}
                showChats={!isIntegrationMode}
            />
        )
    }

    useEffect(() => {
        const savedLayout = readSavedDesktopLayout()
        if (savedLayout) {
            setDesktopLayout(savedLayout)
        }
        setHasMounted(true)
    }, [])

    useEffect(() => {
        if (typeof window === "undefined") {
            return
        }
        const mediaQuery = window.matchMedia("(max-width: 640px)")
        const onChange = (event: MediaQueryListEvent) => {
            setIsMobile(event.matches)
        }
        setIsMobile(mediaQuery.matches)
        mediaQuery.addEventListener("change", onChange)
        return () => mediaQuery.removeEventListener("change", onChange)
    }, [])

    useEffect(() => {
        if (!isMobile) {
            return
        }
        setLeftCollapsed(mobileViewMode !== "list")
    }, [isMobile, mobileViewMode, setLeftCollapsed])

    useEffect(() => {
        setPanelRef(leftPannelRef);
    }, [setPanelRef]);

    if (isMobile) {
        const isListView = mobileViewMode === "list"
        const showMobileChatsShortcut = !isListView && chatUUID === null
        const mobileBackTarget = isIntegrationMode ? "/integrations" : "/chat"
        const mobileBackLabel = isIntegrationMode ? "Integrations" : "Open chats"
        return (
            <div className={`relative flex h-full min-h-0 w-full mobile-page-transition ${isListView ? "mobile-page-transition--list" : "mobile-page-transition--content"}`}>
                {showMobileChatsShortcut ? (
                    <div className="absolute left-3 top-3 z-40">
                        <Button type="button" variant="outline" size="sm" onClick={() => navigateTo(mobileBackTarget)}>
                            <PanelLeft className="mr-1 h-4 w-4" />
                            {mobileBackLabel}
                        </Button>
                    </div>
                ) : null}
                {isListView ? (
                    renderSidebar({ collapsed: false, toggle: () => {}, hideToggle: true })
                ) : (
                    children
                )}
            </div>
        )
    }

    if (!hasMounted) {
        return (
            <div className="flex h-full min-h-0 w-full">
                <StaticDesktopLayout
                    leftSize={desktopLayout.left}
                    rightSize={desktopLayout.right}
                    left={
                      renderSidebar({
                        collapsed: leftPannelCollapsed,
                        toggle: onToggleCollapse,
                      })
                    }
                    right={children}
                />
            </div>
        )
    }

    return (
        <div className="flex h-full min-h-0 w-full">
            <ResizableTilingLayout
                groupRef={groupRef}
                leftPannelRef={leftPannelRef}
                rightPannelRef={rightPannelRef}
                setLeftCollapsed={setLeftCollapsed}
                defaultLayoutLeft={desktopLayout.left}
                defaultLayoutRight={desktopLayout.right}
                left={
                  renderSidebar({
                    collapsed: leftPannelCollapsed,
                    toggle: onToggleCollapse,
                  })
                }
                right={children}
            />
        </div>
    );
}
