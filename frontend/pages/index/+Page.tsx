import React from "react";
import { navigate } from 'vike/client/router'
import { LandingHero } from "@/components/sections/LandingPage"
import { useTabs } from "@/components/sections/LandingPage";
import { useEffect } from "react";

export default function Page() {
  const tab = useTabs(state => state.tab);
  const setTab = useTabs(state => state.setTab);

  useEffect(() => {
    setTab("index");
  }, [tab]);

  return  <LandingHero navigateTo={(to: string) => {navigate(to)}} />
}
