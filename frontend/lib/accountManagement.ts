import { useMemo } from "react"
import useSWR from "swr"
import { fetcher } from "@/lib/utils"

// Public config contributed by the account_management integration. The endpoint
// only exists when that integration is deployed, so a 404 is a meaningful
// answer: "this server has no account creation integration at all", which is a
// different situation from "installed, but sign-up is disabled".
export const ACCOUNT_MANAGEMENT_CONFIG_PATH = "/api/v1/integrations/account_management/config"

// Account creation is not a core page (ci#1150): the route is contributed by
// the account_management integration as a root page. Keeping the path here lets
// the landing/login UI reason about the deep link without importing the
// integration.
export const SIGN_UP_PATH = "/sign-up"

export const SIGNUP_UNAVAILABLE_INTEGRATION_ABSENT_MESSAGE =
  "Account creation is not available on this server."

export type AccountManagementPublicConfig = {
  signup_enabled?: boolean
  signup_requires_admin_approval?: boolean
  google_signup_enabled?: boolean
  google_client_id?: string
  google_signup_redirect_url?: string
  google_signup_start_url?: string
  require_email_verification?: boolean
}

export type AccountManagementIntegrationStatus = "loading" | "present" | "absent"

export type AccountManagementGate = {
  status: AccountManagementIntegrationStatus
  /** True once the config request answered 200 (integration deployed). */
  integrationPresent: boolean
  /** True when sign-up is actually offered (`signup_enabled === true`). */
  signupAvailable: boolean
  config?: AccountManagementPublicConfig
}

// Collapses the SWR state into the three states the UI has to distinguish.
// Any failure (404 because the integration is not deployed, but also a network
// error) is treated as "absent": without a positive `signup_enabled === true`
// from the config we must not advertise account creation.
export function resolveAccountManagementGate(
  config: AccountManagementPublicConfig | undefined,
  error: unknown,
): AccountManagementGate {
  if (error) {
    return { status: "absent", integrationPresent: false, signupAvailable: false }
  }
  if (config === undefined || config === null) {
    return { status: "loading", integrationPresent: false, signupAvailable: false }
  }
  return {
    status: "present",
    integrationPresent: true,
    signupAvailable: config.signup_enabled === true,
    config,
  }
}

export function useAccountManagementGate(): AccountManagementGate {
  const { data, error } = useSWR<AccountManagementPublicConfig>(ACCOUNT_MANAGEMENT_CONFIG_PATH, fetcher)
  return useMemo(() => resolveAccountManagementGate(data, error), [data, error])
}

// Normalises an URL pathname for comparisons: drops query/hash, collapses a
// trailing slash (except for the root) and tolerates a full URL.
export function normalizePathname(pathname: string | null | undefined): string {
  if (!pathname) {
    return ""
  }
  const withoutOrigin = pathname.replace(/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\/[^/]+/, "")
  const withoutQuery = withoutOrigin.split("?")[0].split("#")[0]
  if (!withoutQuery.startsWith("/")) {
    return ""
  }
  const trimmed = withoutQuery.replace(/\/+$/, "")
  return trimmed === "" ? "/" : trimmed
}

export function isSignUpPathname(pathname: string | null | undefined): boolean {
  return normalizePathname(pathname) === SIGN_UP_PATH
}

// The server answers `/sign-up` with the landing shell when no integration
// contributes that page, so the client has to select the tab from the pathname
// instead of from a Vike route.
export function landingTabForPathname(pathname: string | null | undefined): "register" | null {
  return isSignUpPathname(pathname) ? "register" : null
}