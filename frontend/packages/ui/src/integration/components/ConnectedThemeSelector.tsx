"use client"

import { ThemeSelector, type ThemeSelectorVariant } from "@open-chat-go/ui"
import { useThemeStore } from "./ThemeToggle"

export function ConnectedThemeSelector({
  variant = "inline",
}: {
  variant?: ThemeSelectorVariant
}) {
  const theme = useThemeStore((state) => state.theme)
  const changeTheme = useThemeStore((state) => state.changeTheme)
  return <ThemeSelector variant={variant} value={theme} onChange={changeTheme} />
}
