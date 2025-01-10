import {
  Links,
  Meta,
  Outlet,
  Scripts,
  ScrollRestoration,
} from "@remix-run/react";
import image from "../public/logo.png"
import type { LinksFunction } from "@remix-run/node";
import { useThemeStore } from "@/components/ThemeToggle";

import "./tailwind.css";

export const links: LinksFunction = () => [
  { rel: "preconnect", href: "https://fonts.googleapis.com" },
  {
    rel: "preconnect",
    href: "https://fonts.gstatic.com",
    crossOrigin: "anonymous",
  },
  {
    rel: "stylesheet",
    href: "https://fonts.googleapis.com/css2?family=Inter:ital,opsz,wght@0,14..32,100..900;1,14..32,100..900&display=swap",
  },
];

export function Layout({ children, theme }: { children: React.ReactNode, theme?: string }) {
  return (
    <html lang="en" data-theme={theme}>
      <head>
        <meta charSet="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <Meta />
        <Links />
      </head>
      <body>
        {children}
        <ScrollRestoration />
        <Scripts />
      </body>
    </html>
  );
}

export default function App() {
  const theme = useThemeStore(state => state.theme);
  // return <HydrateFallback />
  return (
    <Layout theme={theme}>
      <Outlet />
    </Layout>
  );
}

export function HydrateFallback() {
  return (
    <div className="min-h-screen w-full flex items-center justify-center">
      <div
        className="w-[420px] h-[420px] relative"
      >
        <div className="absolute inset-0"
          style={{
            backgroundImage: `url(${image})`,
            backgroundSize: 'cover',
            filter: 'grayscale(100%)',
            opacity: '0.1'
          }}
        />
        <div className="absolute inset-0 z-30"
          style={{
            backgroundImage: `url(${image})`,
            backgroundSize: 'cover',
            filter: 'grayscale(100%)',
            opacity: '0.1'
          }}
        />
      </div>
    </div>
  );
}
