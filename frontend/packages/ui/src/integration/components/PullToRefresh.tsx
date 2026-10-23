"use client"

import * as React from "react"
import { useEffect, useRef, useState } from "react"
import { cn } from "../../lib/utils"
import { LoadingSpinner } from "../../components/chat/loading-spinner"
import { isMobileAppRuntime } from "../lib/mobile-app"
import {
    dampPullDistance,
    pullProgress,
    shouldTriggerRefresh,
    PULL_REFRESH_THRESHOLD,
} from "../lib/pull-to-refresh"

// Native-only pull-down-to-refresh.
//
// This is deliberately a small wrapper around an existing scroll container: it
// adds a non-scrolling overlay that reveals an animated spinner as the user
// drags down from the top, and calls `onRefresh` once released past the
// threshold. It is inert in the browser (and on desktop) so the web app keeps
// its normal overscroll behaviour.
//
// The gesture is wired with native `touchstart`/`touchmove`/`touchend`
// listeners (`passive: false`) rather than React handlers so we can
// `preventDefault()` while actively pulling and stop the WebView from fighting
// the gesture with its own scroll/overscroll.

const MIN_REFRESH_MS = 450
const DRAG_SLOP_PX = 6
const ARMED_EXTRA_PX = 40

export interface PullToRefreshProps {
    onRefresh: () => void | Promise<unknown>
    children: React.ReactNode
    // Existing scroll element the gesture listens on. When omitted an internal
    // ref is used.
    scrollRef?: React.RefObject<HTMLDivElement | null>
    // Outer (layout) wrapper. Defaults to a flex column that fills its parent so
    // the inner scroller keeps the original sizing.
    className?: string
    // Classes for the scroll container itself (moved here from the caller).
    scrollClassName?: string
    // Extra props (e.g. an existing onScroll handler) applied to the scroll
    // container.
    scrollProps?: React.HTMLAttributes<HTMLDivElement>
    // Force the feature on/off. Defaults to "only inside the native app".
    enabled?: boolean
}

