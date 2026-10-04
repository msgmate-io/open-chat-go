"use client"

import React, { useEffect, useState } from "react";

import { BookOpen, PlayCircle } from "lucide-react"
import { CinematicLogo } from "@/components/CinematicLogo"
import { ConnectedThemeSelector } from "@open-chat-go/ui"
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  Text,
  TextTypes,
  Typewriter,
  openChatLandingTypewriterTexts,
} from "@open-chat-go/ui"
import { create } from 'zustand'
import { devtools } from 'zustand/middleware'
import { LoginSection } from "@/components/sections/LoginSection"
import RegisterSection from "@/components/sections/RegisterSection"
import EmailVerificationSection from "@/components/sections/EmailVerificationSection"
import SignupRequestSentSection from "@/components/sections/SignupRequestSentSection"
import { Cookies } from "typescript-cookie";
import useSWR from "swr";
import { fetcher } from "@/lib/utils";
import { MobileServerSelector } from "@/components/mobile/MobileServerSelector";
import { isMobileAppRuntime } from "@open-chat-go/ui";


interface TabState {
    tab: string
    setTab: (tab: string) => void
}

export const useTabs = create<TabState>()(
      devtools(
          (set) => ({
            tab: "index",
            setTab: (tab) => {
              set({ tab })
            },
          }),
        )
    )

const TEXTS = openChatLandingTypewriterTexts

type AccountManagementPublicConfig = {
    signup_enabled?: boolean
    signup_requires_admin_approval?: boolean
    google_signup_enabled?: boolean
    google_client_id?: string
    google_signup_redirect_url?: string
    google_signup_start_url?: string
    require_email_verification?: boolean
}

function GenerateTextForLoggedInUser(user: { name?: string }) {
    return [{
        model: "cognitivecomputations/dolphin-2.6-mixtral-8x7b",
        prompt: "Starting a new conversation",
        completion: `Welcome back ${user?.name}! Ready to explore some advanced AI capabilities together?`
    }, {
        model: "gpt-4o",
        prompt: "Greeting the user",
        completion: `Great to see you again ${user?.name}! What would you like to work on today?`
    }, {
        model: "meta-llama/Meta-Llama-3-8B-Instruct",
        prompt: "User returns to chat",
        completion: `${user?.name} has returned! Let's make something amazing happen.`
    }, {
        model: "databricks/dbrx-instruct",
        prompt: "Welcoming user back",
        completion: `Welcome back to Open Chat, ${user?.name}! Whether you want to chat, create, or analyze - I'm here to help.`
    }, {
        model: "llama3-70b-8192",
        prompt: "Personal greeting",
        completion: `Hello ${user?.name}! Looking forward to another productive conversation. What's on your mind?`
    }]
}

const footerLinkClass =
  "h-auto px-2 py-1 text-sm font-normal text-muted-foreground shadow-none hover:bg-muted hover:text-foreground";
const SHOW_LANDING_FOOTER_LINKS = false

// Landing release-announcement video (played by the floating
// "Watch Release Announcement" button).
const LANDING_DEMO_VIDEO_URL = "https://youtu.be/k4_KK-o8jEU"

function toYouTubeEmbedUrl(url: string): string {
    if (!url) return ""
    try {
        const parsed = new URL(url)
        const id = parsed.hostname.includes("youtu.be")
            ? parsed.pathname.slice(1)
            : parsed.searchParams.get("v") ?? parsed.pathname.split("/").pop() ?? ""
        return id ? `https://www.youtube.com/embed/${id}?autoplay=1&playsinline=1&rel=0` : ""
    } catch {
        return ""
    }
}

function DefaultFooter({ navigateTo }: { navigateTo: (to: string) => void }) {
    if (!SHOW_LANDING_FOOTER_LINKS) {
        return null
    }

    return (
        <div className="flex flex-row flex-wrap items-center justify-center gap-x-1 gap-y-1 w-full p-2">
            <Button
                variant="ghost"
                className={footerLinkClass}
                onClick={() => navigateTo("/docs")}
            >
                Documentation
            </Button>
            <span className="text-muted-foreground/40 select-none" aria-hidden="true">|</span>
            <Button
                variant="ghost"
                className={footerLinkClass}
                asChild
            >
                <a
                    href="https://github.com/msgmate-io/open-chat-go"
                    target="_blank"
                    rel="noopener noreferrer"
                >
                    Repository
                </a>
            </Button>
            <span className="text-muted-foreground/40 select-none" aria-hidden="true">|</span>
            <Button
                variant="ghost"
                className={footerLinkClass}
                asChild
            >
                <a
                    href="https://msgmate-io.github.io/open-chat-go/"
                    target="_blank"
                    rel="noopener noreferrer"
                >
                    Design System
                </a>
            </Button>
        </div>
    )
}

