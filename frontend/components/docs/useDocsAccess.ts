"use client"

import { useEffect, useMemo, useState } from "react"
import { useCurrentUser } from "@open-chat-go/ui"

import type { DocMetadata } from "@/pages/docs/docs"

/** Why a documented page is (not) rendered for the current visitor. */
export type DocGateReason = "none" | "signin" | "no-access"

export type DocsAccessState = {
  isSignedIn: boolean;
  /** `null` while the caller's accessible integrations are unknown. */
  accessibleIntegrations: string[] | null;
  isLoading: boolean;
}

type IntegrationsListResponse = {
  rows?: { name?: string }[];
}

/**
 * Resolves the caller's documentation access.
 *
 * Sign-in state comes from the shared session query; the accessible
 * integrations come from the public `integrations/list` endpoint, which only
 * returns the integrations the caller may actually see. That endpoint is
 * session-scoped, so an anonymous visitor is treated as signed out.
 */
export function useDocsAccess(): DocsAccessState {
  const { data: user, isLoading: userLoading } = useCurrentUser() as {
    data?: { is_admin?: boolean } | null;
    isLoading?: boolean;
  };
  const isSignedIn = !!user;
  const [accessibleIntegrations, setAccessibleIntegrations] = useState<string[] | null>(null)

  useEffect(() => {
    if (!isSignedIn) {
      setAccessibleIntegrations(null)
      return
    }

    let cancelled = false
    setAccessibleIntegrations(null)

    const load = async () => {
      try {
        const response = await fetch("/api/v1/integrations/list", { credentials: "include" })
        if (cancelled) return
        if (response.status === 401 || response.status === 403) {
          // Signed in, but the caller may not list integrations: keep the known
          // set empty so integration docs stay behind the no-access notice.
          setAccessibleIntegrations([])
          return
        }
        if (!response.ok) {
          setAccessibleIntegrations([])
          return
        }
        const data = (await response.json()) as IntegrationsListResponse
        if (cancelled) return
        setAccessibleIntegrations(
          (data.rows ?? [])
            .map((row) => row?.name)
            .filter((name): name is string => typeof name === "string"),
        )
      } catch {
        if (!cancelled) {
          setAccessibleIntegrations([])
        }
      }
    }

    void load()
    return () => {
      cancelled = true
    }
  }, [isSignedIn])

  return useMemo(
    () => ({
      isSignedIn,
      accessibleIntegrations,
      isLoading: !!userLoading || (isSignedIn && accessibleIntegrations === null),
    }),
    [isSignedIn, accessibleIntegrations, userLoading],
  )
}

/**
 * Returns the gate reason for a single documentation page.
 *
 * `public` pages are always readable, `authenticated` pages need a session and
 * `integration` pages need a session plus access to the documented integration.
 * While access is still resolving the page counts as gated, so private
 * documentation never flashes for an unauthorised visitor.
 */
export function getDocGateReason(
  doc: Pick<DocMetadata, "visibility" | "integration">,
  access: Pick<DocsAccessState, "isSignedIn" | "accessibleIntegrations">,
): DocGateReason {
  const visibility = doc.visibility ?? "public"

  if (visibility === "public") {
    return "none"
  }

  if (!access.isSignedIn) {
    return "signin"
  }

  if (visibility === "integration") {
    if (!doc.integration) {
      return "none"
    }
    if (access.accessibleIntegrations === null) {
      return "no-access"
    }
    return access.accessibleIntegrations.includes(doc.integration) ? "none" : "no-access"
  }

  return "none"
}

/** True when the doc may be rendered for the current visitor. */
export function isDocVisible(
  doc: Pick<DocMetadata, "visibility" | "integration">,
  access: Pick<DocsAccessState, "isSignedIn" | "accessibleIntegrations">,
): boolean {
  return getDocGateReason(doc, access) === "none"
}
