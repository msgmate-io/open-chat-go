"use client"

import { Button, Collapsible, CollapsibleContent, CollapsibleTrigger, Text, TextTypes } from "@open-chat-go/ui"
import { ChevronDown, EyeIcon, EyeOff } from 'lucide-react';
import { ReloadIcon } from "@radix-ui/react-icons";
import { zodResolver } from "@hookform/resolvers/zod";
import { Toaster } from "@/components/Toaster"
import { FormProvider, useForm } from "react-hook-form";
import { useState, useEffect, useRef } from "react"
import { mutate } from "swr"
import { Input } from "@/components/Input";
import { z } from "zod";
import {
    FormControl,
    FormField,
    FormItem,
    FormMessage
} from "@/components/Form";
import { MobileServerSelector } from "@/components/mobile/MobileServerSelector";
import { isMobileAppRuntime } from "@open-chat-go/ui";


const formSchema = z.object({
    email: z.string().min(2, {
        message: "Username must be at least 2 characters.",
    }),
    password: z.string().min(8, {
        message: "Password must be at least 8 characters.",
    }),
    two_factor_code: z.string().optional(),
    recovery_code: z.string().optional(),
})

type ErrorResult = null | { [key: string]: string }
type LoginSuccessResponse = {
    success: boolean
    user?: Record<string, unknown>
}

type EmailVerificationStatusResponse = {
    require_email_verification?: boolean
    is_email_verified?: boolean
}

