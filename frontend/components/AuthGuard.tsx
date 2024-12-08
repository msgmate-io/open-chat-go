"use client"

import { ReactNode, useEffect } from 'react'
import { create } from 'zustand'
import { devtools, persist } from 'zustand/middleware'
import { headers, cookies } from "next/headers";

interface AuthState {
  isAuthenticated: boolean
  setIsAuthenticated: (isAuthenticated: boolean) => void
}


export const useAuthStore = create<AuthState>()(
  devtools(
      (set) => ({
        isAuthenticated: false,
        setIsAuthenticated: (isAuthenticated: boolean) => {
          set({ isAuthenticated })
        },
      }),
  ),
)

export async function AuthGuard() {
    const heads = await headers() 
    const isAuthenticated = heads.get("x-user-authenticated") == "true"
    const setIsAuthenticated = useAuthStore(state => state.setIsAuthenticated)
    useEffect(() => {
        setIsAuthenticated(isAuthenticated)
    }, [])
    return <></>
}