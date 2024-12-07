import { ReactNode } from 'react'
import { redirect } from 'next/navigation'

const SERVER_ROUTE = "http://localhost:1984"

export async function AuthGuard({ children }: { children: ReactNode }) {
    const res = await fetch(`${SERVER_ROUTE}/api/v1/user/self`, { method: "GET" })
    
    if(res.ok){
        return children
    }else{
        redirect("/login")
        return null
    }
}