import { navigate } from 'vike/client/router'
import { LandingHero } from "@/components/sections/LandingPage"
import { useTabs } from "@/components/sections/LandingPage";
import { useEffect } from "react";
import { isDeviceOnlineRuntime } from "@open-chat-go/ui";
import useSWR from "swr";
import { fetcher } from "@/lib/utils";

export default function Page() {
  const setTab = useTabs(state => state.setTab);
  const { data: user, isLoading, error } = useSWR(`/api/v1/user/self`, fetcher);
  
  useEffect(() => {
    setTab("login");
  }, [setTab]);

  useEffect(() => {
    if (!isDeviceOnlineRuntime()) {
      navigate("/chat");
      return;
    }

    if (!isLoading && !error && user) {
      navigate("/chat");
    }
  }, [error, isLoading, user]);

  return  <LandingHero navigateTo={(to: string) => {navigate(to)}} />
}
