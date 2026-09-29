"use client"

import { create } from "zustand"
import { devtools, persist } from "zustand/middleware"
import { applyTheme, isThemeName, THEMES, type ThemeName } from "@open-chat-go/ui"
import { cookiesStorage } from "../lib/utils"

export { THEMES, type ThemeName, isThemeName, applyTheme }

interface ThemeState {
  theme: ThemeName
  changeTheme: (theme: ThemeName) => void
}

export const useThemeStore = create<ThemeState>()(
  devtools(
    persist(
      (set) => ({
        theme: "dark",
        changeTheme: (theme) => {
          applyTheme(theme)
          set({ theme })
        },
      }),
      {
        name: "theme-store",
        storage: cookiesStorage<ThemeState>(),
      },
    ),
  ),
)