function IndexTab({
    hasSignupEnabled,
    navigateTo
}: {
    hasSignupEnabled: boolean,
    navigateTo: (to: string) => void
}) {
    const setTab = useTabs(state => state.setTab)
    const { data: user, isLoading, error } = useSWR(`/api/v1/user/self`, fetcher)
    const [isLoggedIn, setIsLoggedIn] = useState(false)
    const [isMobileRuntime, setIsMobileRuntime] = useState(false)

    useEffect(() => {
        setIsLoggedIn(
            Cookies.get("oc_client_state") === "true" && !isLoading && !error && !!user,
        )
    }, [isLoading, error, user])

    useEffect(() => {
        setIsMobileRuntime(isMobileAppRuntime())
    }, [])

    const pillClass = "rounded-full md:py-6 md:text-xl text-md py-4 font-bold w-full max-w-[240px] bg-transparent text-secondary-foreground hover:bg-accent hover:text-accent-foreground"

    return <div className="flex w-full flex-col items-center justify-center">
        {isLoggedIn ? (
            <>
                <div className="mb-5 md:hidden">
                    <div className="flex flex-col items-center gap-3 rounded-3xl bg-secondary/70 p-5">
                        <CinematicLogo className={"pointer-events-none"} size={330} />
                        <Button variant="ghost" className={pillClass} onClick={() => navigateTo("/chat")}>Open-Chat</Button>
                        {isMobileRuntime ? (
                            <div className="w-full max-w-[260px]">
                                <MobileServerSelector className="w-full" />
                            </div>
                        ) : null}
                    </div>
                </div>
                <div className="mb-6 hidden md:block">
                    <div className="flex flex-col items-center gap-4 rounded-3xl bg-secondary/70 p-6">
                        <CinematicLogo className={"pointer-events-none"} size={480} />
                        <Button variant="ghost" className={pillClass} onClick={() => navigateTo("/chat")}>Open-Chat</Button>
                        {isMobileRuntime ? (
                            <div className="w-full max-w-[260px]">
                                <MobileServerSelector className="w-full" />
                            </div>
                        ) : null}
                    </div>
                </div>
            </>
        ) : (
            <>
                <div className="mb-5 md:hidden" style={{ perspective: "1400px" }}>
                    <div
                        className="group relative"
                        style={{ width: "330px", height: "330px" }}
                        tabIndex={0}
                    >
                        <div className="relative h-full w-full transition-transform duration-700 [transform-style:preserve-3d] group-hover:[transform:rotateY(180deg)] group-focus:[transform:rotateY(180deg)] group-focus-within:[transform:rotateY(180deg)]">
                            <div className="absolute inset-0 flex items-center justify-center [backface-visibility:hidden]">
                                <CinematicLogo className={"pointer-events-none"} size={330} />
                            </div>
                            <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 rounded-3xl bg-secondary/70 p-5 [backface-visibility:hidden] [transform:rotateY(180deg)]">
                                {hasSignupEnabled && <Button variant="ghost" className={pillClass} onClick={() => {
                                    navigateTo("/sign-up")
                                }}>Sign-up</Button>}
                                <Button variant="ghost" className={pillClass} onClick={() => {
                                    const isAuthorized = Cookies.get("oc_client_state")
                                    if (isAuthorized === "true") {
                                        navigateTo("/chat")
                                    } else {
                                        navigateTo("/login")
                                    }
                                }}>{(isLoading || error) ? "Log-In" : "Go-To-Chat"}</Button>
                            </div>
                        </div>
                    </div>
                </div>
                <div className="mb-6 hidden md:block" style={{ perspective: "1700px" }}>
                    <div
                        className="group relative"
                        style={{ width: "480px", height: "480px" }}
                        tabIndex={0}
                    >
                        <div className="relative h-full w-full transition-transform duration-700 [transform-style:preserve-3d] group-hover:[transform:rotateY(180deg)] group-focus:[transform:rotateY(180deg)] group-focus-within:[transform:rotateY(180deg)]">
                            <div className="absolute inset-0 flex items-center justify-center [backface-visibility:hidden]">
                                <CinematicLogo className={"pointer-events-none"} size={480} />
                            </div>
                            <div className="absolute inset-0 flex flex-col items-center justify-center gap-4 rounded-3xl bg-secondary/70 p-6 [backface-visibility:hidden] [transform:rotateY(180deg)]">
                                {hasSignupEnabled && <Button variant="ghost" className={pillClass} onClick={() => {
                                    navigateTo("/sign-up")
                                }}>Sign-up</Button>}
                                <Button variant="ghost" className={pillClass} onClick={() => {
                                    const isAuthorized = Cookies.get("oc_client_state")
                                    if (isAuthorized === "true") {
                                        navigateTo("/chat")
                                    } else {
                                        navigateTo("/login")
                                    }
                                }}>{(isLoading || error) ? "Log-In" : "Go-To-Chat"}</Button>
                            </div>
                        </div>
                    </div>
                </div>
            </>
        )}
    </div>
}

