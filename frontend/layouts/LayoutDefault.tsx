import { useThemeStore } from "@/components/ThemeToggle";
import "./style.css";
import "./tailwind.css";
import React from "react";

export default function LayoutDefault({ children }: { children: React.ReactNode }) {
  const theme = useThemeStore((state) => state.theme);
  return (
    <div id="theme-root" data-theme={theme}>{children}</div>
  )
}
