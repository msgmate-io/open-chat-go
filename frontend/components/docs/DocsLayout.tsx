"use client"

import { useEffect, useRef, useState, type ReactNode } from "react"
import { Button, CollapseIndicator } from "@open-chat-go/ui"
import { ArrowLeft, PanelLeft } from "lucide-react"
import { navigate } from "vike/client/router"
import { DocsNavContent } from "@/components/docs/DocsNavContent"
import {
  isMobileViewport,
  readSavedDesktopLayout,
  ResizableTilingLayout,
  StaticDesktopLayout,
  useSidePanelCollapse,
} from "@open-chat-go/ui"

export function DocsLayout({
  activeDocPath,
  onSelect,
  children,
}: {
  activeDocPath: string
  onSelect: (path: string) => void
  children: ReactNode
}) {
  const leftPannelCollapsed = useSidePanelCollapse((state) => state.isCollapsed)
  const setLeftCollapsed = useSidePanelCollapse((state) => state.setCollapsed)
  const leftPannelRef = useRef<{ collapse: () => void; expand: () => void; isCollapsed: () => boolean } | null>(null)
  const rightPannelRef = useRef<{ collapse: () => void; expand: () => void } | null>(null)
  const groupRef = useRef<any>(null)
  const setPanelRef = useSidePanelCollapse((state) => state.setPanelRef)
  const onToggleCollapse = useSidePanelCollapse((state) => state.toggle)
  const [isMobile, setIsMobile] = useState(() => isMobileViewport())
  const [hasMounted, setHasMounted] = useState(false)
  const [mobileView, setMobileView] = useState<"nav" | "content">("content")
  const [desktopLayout, setDesktopLayout] = useState<{ left: number; right: number }>({ left: 22, right: 78 })

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
    setLeftCollapsed(mobileView !== "nav")
  }, [isMobile, mobileView, setLeftCollapsed])

  useEffect(() => {
    setPanelRef(leftPannelRef)
  }, [setPanelRef])

  const sidebar = (
    <DocsNavContent
      activeDocPath={activeDocPath}
      onSelect={(path) => {
        onSelect(path)
        if (isMobile) {
          setMobileView("content")
        }
      }}
      header={
        <div className="chat-list-header">
          <CollapseIndicator
            leftPannelCollapsed={leftPannelCollapsed}
            onToggleCollapse={onToggleCollapse}
          />
          <span className="min-w-0 flex-1 truncate text-sm font-semibold">Documentation</span>
        </div>
      }
    />
  )

  const content = (
    <div className="docs-main-scroll">
      <div className="docs-topbar">
        {isMobile ? (
          <Button type="button" variant="outline" size="lg" onClick={() => setMobileView("nav")}>
            <PanelLeft className="mr-2 size-5" />
            Docs menu
          </Button>
        ) : null}
        <Button
          type="button"
          variant="default"
          size="lg"
          className="docs-back-button"
          onClick={() => navigate("/")}
        >
          <ArrowLeft className="mr-2 size-5" />
          Back to OpenChat
        </Button>
      </div>
      <div className="docs-article-container">{children}</div>
    </div>
  )

  if (isMobile) {
    return (
      <div
        className={`relative flex h-full min-h-0 w-full mobile-page-transition ${
          mobileView === "nav" ? "mobile-page-transition--list" : "mobile-page-transition--content"
        }`}
      >
        {mobileView === "nav" ? sidebar : content}
      </div>
    )
  }

  if (!hasMounted) {
    return (
      <div className="flex h-full min-h-0 w-full">
        <StaticDesktopLayout
          leftSize={desktopLayout.left}
          rightSize={desktopLayout.right}
          left={sidebar}
          right={content}
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
        left={sidebar}
        right={content}
      />
    </div>
  )
}