function RegisterTab({
    hasSignupEnabled,
    config,
    navigateTo,
}: {
    hasSignupEnabled: boolean,
    config?: AccountManagementPublicConfig,
    navigateTo: (to: string) => void,
}){
    if (hasSignupEnabled) {
        return <RegisterSection
            navigateTo={navigateTo}
            signupRequiresAdminApproval={Boolean(config?.signup_requires_admin_approval)}
            googleSignupEnabled={Boolean(config?.google_signup_enabled)}
            googleClientID={config?.google_client_id}
            googleSignupRedirectURL={config?.google_signup_redirect_url}
        />
    }

    return <div>
        <div className="flex flex-col items-center content-center justify-center pb-2 text-secondary-foreground">
            <Text type={TextTypes.Heading5} tag="h1" bold center>
                No Sign-up Yet!
            </Text>
            <Text type={TextTypes.Body4} center className="mt-2">
                We are in closed beta, you can contact{" "}
                <a href="mailto:tim@msgmate.io" className="text-primary font-bold hover:underline">
                    tim@msgmate.io
                </a>{" "}
                to get early access.
            </Text>
        </div>
    </div>
}

function SignupRequestTab({
    navigateTo,
}: {
    navigateTo: (to: string) => void,
}) {
    const params = typeof window !== "undefined" ? new URLSearchParams(window.location.search) : null
    const email = params?.get("email") || undefined
    return <SignupRequestSentSection navigateTo={navigateTo} email={email} />
}