export function PullToRefresh({
    onRefresh,
    children,
    scrollRef: externalScrollRef,
    className,
    scrollClassName,
    scrollProps,
    enabled,
}: PullToRefreshProps) {
    const internalScrollRef = useRef<HTMLDivElement | null>(null)
    const scrollRef = externalScrollRef ?? internalScrollRef

    const [isNative, setIsNative] = useState(false)
    const [distance, setDistance] = useState(0)
    const [isRefreshing, setIsRefreshing] = useState(false)

    useEffect(() => {
        setIsNative(typeof enabled === "boolean" ? enabled : isMobileAppRuntime())
    }, [enabled])

    const active = typeof enabled === "boolean" ? enabled : isNative

    // Gesture bookkeeping lives in refs: the handlers are attached once and must
    // observe the latest refresh state without re-subscribing.
    const startYRef = useRef(0)
    const startXRef = useRef(0)
    const pullingRef = useRef(false)
    const refreshingRef = useRef(false)
    const distanceRef = useRef(0)
    const onRefreshRef = useRef(onRefresh)

    useEffect(() => {
        onRefreshRef.current = onRefresh
    }, [onRefresh])

    useEffect(() => {
        refreshingRef.current = isRefreshing
    }, [isRefreshing])

    useEffect(() => {
        if (!active) {
            return
        }
        const element = scrollRef.current
        if (!element) {
            return
        }

        const setPullDistance = (next: number) => {
            distanceRef.current = next
            setDistance(next)
        }

        const resetGesture = () => {
            pullingRef.current = false
            setPullDistance(0)
        }

        const runRefresh = async () => {
            setIsRefreshing(true)
            refreshingRef.current = true
            setPullDistance(PULL_REFRESH_THRESHOLD)
            const startedAt = Date.now()
            try {
                await onRefreshRef.current()
            } catch {
                // A failed refresh should still snap back to rest.
            } finally {
                const remaining = Math.max(0, MIN_REFRESH_MS - (Date.now() - startedAt))
                if (remaining > 0) {
                    await new Promise((resolve) => setTimeout(resolve, remaining))
                }
                setIsRefreshing(false)
                refreshingRef.current = false
                setPullDistance(0)
            }
        }

        const handleTouchStart = (event: TouchEvent) => {
            if (refreshingRef.current || event.touches.length !== 1) {
                return
            }
            if (element.scrollTop > 0) {
                return
            }
            startYRef.current = event.touches[0].clientY
            startXRef.current = event.touches[0].clientX
            pullingRef.current = false
        }

        const handleTouchMove = (event: TouchEvent) => {
            if (refreshingRef.current || event.touches.length !== 1) {
                return
            }

            const touch = event.touches[0]
            const deltaY = touch.clientY - startYRef.current
            const deltaX = touch.clientX - startXRef.current

            // Any upward motion or a non-zero scroll offset means the user is
            // scrolling the list, not pulling to refresh.
            if (deltaY <= 0 || element.scrollTop > 0) {
                if (pullingRef.current) {
                    resetGesture()
                }
                return
            }

            // Ignore mostly-horizontal gestures so carousels/rows still work.
            if (Math.abs(deltaX) > Math.abs(deltaY)) {
                return
            }

            pullingRef.current = true
            if (event.cancelable) {
                event.preventDefault()
            }
            setPullDistance(dampPullDistance(deltaY))
        }

        const handleTouchEnd = () => {
            if (!pullingRef.current) {
                return
            }
            pullingRef.current = false
            if (shouldTriggerRefresh(distanceRef.current)) {
                void runRefresh()
            } else {
                setPullDistance(0)
            }
        }

        element.addEventListener("touchstart", handleTouchStart, { passive: true })
        element.addEventListener("touchmove", handleTouchMove, { passive: false })
        element.addEventListener("touchend", handleTouchEnd)
        element.addEventListener("touchcancel", handleTouchEnd)

        return () => {
            element.removeEventListener("touchstart", handleTouchStart)
            element.removeEventListener("touchmove", handleTouchMove)
            element.removeEventListener("touchend", handleTouchEnd)
            element.removeEventListener("touchcancel", handleTouchEnd)
        }
    }, [active, scrollRef])

    const progress = pullProgress(distance)
    const visible = active && (distance > 0 || isRefreshing)
    const armed = distance >= PULL_REFRESH_THRESHOLD
    // Give a little extra travel past the threshold so the "armed" state feels
    // deliberate before release.
    const indicatorHeight = isRefreshing
        ? PULL_REFRESH_THRESHOLD + ARMED_EXTRA_PX
        : distance

    return (
        <div className={cn("oc-pull-to-refresh relative flex min-h-0 flex-1 flex-col", className)}>
            {active ? (
                <div
                    aria-hidden={!visible}
                    data-ptr-state={isRefreshing ? "refreshing" : armed ? "armed" : "pulling"}
                    className={cn(
                        "pointer-events-none absolute inset-x-0 top-0 z-30 flex items-center justify-center overflow-hidden",
                        isRefreshing || distance === 0 ? "transition-[height,opacity] duration-200 ease-out" : "transition-none",
                    )}
                    style={{ height: visible ? indicatorHeight : 0, opacity: visible ? 1 : 0 }}
                >
                    <span
                        className="flex size-8 items-center justify-center rounded-full border border-border/70 bg-card/95 text-primary shadow-sm backdrop-blur-sm"
                        style={
                            isRefreshing
                                ? undefined
                                : {
                                      transform: `scale(${0.55 + progress * 0.45}) rotate(${progress * 270}deg)`,
                                  }
                        }
                    >
                        <LoadingSpinner size={18} />
                    </span>
                </div>
            ) : null}
            <div ref={scrollRef} className={cn("min-h-0 flex-1", scrollClassName)} {...scrollProps}>
                {children}
            </div>
        </div>
    )
}
