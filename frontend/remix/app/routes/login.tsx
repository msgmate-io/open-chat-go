import { LandingHero } from "@/components/sections/LandingPage";
import { useTabs } from "@/components/sections/LandingPage";
import { useNavigate } from "@remix-run/react";
import { useEffect } from "react";

export default function Login() {
  const tab = useTabs(state => state.tab);
  const setTab = useTabs(state => state.setTab);
  const navigate = useNavigate()
  
  useEffect(() => {
    setTab("login");
  }, [setTab]);

  return <LandingHero navigateTo={(to: string) => {navigate(to)}} />;
}

// Disable server-side rendering for this route
export const handle = {
  hydrate: true,
}; 