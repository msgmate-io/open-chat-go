import type { Metadata } from "next";
import "./globals.css";
import { Layout } from "@/next-components/Layout";

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
