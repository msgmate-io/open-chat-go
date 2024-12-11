"use client"

import { useRouter } from "next/navigation";
import { LandingHero } from "@/components/sections/LandingPage";
import { useTabs } from "@/components/sections/LandingPage";
import { useEffect } from "react";

export default function Login() {
  const tab = useTabs(state => state.tab)
  const setTab = useTabs(state => state.setTab)
  const router = useRouter();
  
  useEffect(() => {
    setTab("login")
  }, [])

  return (
        <LandingHero navigateTo={(to: string) => {router.push(to)}}/>
  );
}
