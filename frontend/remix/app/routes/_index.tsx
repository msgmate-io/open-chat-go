import type { MetaFunction } from "@remix-run/node";
import { LandingHero } from "@/components/sections/LandingPage";
import { useNavigate } from "@remix-run/react";

export const meta: MetaFunction = () => {
  return [
    { title: "New Remix App" },
    { name: "description", content: "Welcome to Remix!" },
  ];
};

export default function Index() {
  const navigate = useNavigate()
  return (
    <LandingHero navigateTo={(to: string) => {navigate(to)}} />
  );
}