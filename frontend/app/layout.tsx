import type { Metadata } from "next";
import "./globals.css";
import { useThemeStore } from "@/components/ThemeToggle";
import { redirect } from 'next/navigation'
import { Layout } from "@/components/Layout";
import { headers, cookies } from "next/headers";

const SERVER_ROUTE = "http://localhost:1984"

const AUTH_REDIRECTS = [{
  expr: new RegExp("/login?"),
  to: "/chat"
}]

const UNAUTH_REDIRECTS = [{
  expr: new RegExp("/chat?"),
  to: "/login"
}]


export const metadata: Metadata = {
  title: "Open Chat Go",
  description: "Open Chat Go is a free and open-source AI chat application.",
};

export default async function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <Layout>
        {children}
    </Layout>
  );
}
