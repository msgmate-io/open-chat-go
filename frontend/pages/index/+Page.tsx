import React from "react";
import { navigate } from 'vike/client/router'
import { LandingHero } from "@/components/sections/LandingPage"

export default function Page() {
  return  <LandingHero navigateTo={(to: string) => {navigate(to)}} />
}
