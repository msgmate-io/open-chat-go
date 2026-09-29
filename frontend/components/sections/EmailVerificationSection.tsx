"use client"

import { Button, Text, TextTypes } from "@open-chat-go/ui"
import { useEffect, useRef, useState } from "react"
import useSWR from "swr"
import { Input } from "@/components/Input"
import { fetcher } from "@/lib/utils"

const emailVerificationContextCookieName = "oc_email_verification_context"

type EmailVerificationCookieContext = {
  requestUUID?: string
  email?: string
  signupRequiresAdminApproval?: boolean
}

function parseBoolString(raw: string | null): boolean | undefined {
  if (!raw) {
    return undefined
  }
  const normalized = raw.trim().toLowerCase()
  if (normalized === "true") {
    return true
  }
  if (normalized === "false") {
    return false
  }
  return undefined
}

function readEmailVerificationContextCookie(): EmailVerificationCookieContext {
  if (typeof document === "undefined") {
    return {}
  }
  const parts = document.cookie.split(";")
  const raw = parts
    .map((entry) => entry.trim())
    .find((entry) => entry.startsWith(`${emailVerificationContextCookieName}=`))
  if (!raw) {
    return {}
  }
  const encoded = raw.slice(emailVerificationContextCookieName.length + 1)
  const decoded = decodeURIComponent(encoded)
  const params = new URLSearchParams(decoded)
  return {
    requestUUID: params.get("request_uuid") || undefined,
    email: params.get("email") || undefined,
    signupRequiresAdminApproval: parseBoolString(params.get("signup_requires_admin_approval")),
  }
}

function clearEmailVerificationContextCookie() {
  if (typeof document === "undefined") {
    return
  }
  document.cookie = `${emailVerificationContextCookieName}=; Path=/; Max-Age=0; SameSite=Lax`
}

type StatusResponse = {
  require_email_verification?: boolean
  is_email_verified?: boolean
  request_uuid?: string
  email?: string
  signup_requires_admin_approval?: boolean
}

type RequestResponse = {
  verification_token?: string
  verification_code?: string
  message?: string
  request_uuid?: string
  email?: string
}

