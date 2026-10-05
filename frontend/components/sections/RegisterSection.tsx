"use client"

import { useEffect, useRef, useState } from "react"
import { Button, Collapsible, CollapsibleContent, CollapsibleTrigger, Text, TextTypes } from "@open-chat-go/ui"
import { Input } from "@/components/Input"
import { ChevronDown } from "lucide-react"

type RegisterResponse = {
  message?: string
  request_uuid?: string
  status?: string
  email?: string
  require_email_verification?: boolean
  verification_token?: string
  signup_requires_admin_approval?: boolean
}

export default function RegisterSection({
  navigateTo,
  signupRequiresAdminApproval,
  googleSignupEnabled,
  googleClientID,
  googleSignupRedirectURL,
  requestMode = false,
}: {
  navigateTo: (to: string) => void
  signupRequiresAdminApproval: boolean
  googleSignupEnabled: boolean
  googleClientID?: string
  googleSignupRedirectURL?: string
  requestMode?: boolean
}) {
  const signupRequestPath = "/signup-request-send"
  const [name, setName] = useState("")
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")
  const [success, setSuccess] = useState("")
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [googleOpen, setGoogleOpen] = useState(false)
  const googleButtonRef = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    if (!googleSignupEnabled || !googleClientID || !googleSignupRedirectURL || !googleOpen) {
      return
    }

    const renderGoogleButton = () => {
      const google = (window as any).google
      if (!google?.accounts?.id || !googleButtonRef.current) {
        return
      }
      google.accounts.id.initialize({
        client_id: googleClientID,
        context: "signup",
        ux_mode: "redirect",
        login_uri: googleSignupRedirectURL,
      })
      googleButtonRef.current.innerHTML = ""
      google.accounts.id.renderButton(googleButtonRef.current, {
        type: "standard",
        shape: "pill",
        theme: "outline",
        text: "signup_with",
        size: "large",
      })
    }

    if ((window as any).google?.accounts?.id) {
      renderGoogleButton()
      return
    }

    const existingScript = document.querySelector('script[src="https://accounts.google.com/gsi/client"]') as HTMLScriptElement | null
    if (existingScript) {
      existingScript.addEventListener("load", renderGoogleButton, { once: true })
      return () => existingScript.removeEventListener("load", renderGoogleButton)
    }

    const script = document.createElement("script")
    script.src = "https://accounts.google.com/gsi/client"
    script.async = true
    script.defer = true
    script.onload = renderGoogleButton
    document.body.appendChild(script)

    return () => {
      script.onload = null
    }
  }, [googleSignupEnabled, googleClientID, googleSignupRedirectURL, googleOpen])

  const onSubmit = async () => {
    setError("")
    setSuccess("")
    setIsSubmitting(true)
    try {
      const response = await fetch("/api/v1/user/register", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          name: name.trim(),
          email: email.trim(),
          password,
        }),
      })

      if (response.status === 201) {
        setSuccess("Account created. You can log in now.")
        window.setTimeout(() => navigateTo("/login"), 500)
        return
      }

      if (response.status === 202) {
        const responseText = await response.text().catch(() => "")
        let data: RegisterResponse = {}
        if (responseText) {
          try {
            data = JSON.parse(responseText) as RegisterResponse
          } catch {
            data = {}
          }
        }
        const requestedEmail = data.email || email.trim()
        if (data.require_email_verification && data.request_uuid) {
          const params = new URLSearchParams({ request_uuid: data.request_uuid })
          if (requestedEmail) {
            params.set("email", requestedEmail)
          }
          if (typeof data.signup_requires_admin_approval === "boolean") {
            params.set("signup_requires_admin_approval", data.signup_requires_admin_approval ? "true" : "false")
          }
          if (data.verification_token) {
            params.set("token", data.verification_token)
          }
          navigateTo(`/email-verification?${params.toString()}`)
          return
        }
        const target = requestedEmail ? `${signupRequestPath}?email=${encodeURIComponent(requestedEmail)}` : signupRequestPath
        navigateTo(target)
        return
      }

      const responseText = await response.text().catch(() => "")
      let payload: RegisterResponse | null = null
      if (responseText) {
        try {
          payload = JSON.parse(responseText) as RegisterResponse
        } catch {
          payload = null
        }
      }
      if (payload?.message) {
        if (payload.message.toLowerCase().includes("registration request already pending")) {
          const requestedEmail = email.trim()
          const target = requestedEmail ? `${signupRequestPath}?email=${encodeURIComponent(requestedEmail)}` : signupRequestPath
          navigateTo(target)
          return
        }
        setError(payload.message)
        return
      }
      if (responseText.toLowerCase().includes("registration request already pending")) {
        const requestedEmail = email.trim()
        const target = requestedEmail ? `${signupRequestPath}?email=${encodeURIComponent(requestedEmail)}` : signupRequestPath
        navigateTo(target)
        return
      }
      if (responseText.trim()) {
        setError(responseText.trim())
        return
      }
      setError(`Unable to register (status ${response.status})`)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to register")
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <div className="container pt-6 pb-20 sm:py-32 flex flex-col flex-grow items-center content-center justify-start sm:justify-center text-foreground max-w-xl">
      <div className="flex flex-col items-center content-center justify-center pb-2">
        <Text type={TextTypes.Heading5} tag="h1" bold center>
          {requestMode ? "Send signup request" : "Create your account"}
        </Text>
        <Text type={TextTypes.Body4} center>
          {requestMode
            ? "New accounts require admin approval."
            : (signupRequiresAdminApproval
              ? "Sign up requires manual confirmation at the moment, only invited tester will be enabled!"
              : "Sign up for Open-Chat")}
        </Text>
      </div>
      <div className="flex flex-col relative w-full gap-3">
        <Input
          type="text"
          placeholder="Name"
          className="rounded-full py-6 text-lg text-center border-2"
          value={name}
          onChange={(event) => setName(event.target.value)}
        />
        <Input
          type="email"
          placeholder="Email"
          className="rounded-full py-6 text-lg text-center border-2"
          value={email}
          onChange={(event) => setEmail(event.target.value)}
        />
        <Input
          type="password"
          placeholder="Password"
          className="rounded-full py-6 text-lg text-center border-2"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
        />
        <Button
          variant="outline"
          className="rounded-full border-2"
          onClick={onSubmit}
          disabled={isSubmitting}
        >
          {isSubmitting ? "Submitting..." : (requestMode ? "Send request" : "Sign up")}
        </Button>
        {googleSignupEnabled && googleClientID && googleSignupRedirectURL ? (
          <Collapsible open={googleOpen} onOpenChange={setGoogleOpen} className="flex flex-col items-center">
            <CollapsibleTrigger asChild>
              <Button type="button" variant="ghost" className="h-auto px-2 py-1 text-xs text-muted-foreground hover:text-foreground">
                or continue with Google
                <ChevronDown className={`ml-1 h-3 w-3 transition-transform ${googleOpen ? "rotate-180" : ""}`} />
              </Button>
            </CollapsibleTrigger>
            <CollapsibleContent className="CollapsibleContent">
              <div ref={googleButtonRef} />
            </CollapsibleContent>
          </Collapsible>
        ) : null}
        {error ? (
          <Text type={TextTypes.Body6} color="destructive" center>
            {error}
          </Text>
        ) : null}
        {success ? (
          <Text type={TextTypes.Body6} color="primary" center>
            {success}
          </Text>
        ) : null}
        <Button variant="ghost" className="rounded-full" onClick={() => navigateTo("/login")}>
          Already have an account? Log in
        </Button>
      </div>
    </div>
  )
}
