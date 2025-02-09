"use client"

import { StateStorage, PersistStorage, StorageValue } from 'zustand/middleware';
import { getCookie, setCookie, removeCookie } from 'typescript-cookie';
import { Moon, Sun } from "lucide-react";
import React, { useEffect } from 'react';
import { Button } from "@/components/Button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/DropdownMenu"

import { create } from 'zustand'
import { devtools, persist } from 'zustand/middleware'
import { cookiesStorage } from '@/lib/utils'
export const THEMES = ["dark", "light", "cupcake", "retro"]

interface ThemeState {
  theme: string
  changeTheme: (theme: string) => void
}

export const useThemeStore = create<ThemeState>()(
  devtools(
    persist(
      (set) => ({
        theme: "dark",
        changeTheme: (theme) => {
          document.documentElement.setAttribute('data-theme', theme);
          document.getElementById('theme-root')?.setAttribute('data-theme', theme);
          set({ theme })
        },
      }),
      {
        name: 'theme-store',
        storage: cookiesStorage<ThemeState>(),
      },
    ),
  ),
)

export function ModeToggle() {
  const changeTheme = useThemeStore(state => state.changeTheme)

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className="ghost"
        >
          <Sun className="h-[1.1rem] w-[1.2rem] rotate-0 scale-100 transition-all dark:-rotate-90 dark:scale-0" />
          <Moon className="absolute h-[1.1rem] w-[1.2rem] rotate-90 scale-0 transition-all dark:rotate-0 dark:scale-100" />
          <span className="sr-only">Toggle theme</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {Object.values(THEMES).map((theme) => (
          <DropdownMenuItem key={theme} onClick={() => {
            console.log("theme", theme)
            changeTheme(theme)
          }}>
            {theme}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
