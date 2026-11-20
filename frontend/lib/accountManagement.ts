import { useMemo } from "react"
import useSWR from "swr"
import { fetcher } from "@/lib/utils"
import {
  ACCOUNT_MANAGEMENT_CONFIG_PATH,
  resolveAccountManagementGate,
  type AccountManagementGate,
  type AccountManagementPublicConfig,
} from "@/lib/accountManagementGate"

// The pure half of the gate lives in `@/lib/accountManagementGate` so it stays
// testable without React/DOM; everything below is the React binding.
export {
  ACCOUNT_MANAGEMENT_CONFIG_PATH,
  SIGNUP_UNAVAILABLE_INTEGRATION_ABSENT_MESSAGE,
  SIGN_UP_PATH,
  isSignUpPathname,
  landingTabForPathname,
  normalizePathname,
  resolveAccountManagementGate,
  resolveRegisterTabPresentation,
  type AccountManagementGate,
  type AccountManagementIntegrationStatus,
  type AccountManagementPublicConfig,
  type RegisterTabPresentation,
} from "@/lib/accountManagementGate"

export function useAccountManagementGate(): AccountManagementGate {
  const { data, error } = useSWR<AccountManagementPublicConfig>(ACCOUNT_MANAGEMENT_CONFIG_PATH, fetcher)
  return useMemo(() => resolveAccountManagementGate(data, error), [data, error])
}