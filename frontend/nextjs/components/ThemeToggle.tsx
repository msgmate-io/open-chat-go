"use client"

import { StateStorage, PersistStorage, StorageValue } from 'zustand/middleware';
import { getCookie, setCookie, removeCookie } from 'typescript-cookie';
import { Moon, Sun } from "lucide-react";
import React, { useEffect } from 'react';
const THEMES = ["dark", "light", "cupcake", "retro"]
import { Button } from "@/components/Button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/DropdownMenu"

import { create } from 'zustand'
import { devtools, persist } from 'zustand/middleware'

interface ThemeState {
  theme: string
  changeTheme: (theme: string) => void
}


const cookiesStorage: PersistStorage<ThemeState> = {
  getItem: (name: string) => {
    const value = getCookie(name);
    return value ? JSON.parse(value) : null;
  },
  setItem: (name: string, value: StorageValue<ThemeState>) => {
    setCookie(name, JSON.stringify(value), { expires: 1 });
  },
  removeItem: (name: string) => {
    removeCookie(name);
  }
}

export const useThemeStore = create<ThemeState>()(
  devtools(
    persist(
      (set) => ({
        theme: "dark",
        changeTheme: (theme) => {
          document.documentElement.setAttribute('data-theme', theme);
          set({ theme })
        },
      }),
      {
        name: 'theme-store',
        storage: cookiesStorage,
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
            changeTheme(theme)
          }}>
            {theme}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
