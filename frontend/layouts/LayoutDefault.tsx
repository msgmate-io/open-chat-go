import { useThemeStore } from "@/components/ThemeToggle";
import "./style.css";
import "./tailwind.css";
import React, { useEffect } from "react";
import { cn } from "@/lib/utils";

export default function LayoutDefault({ children }: { children: React.ReactNode }) {
  const theme = useThemeStore((state) => state.theme);
  
  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme);
    document.getElementById('theme-root')?.setAttribute('data-theme', theme);
  }, [])

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme);
    document.getElementById('theme-root')?.setAttribute('data-theme', theme);
  }, [theme])
  
  return (
    <div className={cn(theme, 'bg-background')}>{children}</div>
  )
}
