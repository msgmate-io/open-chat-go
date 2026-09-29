import { AppSidebar } from "@/components/app-sidebar"
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@open-chat-go/ui"
import { Separator } from "@open-chat-go/ui"
import {
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
} from "@open-chat-go/ui"
import { navigate } from "vike/client/router";
import { Toaster } from "@/components/Toaster";
import { Cookies } from "typescript-cookie";
import { useEffect, useState } from "react";

export default function PlatformLayout({
  children,
  descriptor,
  basePath = "/chat",
  baseTitle = "Chat",
  parent,
  allowPublicAccess = false,
}: {
  children: React.ReactNode,
  descriptor: string,
  basePath?: string,
  baseTitle?: string,
  parent?: { title: string; href: string },
  allowPublicAccess?: boolean
}) {
  const [isAuthorized, setIsAuthorized] = useState(!allowPublicAccess);

  useEffect(() => {
    if (allowPublicAccess) {
      setIsAuthorized(Cookies.get("is_authorized") === "true");
    }
  }, [allowPublicAccess]);

  return (
      <SidebarProvider>
        <AppSidebar navigateTo={navigate} accessMode={isAuthorized ? "private" : "public"} />
        <SidebarInset className="h-dvh min-h-0">
          <header className="flex h-16 shrink-0 items-center gap-2 transition-[width,height] ease-linear group-has-data-[collapsible=icon]/sidebar-wrapper:h-12">
            <div className="flex items-center gap-2 px-4">
              <SidebarTrigger className="-ml-1 text-foreground" />
              <Separator
                orientation="vertical"
                className="mr-2 data-[orientation=vertical]:h-4"
              />
              <Breadcrumb>
                <BreadcrumbList>
                  <BreadcrumbItem className="hidden md:block">
                    <BreadcrumbLink href={basePath} className="text-foreground">
                      {baseTitle}
                    </BreadcrumbLink>
                  </BreadcrumbItem>
                  {parent && (
                    <>
                      <BreadcrumbSeparator className="hidden md:block text-foreground" />
                      <BreadcrumbItem className="hidden md:block">
                        <BreadcrumbLink href={parent.href} className="text-foreground">
                          {parent.title}
                        </BreadcrumbLink>
                      </BreadcrumbItem>
                    </>
                  )}
                  <BreadcrumbSeparator className="hidden md:block text-foreground" />
                  <BreadcrumbItem>
                    <BreadcrumbPage className="text-foreground">{descriptor}</BreadcrumbPage>
                  </BreadcrumbItem>
                </BreadcrumbList>
              </Breadcrumb>
            </div>
          </header>
          <div className="flex min-h-0 flex-1 touch-pan-y flex-col gap-4 overflow-y-scroll overscroll-y-contain [-webkit-overflow-scrolling:touch] p-4 pt-0 text-foreground">
              {children}
          </div>
        </SidebarInset>
        <Toaster />
      </SidebarProvider>
  )
}
