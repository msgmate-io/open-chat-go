"use client"

import { Button, Text, TextTypes } from "@open-chat-go/ui"
import { Lock, LogIn } from "lucide-react"

import type { DocGateReason } from "@/components/docs/useDocsAccess"

const NOTICES: Record<
  Exclude<DocGateReason, "none">,
  {
    title: string;
    body: string;
    action: { label: string; href: string; variant?: "default" | "outline" };
  }
> = {
  signin: {
    title: "You have to sign in to view these docs",
    body:
      "This page documents an integration that is not part of the public documentation. Sign in with an account that has access to view it.",
    action: { label: "Sign in", href: "/login", variant: "default" },
  },
  "no-access": {
    title: "You don't have access to view this integration's docs",
    body:
      "Your account is signed in, but it is not permitted to view the documentation of this integration. Ask an administrator for access if you think this is a mistake.",
    action: { label: "Back to public docs", href: "/docs", variant: "outline" },
  },
}

/**
 * Rendered in place of a documentation page the visitor may not read.
 *
 * The public docs never inline private integration material: they only mention
 * it and link to it, and such a link resolves to this notice when the visitor
 * has no session or no access to the documented integration.
 */
export function DocsAccessNotice({ reason }: { reason: DocGateReason }) {
  if (reason === "none") {
    return null
  }

  const notice = NOTICES[reason]

  return (
    <div
      className="mx-auto flex w-full max-w-xl flex-col items-center gap-3 py-16 text-center"
      data-doc-gate={reason}
    >
      <div className="flex size-12 items-center justify-center rounded-full bg-muted text-muted-foreground">
        {reason === "signin" ? <LogIn className="size-6" /> : <Lock className="size-6" />}
      </div>
      <Text type={TextTypes.Heading4} tag="h1" className="docs-h1">
        {notice.title}
      </Text>
      <Text type={TextTypes.Body5} tag="p" color="muted" className="docs-p">
        {notice.body}
      </Text>
      <Button asChild variant={notice.action.variant ?? "outline"}>
        <a href={notice.action.href}>{notice.action.label}</a>
      </Button>
    </div>
  )
}
