import React from "react";
import { Counter } from "./Counter.js";
import { navigate } from 'vike/client/router'
import { usePageContext } from 'vike-react/usePageContext'
import { LandingHero } from "@/components/sections/LandingPage"

export default function Page() {
  return  <LandingHero navigateTo={(to: string) => {navigate(to)}} />
}