export default function EmailVerificationSection({
  navigateTo,
}: {
  navigateTo: (to: string) => void
}) {
  const initialParams = typeof window !== "undefined" ? new URLSearchParams(window.location.search) : null
  const cookieContext = readEmailVerificationContextCookie()
  const [token, setToken] = useState(initialParams?.get("token") || "")
  const [code, setCode] = useState("")
  const [feedback, setFeedback] = useState("")
  const [error, setError] = useState("")
  const [requestUUID, setRequestUUID] = useState(initialParams?.get("request_uuid") || cookieContext.requestUUID || "")
  const [requestEmail, setRequestEmail] = useState(initialParams?.get("email") || cookieContext.email || "")
  const [signupRequiresAdminApprovalHint, setSignupRequiresAdminApprovalHint] = useState<boolean | undefined>(
    cookieContext.signupRequiresAdminApproval,
  )
  const redirectScheduledRef = useRef(false)

  const requestMode = requestUUID.trim().length > 0
  const publicMode = !requestMode && requestEmail.trim().length > 0

  const verificationBasePath = requestMode
    ? `/api/v1/integrations/account_management/registration-requests/${requestUUID}/email-verification`
    : (publicMode
      ? "/api/v1/integrations/account_management/email-verification/public"
      : "/api/v1/integrations/account_management/email-verification")

  const statusPath = requestMode
    ? `${verificationBasePath}/status`
    : (publicMode
      ? (requestEmail.trim() ? `${verificationBasePath}/status?email=${encodeURIComponent(requestEmail.trim())}` : null)
      : `${verificationBasePath}/status`)

  const {
    data: status,
    error: statusError,
    mutate: mutateStatus,
  } = useSWR<StatusResponse>(statusPath, fetcher, {
    refreshInterval: (latest: StatusResponse | undefined) => {
      if (!latest) {
        return 2000
      }
      if (latest.require_email_verification === false) {
        return 0
      }
      return latest.is_email_verified ? 0 : 2000
    },
  })

  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const tokenFromURL = params.get("token")
    const requestUUIDFromURL = params.get("request_uuid")
    const requestEmailFromURL = params.get("email")
    const signupRequiresAdminApprovalFromURL = parseBoolString(params.get("signup_requires_admin_approval"))
    if (tokenFromURL) {
      setToken(tokenFromURL)
    }
    if (requestUUIDFromURL) {
      setRequestUUID(requestUUIDFromURL)
    }
    if (requestEmailFromURL) {
      setRequestEmail(requestEmailFromURL)
    }
    if (signupRequiresAdminApprovalFromURL !== undefined) {
      setSignupRequiresAdminApprovalHint(signupRequiresAdminApprovalFromURL)
    }
  }, [])

  useEffect(() => {
    if (!status) {
      return
    }
    if (status.request_uuid && status.request_uuid !== requestUUID) {
      setRequestUUID(status.request_uuid)
    }
    if (status.email && status.email !== requestEmail) {
      setRequestEmail(status.email)
    }
    if (typeof status.signup_requires_admin_approval === "boolean") {
      setSignupRequiresAdminApprovalHint(status.signup_requires_admin_approval)
    }
  }, [requestEmail, requestUUID, status])

  useEffect(() => {
    if (statusError) {
      setError(statusError instanceof Error ? statusError.message : "Unable to load status")
      return
    }
    setError("")
  }, [statusError])

  const requestChallenge = async () => {
    setError("")
    setFeedback("")
    const response = await fetch(`${verificationBasePath}/request`, {
      method: "POST",
      headers: publicMode ? { "Content-Type": "application/json" } : undefined,
      body: publicMode ? JSON.stringify({ email: requestEmail.trim() }) : undefined,
    })
    if (!response.ok) {
      const text = await response.text().catch(() => "Unable to request verification")
      setError(text)
      return
    }
    const payload = (await response.json().catch(() => ({}))) as RequestResponse
    if (payload.verification_token) {
      setToken(payload.verification_token)
    }
    if (payload.verification_code) {
      setCode(payload.verification_code)
    }
    if (payload.request_uuid) {
      setRequestUUID(payload.request_uuid)
    }
    if (payload.email) {
      setRequestEmail(payload.email)
    }
    setFeedback(payload.message || "Verification challenge generated")
    await mutateStatus()
  }

  const verify = async () => {
    setError("")
    setFeedback("")
    const response = await fetch(`${verificationBasePath}/verify`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: requestEmail.trim(), token: token.trim(), code: code.trim() }),
    })
    if (!response.ok) {
      const text = await response.text().catch(() => "Unable to verify email")
      setError(text)
      return
    }
    setFeedback("Email verified. Redirecting...")
    await mutateStatus()
  }

  const isVerified = status?.is_email_verified === true

  useEffect(() => {
    if (!isVerified || redirectScheduledRef.current) {
      return
    }
    redirectScheduledRef.current = true
    clearEmailVerificationContextCookie()
    setFeedback("Email verified. Redirecting...")
    window.setTimeout(() => {
      const requiresAdminApproval = signupRequiresAdminApprovalHint === true || requestMode
      if (requiresAdminApproval) {
        navigateTo(requestEmail ? `/signup-request-send?email=${encodeURIComponent(requestEmail)}` : "/signup-request-send")
        return
      }
      navigateTo("/chat")
    }, 650)
  }, [isVerified, navigateTo, requestEmail, requestMode, signupRequiresAdminApprovalHint])

  return (
    <div className="container py-24 sm:py-32 flex flex-col flex-grow items-center content-center justify-center text-foreground max-w-xl">
      <div className="flex flex-col items-center content-center justify-center pb-2">
          <Text type={TextTypes.Heading5} tag="h1" bold center>
            Verify your email
          </Text>
          <Text type={TextTypes.Body4} center>
            {isVerified
              ? "Email already verified."
              : (requestMode
                ? "Please verify your email before admin approval can complete your signup."
                : "Please verify your email before using Open-Chat.")}
          </Text>
          {requestEmail ? (
            <Text type={TextTypes.Body6} center color="muted">
              Requested with: {requestEmail}
            </Text>
          ) : null}
        </div>
      <div className="flex flex-col relative w-full gap-3">
        {!isVerified && (
          <>
            <Input
              type="text"
              placeholder="6-digit verification code"
              className="rounded-full py-6 text-lg text-center border-2"
              value={code}
              onChange={(event) => setCode(event.target.value)}
            />
            <div className="flex justify-center">
              <Button variant="ghost" className="h-auto px-2 py-1 text-xs text-muted-foreground hover:text-foreground" onClick={requestChallenge}>
                Resend verification email
              </Button>
            </div>
            <Button variant="outline" className="rounded-full border-2" onClick={verify}>
              Verify email
            </Button>
          </>
        )}
        {feedback ? <Text type={TextTypes.Body6} color="primary" center>{feedback}</Text> : null}
        {error ? <Text type={TextTypes.Body6} color="destructive" center>{error}</Text> : null}
      </div>
    </div>
  )
}
