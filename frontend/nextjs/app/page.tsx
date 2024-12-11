"use client"

import { LandingHero } from "@/components/sections/LandingPage";
import { useRouter } from "next/navigation";

export default function Home() {
  const router = useRouter();
  return (
        <LandingHero navigateTo={(to: string) => { router.push(to) }}/>
  );
}
