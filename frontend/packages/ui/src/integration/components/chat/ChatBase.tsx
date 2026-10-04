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
    hideMobileShortcut = false,
    sidebarTitle,
    sidebar,
    sidebarTopSection,
    integrationName,
}: {
    children: ReactNode,
    chatUUID: string | null,
    navigateTo: (to: string) => void,
    mobileViewMode?: "list" | "content",
    hideMobileShortcut?: boolean,
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
    const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false)
    const [desktopLayout, setDesktopLayout] = useState<{ left: number; right: number }>({ left: 25, right: 75 })
    const pageContext = usePageContext()
    const activePath = pageContext?.urlPathname || (typeof window !== "undefined" ? window.location.pathname : "")
    const isIntegrationMode = integrationName !== undefined
    const resolvedSidebarTitle = isIntegrationMode
        ? (integrationName ?? "Integrations")
        : sidebarTitle || getPageMetadata(typeof window !== "undefined" ? window.location.pathname : "").sidebarTitle

    // Selecting an entry from the mobile fullscreen sidebar should navigate and
    // dismiss the sidebar so the destination page is visible immediately.
    const handleSidebarNavigate = (to: string) => {
        setMobileSidebarOpen(false)
        navigateTo(to)
    }

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
                navigateTo={handleSidebarNavigate}
                themeSelector={<ConnectedThemeSelector />}
                avatarSrc={logoUrl}
                hideCollapseToggle={hideToggle}
                title={resolvedSidebarTitle}
                topSection={
                    sidebarTopSection ?? (isIntegrationMode ? (
                        <IntegrationsNav
                            integrationName={integrationName ?? null}
                            activePath={activePath}
                            navigateTo={handleSidebarNavigate}
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

    // Any route change should dismiss the transient mobile sidebar overlay.
    useEffect(() => {
        setMobileSidebarOpen(false)
    }, [activePath])

    if (isMobile) {
        const isDedicatedListView = mobileViewMode === "list"
        const isSidebarVisible = isDedicatedListView || mobileSidebarOpen
        const canOpenMobileSidebar = isIntegrationMode || chatUUID === null
        const showMobileChatsShortcut = !isSidebarVisible && canOpenMobileSidebar && !hideMobileShortcut
        const mobileBackTarget = isIntegrationMode ? "/integrations" : "/chat"
        const mobileBackLabel = isIntegrationMode ? "Integrations" : "Open chats"
        const openMobileSidebar = () => {
            // Integration pages have no dedicated list route: reveal the
            // fullscreen sidebar in place so the integration/page list (and the
            // page-specific top section) stays reachable on native.
            if (isIntegrationMode) {
                setMobileSidebarOpen(true)
                return
            }
            navigateTo(mobileBackTarget)
        }
        return (
            <div className={`relative flex h-full min-h-0 w-full mobile-page-transition ${isSidebarVisible ? "mobile-page-transition--list" : "mobile-page-transition--content"}`}>
                {showMobileChatsShortcut ? (
                    <div className="absolute left-3 top-3 z-40">
                        <Button type="button" variant="outline" size="sm" onClick={openMobileSidebar}>
                            <PanelLeft className="mr-1 h-4 w-4" />
                            {mobileBackLabel}
                        </Button>
                    </div>
                ) : null}
                {isSidebarVisible ? (
                    renderSidebar({
                        collapsed: false,
                        toggle: () => setMobileSidebarOpen(false),
                        hideToggle: isDedicatedListView && !mobileSidebarOpen,
                    })
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