export default function LoginHero({
    navigateTo,
    onSignUpClick,
}: {
    navigateTo: (to: string) => void,
    onSignUpClick?: () => void,
}) {
    const [showPassword, setShowPassword] = useState(false)
    const togglePasswordVisibility = () => setShowPassword(!showPassword);
    const [error, setError] = useState<ErrorResult>(null)
    const [requiresTwoFactor, setRequiresTwoFactor] = useState(false)
    const autoLoginTriggeredRef = useRef(false)
    const [postLoginRedirect, setPostLoginRedirect] = useState<string>("")
    const [isMobileRuntime, setIsMobileRuntime] = useState(false)
    
    const onSubmit = async () => {
       // Pass only the host (no scheme, no port) so the backend can set the cookie Domain correctly.
       const cookieDomain = typeof window !== "undefined" ? window.location.hostname : "";
       const loginUrl = cookieDomain
          ? `/api/v1/user/login?cookie_domain=${encodeURIComponent(cookieDomain)}`
          : "/api/v1/user/login";
       try {
        const res = await fetch(loginUrl, {
             method: "POST",
             body: JSON.stringify({
                 ...form.getValues()
             })
       })

         if(res.ok){
            const payload = await res.json().catch(() => null) as LoginSuccessResponse | null
            if (payload?.user) {
                mutate('/api/v1/user/self', payload.user, false)
            }

            for (let i = 0; i < 5; i++) {
                try {
                    const selfUser = await mutate('/api/v1/user/self')
                    if (selfUser) break
                } catch {
                    // Session cookie might still be settling on first login response.
                }
                await new Promise((resolve) => setTimeout(resolve, 150))
            }
            const verifyResponse = await fetch("/api/v1/integrations/account_management/email-verification/status")
            if (verifyResponse.ok) {
                const verifyPayload = await verifyResponse.json().catch(() => ({})) as EmailVerificationStatusResponse
                if (verifyPayload.require_email_verification && !verifyPayload.is_email_verified) {
                    const loginEmail = String(form.getValues("email") || "").trim()
                    navigateTo(loginEmail ? `/email-verification?email=${encodeURIComponent(loginEmail)}` : "/email-verification")
                    return
                }
            }
            navigateTo(postLoginRedirect || "/chat")
         }else{
            const json = await res.json().catch(() => null)
                if (json?.requires_two_factor) {
                    setRequiresTwoFactor(true)
                    setError({ non_field_errors: json?.error || "Two-factor code required" })
                } else {
                    setError({ non_field_errors: json?.error || "Error occurred while logging in." })
                }
                if (!json) {
                    const text = await res.text().catch(() => "Error occurred while logging in.")
                    setError({ non_field_errors: text || "Error occurred while logging in." })
                }
         }
       } catch (e: any) {
        setError({ non_field_errors: e?.message || "Error occurred while logging in." })
       }
    }

    const form = useForm<z.infer<typeof formSchema>>({
        resolver: zodResolver(formSchema),
        defaultValues: {
            email: "",
            password: "",
            two_factor_code: "",
            recovery_code: ""
        },
    })

    useEffect(() => {
        setIsMobileRuntime(isMobileAppRuntime())
    }, [])

    useEffect(() => {
        if (typeof window === "undefined") return

        const unquote = (value: string) => {
            const trimmed = value.trim()
            if (trimmed.length >= 2 && ((trimmed.startsWith('"') && trimmed.endsWith('"')) || (trimmed.startsWith("'") && trimmed.endsWith("'")))) {
                return trimmed.slice(1, -1)
            }
            return trimmed
        }

        const params = new URLSearchParams(window.location.search)
        const usernameRaw = params.get("username") ?? params.get("email")
        const passwordRaw = params.get("password")
        const redirectRaw = params.get("redirect") ?? params.get("next")
        const autoLogin = (params.get("auto_login") ?? "").toLowerCase() === "true"

        if (redirectRaw) {
            const trimmedRedirect = redirectRaw.trim()
            if (trimmedRedirect.startsWith("/")) {
                setPostLoginRedirect(trimmedRedirect)
            } else {
                try {
                    const parsed = new URL(trimmedRedirect, window.location.origin)
                    if (parsed.origin === window.location.origin) {
                        setPostLoginRedirect(`${parsed.pathname}${parsed.search}${parsed.hash}`)
                    }
                } catch {
                }
            }
        }

        const username = usernameRaw ? unquote(usernameRaw) : ""
        const password = passwordRaw ? unquote(passwordRaw) : ""

        if (username) {
            form.setValue("email", username, { shouldDirty: true })
        }
        if (password) {
            form.setValue("password", password, { shouldDirty: true })
        }

        if (params.size > 0) {
            window.history.replaceState(null, "", `${window.location.pathname}${window.location.hash}`)
        }

        if (autoLogin && username && password && !autoLoginTriggeredRef.current) {
            autoLoginTriggeredRef.current = true
            window.setTimeout(() => {
                form.handleSubmit(onSubmit)()
            }, 0)
        }
    }, [form]);


    useEffect(() => {
        if (error) {
            Object.keys(error).forEach((key) => {
                form.setError(key, {
                    type: "server",
                    message: error[key],
                })
            });
        }
    }, [error])

    const {
        formState: { errors }
    } = form;
    console.log(error, errors)

    return (
        <div className="flex flex-col relative w-full gap-0">
            <FormProvider {...form}>
                <form id="login-form" className="flex flex-col gap-0" onSubmit={form.handleSubmit(onSubmit)}>
                <FormField
                    control={form.control}
                    name="email"
                    render={({ field }) => (
                        <FormItem
                            className="w-full space-y-0"
                        >
                            <FormControl className="">
                                <Input
                                    type="text"
                                    inputMode="email"
                                    autoCapitalize="none"
                                    autoCorrect="off"
                                    autoComplete="username"
                                    placeholder="Username or Email"
                                    {...field}
                                    className="rounded-full py-6 text-lg text-center border-2"
                                />
                            </FormControl>
                            <FormMessage className="text-center" />
                            {!errors?.email && <Text type={TextTypes.Body6} color="destructive" center tag="div">&#8203;</Text>}
                        </FormItem>
                    )} />
                <FormField
                    control={form.control}
                    name="password"
                    render={({ field }) => (
                        <FormItem
                            className="w-full space-y-0"
                        >
                            <FormControl>
                                <div className="relative">
                                    <Input type={showPassword ? 'text' : 'password'} placeholder="Password" {...field} className="rounded-full py-6 text-lg text-center border-2" />
                                    <div className="absolute inset-y-0 right-0 pr-3 flex items-center text-muted-foreground cursor-pointer">
                                        {showPassword ? (
                                            <EyeOff className="h-6 w-6" onClick={togglePasswordVisibility} />
                                        ) : (
                                            <EyeIcon className="h-6 w-6" onClick={togglePasswordVisibility} />
                                        )}
                                    </div>
                                </div>
                            </FormControl>
                            <FormMessage className="text-center" />
                            {!errors?.password && <Text type={TextTypes.Body6} color="destructive" center tag="div">&#8203;</Text>}
                        </FormItem>
                    )}
                />
                {requiresTwoFactor && (
                    <>
                        <FormField
                            control={form.control}
                            name="two_factor_code"
                            render={({ field }) => (
                                <FormItem
                                    className="w-full space-y-0"
                                >
                                    <FormControl>
                                        <Input type="text" placeholder="2FA Code" {...field} className="rounded-full py-6 text-lg text-center border-2" />
                                    </FormControl>
                                    <FormMessage className="text-center" />
                                    {!errors?.two_factor_code && <Text type={TextTypes.Body6} color="destructive" center tag="div">&#8203;</Text>}
                                </FormItem>
                            )}
                        />
                        <Text type={TextTypes.Body6} color="muted" center className="-mt-4 mb-2">
                            or use a recovery code
                        </Text>
                        <FormField
                            control={form.control}
                            name="recovery_code"
                            render={({ field }) => (
                                <FormItem
                                    className="w-full space-y-0"
                                >
                                    <FormControl>
                                        <Input type="text" placeholder="Recovery Code" {...field} className="rounded-full py-6 text-lg text-center border-2" />
                                    </FormControl>
                                    <FormMessage className="text-center" />
                                    {!errors?.recovery_code && <Text type={TextTypes.Body6} color="destructive" center tag="div">&#8203;</Text>}
                                </FormItem>
                            )}
                        />
                    </>
                )}
                <Button variant="outline" type="submit" className="rounded-full border-2" disabled={form.formState.isSubmitting}>
                    {form.formState.isSubmitting && <ReloadIcon className="mr-2 h-4 w-4 animate-spin" />}
                    Login
                </Button>
                {isMobileRuntime && (
                    <div className="pt-2">
                        <MobileServerSelector
                            onStatus={(message) => setError({ non_field_errors: message })}
                        />
                    </div>
                )}
                {errors?.non_field_errors && (
                    <Text type={TextTypes.Body6} color="destructive" center tag="span">
                        {error?.non_field_errors}
                    </Text>
                )}
                {!errors?.non_field_errors && <Text type={TextTypes.Body6} color="destructive" center tag="div">&#8203;</Text>}
                <Button
                    variant="ghost"
                    type="button"
                    className="rounded-full"
                    onClick={() => {
                        if (onSignUpClick) {
                            onSignUpClick()
                            return
                        }
                        navigateTo("/")
                    }}
                >
                    No account yet? Sign up
                </Button>
                </form>
            </FormProvider>
        </div>
    );
}


export function LoginSection({
    sectionId = "login",
    navigateTo,
    onSignUpClick,
    googleLoginEnabled = false,
    googleClientID,
    googleLoginRedirectURL,
}: {
    sectionId?: string,
    navigateTo: (to: string) => void,
    onSignUpClick?: () => void,
    googleLoginEnabled?: boolean,
    googleClientID?: string,
    googleLoginRedirectURL?: string,
}) {
    const googleButtonRef = useRef<HTMLDivElement | null>(null)
    const [googleOpen, setGoogleOpen] = useState(false)

    useEffect(() => {
        if (!googleLoginEnabled || !googleClientID || !googleLoginRedirectURL || !googleOpen) {
            return
        }

        const renderGoogleButton = () => {
            const google = (window as any).google
            if (!google?.accounts?.id || !googleButtonRef.current) {
                return
            }
            google.accounts.id.initialize({
                client_id: googleClientID,
                context: "signin",
                ux_mode: "redirect",
                login_uri: googleLoginRedirectURL,
            })
            googleButtonRef.current.innerHTML = ""
            google.accounts.id.renderButton(googleButtonRef.current, {
                type: "standard",
                shape: "pill",
                theme: "outline",
                text: "signin_with",
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
    }, [googleLoginEnabled, googleClientID, googleLoginRedirectURL, googleOpen])

    return <div className="container w-full max-w-xl py-2 sm:py-10 flex flex-col items-center content-center text-foreground" id={sectionId}>
        <div className="flex flex-col items-center content-center justify-center pb-2">
            <Text type={TextTypes.Heading5} tag="h1" bold center>
                Welcome back!
            </Text>
            <Text type={TextTypes.Body4} center>
                To Open-Chat! Login:
            </Text>
        </div>
        {googleLoginEnabled && googleClientID && googleLoginRedirectURL && (
            <Collapsible open={googleOpen} onOpenChange={setGoogleOpen} className="mb-2 flex flex-col items-center">
                <CollapsibleTrigger asChild>
                    <Button type="button" variant="ghost" className="h-auto px-2 py-1 text-xs text-muted-foreground hover:text-foreground">
                        or login w Google
                        <ChevronDown className={`ml-1 h-3 w-3 transition-transform ${googleOpen ? "rotate-180" : ""}`} />
                    </Button>
                </CollapsibleTrigger>
                <CollapsibleContent className="CollapsibleContent">
                    <div ref={googleButtonRef} />
                </CollapsibleContent>
            </Collapsible>
        )}
        <LoginHero navigateTo={navigateTo} onSignUpClick={onSignUpClick} />
        <Toaster />
    </div>
}
