import type { Metadata } from "next";
import "./globals.css";
import { useThemeStore } from "@/components/ThemeToggle";
import { redirect } from 'next/navigation'
import { Layout } from "@/components/Layout";
import { headers, cookies } from "next/headers";

const PUBLIC_ROUTES = ["/"]
const SERVER_ROUTE = "http://localhost:1984"
const AUTH_REDIRECT_ROUTES = ["/login"]

export const metadata: Metadata = {
  title: "Open Chat Go",
  description: "Open Chat Go is a free and open-source AI chat application.",
};

export default async function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  const heads = await headers() 
  const cookieStore = await cookies()
  const pathName = heads.get("x-pathname") || ""
  const sessionIdPresent = cookieStore.get("session_id") || false
  const isPublicRoute = PUBLIC_ROUTES.includes(pathName)
  
  if(isPublicRoute && sessionIdPresent){
    // try to fetch the user using the present session_id
    const res = await fetch(`${SERVER_ROUTE}/api/v1/user/self`, { method: "GET" })
    if (res.ok) {
      redirect("/chat")
    }else{
      // TODO then we should invalidate the current present session
    }
  }

  return (
    <Layout>
        {children}
    </Layout>
  );
}