export function LandingHero({
    navigateTo
}: {
    navigateTo: (to: string) => void
}) {
    const tab = useTabs(state => state.tab)
    const setTab = useTabs(state => state.setTab)
    const { data: user, isLoading, error } = useSWR(`/api/v1/user/self`, fetcher)
    const { data: accountManagementConfig, error: accountManagementConfigError } = useSWR<AccountManagementPublicConfig>(
        "/api/v1/integrations/account_management/config",
        fetcher,
    )
    const { data: versionData } = useSWR(`/api/version`, fetcher)
    const [isVideoOpen, setIsVideoOpen] = useState(false)
    const demoEmbedUrl = toYouTubeEmbedUrl(LANDING_DEMO_VIDEO_URL)
    const hasSignupEnabled = accountManagementConfig?.signup_enabled === true
    const signupGateResolved = accountManagementConfig !== undefined || Boolean(accountManagementConfigError)

    useEffect(() => {
        if (!signupGateResolved) {
            return
        }
        if (!hasSignupEnabled && tab === "register") {
            setTab("index")
        }
        if ((!hasSignupEnabled || !Boolean(accountManagementConfig?.signup_requires_admin_approval)) && tab === "signup-request-send") {
            setTab("index")
        }
    }, [accountManagementConfig?.signup_requires_admin_approval, hasSignupEnabled, setTab, signupGateResolved, tab])

    return <>
        <div className="flex relative w-full z-40">
            <div className="absolute flex w-full flex-col md:flex-row items-start p-4 text-foreground">
                <Text type={TextTypes.Heading5} tag="div" bold>
                    Open-Chat
                </Text>
                <Text type={TextTypes.Body6} color="muted" className="mt-1 ml-0 md:mt-3 md:ml-1">
                    and Service Msgmate.io by TBS (beta-{versionData?.version || 'loading...'})
                </Text>
            </div>
        </div>
        <div className="flex md:flex-row flex-col items-center justify-center content-center h-full">
            <div className="hidden md:flex flex-col flex-grow items-center justify-center content-center bg-background md:w-2/3 h-full shadow-xl z-10 relative text-foreground">
                <Typewriter
                    fullHeight
                    typingSpeed={10}
                    persistKey="landing-typewriter"
                    texts={(isLoading || error) ? TEXTS : GenerateTextForLoggedInUser(user)}
                />
            </div>
            <div className="flex flex-col flex-grow items-center justify-center content-center bg-secondary text-secondary-foreground w-full md:w-1/3 h-full">
                <div className="flex flex-row items-end justify-end content-center w-full">
                    <div className="p-2 hover:bg-accent rounded-xl z-40">
                        <ConnectedThemeSelector variant="icon-dropdown" />
                    </div>
                </div>
                <div className="flex flex-col items-center justify-center content-center w-full flex-grow p-4">
                    {tab === "index" && <IndexTab hasSignupEnabled={hasSignupEnabled} navigateTo={navigateTo}/>} 
                    {tab === "login" && <LoginSection
                        navigateTo={navigateTo}
                        onSignUpClick={() => navigateTo("/sign-up")}
                        googleLoginEnabled={Boolean(accountManagementConfig?.google_signup_enabled)}
                        googleClientID={accountManagementConfig?.google_client_id}
                        googleLoginRedirectURL={accountManagementConfig?.google_signup_redirect_url}
                    />}
                    {tab === "register" && <RegisterTab hasSignupEnabled={hasSignupEnabled} config={accountManagementConfig} navigateTo={navigateTo} />}
                    {tab === "signup-request-send" && <SignupRequestTab navigateTo={navigateTo} />}
                    {tab === "email-verification" && <EmailVerificationSection navigateTo={navigateTo} />}
                </div>
                <DefaultFooter navigateTo={navigateTo} />
            </div>
        </div>
        <div className="fixed bottom-3 left-3 z-50 flex items-center gap-2">
            <Button
                variant="ghost"
                size="sm"
                className="h-7 gap-1.5 rounded-full border border-border/60 bg-background/70 px-2.5 text-xs font-normal text-muted-foreground shadow-sm backdrop-blur-sm hover:bg-accent hover:text-accent-foreground"
                onClick={() => navigateTo("/docs")}
            >
                <BookOpen className="size-3.5" aria-hidden="true" />
                Documentation
            </Button>
        </div>
        <div className="fixed bottom-3 right-3 md:right-1/3 z-50">
            <div className="relative isolate">
                <span aria-hidden="true" className="landing-video-glow" />
                <Button
                    variant="ghost"
                    size="sm"
                    className="relative h-7 gap-1.5 rounded-full border border-border/60 bg-background/80 px-2.5 text-xs font-normal text-foreground shadow-sm backdrop-blur-sm hover:bg-accent hover:text-accent-foreground"
                    onClick={() => setIsVideoOpen(true)}
                >
                    <PlayCircle className="size-3.5" aria-hidden="true" />
                    Watch Release Announcement
                </Button>
            </div>
        </div>
        <Dialog open={isVideoOpen} onOpenChange={setIsVideoOpen}>
            <DialogContent className="max-w-3xl gap-0 overflow-hidden border-border/60 p-0 sm:max-w-3xl">
                <DialogTitle className="sr-only">Open-Chat demo video</DialogTitle>
                <DialogDescription className="sr-only">
                    Introductory demo video for Open-Chat.
                </DialogDescription>
                <div className="aspect-video w-full bg-black">
                    {isVideoOpen && demoEmbedUrl ? (
                        <iframe
                            src={demoEmbedUrl}
                            title="Open-Chat demo video"
                            className="h-full w-full"
                            allow="autoplay; encrypted-media; picture-in-picture"
                            allowFullScreen
                        />
                    ) : (
                        <div className="flex h-full w-full items-center justify-center text-sm text-muted-foreground">
                            Demo video coming soon
                        </div>
                    )}
                </div>
            </DialogContent>
        </Dialog>
    </>
}
