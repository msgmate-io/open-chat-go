"use client"

// React binding for the pinned-integration-pages model (ci#610).
//
// Every hook instance shares one module-level store + subscription list, so
// pinning from the integration sidebar immediately updates the profile menu
// (and vice versa) without a provider. Persistence goes through the injectable
// `createPinnedPagesStore` seam, keeping the door open for a backend-backed
// store.

import { useCallback, useEffect, useMemo, useState } from "react"
import {
  createPinnedPagesStore,
  isPagePinned,
  resolveIntegrationLandingPath,
  togglePinnedPage,
  type PinnedIntegrationPage,
  type PinnedPageInput,
} from "./pinned-integrations"

const store = createPinnedPagesStore()
const listeners = new Set<() => void>()
let snapshot: PinnedIntegrationPage[] | null = null

function readSnapshot(userKey: string | null): PinnedIntegrationPage[] {
  if (snapshot === null) {
    snapshot = store.load(userKey)
  }
  return snapshot
}

function publish(next: PinnedIntegrationPage[], userKey: string | null): void {
  snapshot = next
  store.save(userKey, next)
  listeners.forEach((listener) => listener())
}

/** Test/hydration helper: drops the module cache and re-reads storage. */
export function resetPinnedIntegrationPagesCache(): void {
  snapshot = null
}

export function usePinnedIntegrationPages(userKey: string | null = null) {
  const [, forceRender] = useState(0)
  const [hydrated, setHydrated] = useState(false)

  useEffect(() => {
    const listener = () => forceRender((value) => value + 1)
    listeners.add(listener)
    // Storage is not available during SSR; re-read once we are on the client so
    // the first paint matches what the browser actually has pinned.
    snapshot = store.load(userKey)
    setHydrated(true)
    return () => {
      listeners.delete(listener)
    }
  }, [userKey])

  const pages = useMemo(() => readSnapshot(userKey), [userKey, hydrated])

  const isPinned = useCallback(
    (path: string) => isPagePinned(pages, path),
    [pages],
  )

  const setPinned = useCallback(
    (input: PinnedIntegrationPage | PinnedPageInput | string, pinned = true) => {
      publish(togglePinnedPage(pages, input, pinned), userKey)
    },
    [pages, userKey],
  )

  const toggle = useCallback(
    (input: PinnedIntegrationPage | PinnedPageInput | string) => {
      setPinned(input, !isPagePinned(pages, typeof input === "string" ? input : input.path))
    },
    [pages, setPinned],
  )

  const landingPath = useCallback(
    (integration: string, fallbackPath?: string | null) =>
      resolveIntegrationLandingPath(integration, pages, fallbackPath),
    [pages],
  )

  return { pages, hydrated, isPinned, setPinned, toggle, landingPath }
}
