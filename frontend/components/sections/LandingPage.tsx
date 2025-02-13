"use client"

import React, { useState, useEffect } from "react";

import { CinematicLogo } from "@/components/CinematicLogo"
import { Typewriter } from "@/components/TypewriterEffect"
import { ModeToggle } from "@/components/ThemeToggle"
import { Button } from "@/components/Button"
import { create } from 'zustand'
import { devtools } from 'zustand/middleware'
import { LoginSection } from "@/components/sections/LoginSection"
import { Cookies } from "typescript-cookie";


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

const TEXTS = [{
    model: "cognitivecomputations/dolphin-2.6-mixtral-8x7b",
    prompt: "\"Why is open-chat different than other LLM chat interfaces?\"",
    completion: "Open-chat is different due to its decentralized, self-hostable nature and support for both proprietary and open-source AI backend models. It also offers a full API endpoint for chat-based interactions, unlike many other LLM chat interfaces. This gives users unique features like running LLM completions via GPU at home!"
}, {
    model: "gpt-4o",
    prompt: "\"Name a thing that open-chat can do that OpenAI's chat GPT can't.\"",
    completion: "Open-Chat can integrate with and self-host various open-source models, allowing for more customizable and private AI interactions."
}, {
    model: "meta-llama/Meta-Llama-3-8B-Instruct",
    prompt: "\"How can open-chat bots be used?\"",
    completion: "Open-chat bots can be used like regular users, authenticating with the server and joining websocket channels. Bot logic is encapsulated within the bot itself, allowing any bot to serve any purpose, such as processing commands, generating content, or even creating complex conversations."
}, {
    model: "databricks/dbrx-instruct",
    prompt: "\"Why should I use open chat if I could just use ChatGpt?\"",
    completion: "You should use Open Chat if you want more control and flexibility over your AI interactions. Open Chat is self-hostable, compatible with various proprietary services and open-source LLMs, and allows users to run AI bots and LLMs on their own devices or choose from pre-hosted services without exposing your API keys to the world. Also, Open Chat is designed to support any LLM backend or service."
}, {
    model: "llama3-70b-8192",
    prompt: "\"What is open-chat's audio chat feature?\"",
    completion: "Open-Chat's audio chat feature allows users to engage in voice conversations with AI bots, offering a more human-like interaction experience."
}]

function DefaultFooter() {
    return <>
        <div className="flex flex-col items-center justify-center content-center w-full">
            <div>homepage</div>
        </div>
        <div className="flex flex-row items-center justify-center content-center w-full p-2">
            <div className="flex w-1/2 justify-end">
                <Button variant="ghost" className="h-5 p-1 hover:text-neutral">Therms of use</Button>
            </div>
            |
            <div className="flex w-1/2 justify-start">
                <Button variant="ghost" className="h-5 p-1">Privacy policy</Button>
            </div>
        </div>
    </>
}

function IndexTab({
    navigateTo
}: {
    navigateTo: (to: string) => void
}) {
    const setTab = useTabs(state => state.setTab)
    return <>
        <CinematicLogo className={"mr-8"} size={420} />
        <div className="flex flex-row items-center justify-center content-center w-full relative">
            <div className="flex w-1/2 justify-end pr-4">
                <Button variant="ghost" className="rounded-full border py-8 text-xl text-bold border-2" onClick={() => {
                    setTab("register")
                }}>Sign-up</Button>
            </div>
            <div className="flex w-1/2 justify-start pl-4">
                <Button variant="ghost" className="rounded-full border py-8 text-xl text-bold border-2" onClick={() => {
                    // check if the user has a current session cookie
                    const isAuthorized = Cookies.get("is_authorized")
                    // check if the cookies is not expired
                    console.log("SID", isAuthorized)
                    if (isAuthorized === "true") {
                        navigateTo("/chat")
                    } else {
                        navigateTo("/login")
                    }
                }}>Log-in</Button>
            </div>
        </div>
    </>
}

function RegisterTab(){
    return <div>
        <div className="flex flex-col items-center content-center justify-center pb-2">
            <h1 className="text-2xl font-bold text-center">No Sign-up Yet!</h1>
            <p className="text-lg text-center">
                We are in closed beta, you can contact{" "}
                <a href="mailto:tim@msmate.io" className="text-blue-500 text-bold">
                    tim@msgmate.io
                </a>{" "}
                to get early access.
            </p>
        </div>
    </div>
}

export function LandingHero({
    navigateTo
}: {
    navigateTo: (to: string) => void
}) {
    const tab = useTabs(state => state.tab)
    const setTab = useTabs(state => state.setTab)

    return <>
        <div className="flex relative w-full z-40">
            <div className="absolute flex w-full p-4 font-bold text-2xl w-full">
                Open-Chat
                <div className="text-sm text-gray-500 mt-3 ml-1">by Msgmate.io (beta)</div>
            </div>
        </div>
        <div className="flex flex-row items-center justify-center content-center h-screen">
            <div className="flex flex-col flex-grow items-center justify-center content-center bg-background w-2/3 h-full shadow-xl z-10 lg:flex relative">
                <Typewriter
                    typingSpeed={10}
                    texts={TEXTS} />
            </div>
            <div className="flex flex-col flex-grow items-center justify-center content-center bg-secondary w-1/3 h-full">
                <div className="flex flex-row items-end justify-end content-center w-full">
                    <div className="p-2 hover:bg-secondary rounded-xl z-40" onClick={() => {
                        setTab("index")
                    }}>
                        {tab !== "index" && <div className="p-1"><ModeToggle /></div>}
                        {tab === "index" && <ModeToggle />}
                    </div>
                </div>
                <div className="flex flex-col items-center justify-center content-center w-full flex-grow p-4">
                    {tab === "index" && <IndexTab navigateTo={navigateTo}/>}
                    {tab === "login" && <LoginSection navigateTo={navigateTo}/>}
                    {tab === "register" && <RegisterTab/>}
                </div>
                <DefaultFooter />
            </div>
        </div>
    </>
}
