"use client"

import React from "react"
import { navigate } from "vike/client/router"
import { Button, Text, TextTypes } from "@open-chat-go/ui"
import {
  ArrowRight,
  BookOpen,
  Building2,
  Code2,
  Cpu,
  FlaskConical,
  HelpCircle,
  Lock,
  PlayCircle,
  ShieldCheck,
  Sparkles,
} from "lucide-react"

// Release-announcement video. ci#858 moves it off the landing action bar and
// onto the product overview page, so the landing page keeps exactly two
// actions (docs + product hub) and the video plays below the fold here.
const RELEASE_VIDEO_URL = "https://youtu.be/k4_KK-o8jEU"

function toYouTubeEmbedUrl(url: string): string {
  if (!url) return ""
  try {
    const parsed = new URL(url)
    const id = parsed.hostname.includes("youtu.be")
      ? parsed.pathname.slice(1)
      : parsed.searchParams.get("v") ?? parsed.pathname.split("/").pop() ?? ""
    return id ? `https://www.youtube.com/embed/${id}?playsinline=1&rel=0` : ""
  } catch {
    return ""
  }
}

type ProductPageLink = {
  href: string
  label: string
  title: string
  description: string
  Icon: React.ComponentType<{ className?: string }>
}

// The five registered product pages (ci#858). The integration owns the page
// markup and their per-page config rows; this hub is the always-served entry
// point that links to all of them.
const PRODUCT_PAGES: ProductPageLink[] = [
  {
    href: "/company_ai_integration",
    label: "AI integration",
    title: "Open-Chat as your company AI layer",
    description:
      "One self-hosted layer for chat, retrieval, tools and automations - your models, your data, your rules.",
    Icon: Building2,
  },
  {
    href: "/in_product_ai_framework",
    label: "AI framework",
    title: "Open-Chat as an in-product AI framework",
    description:
      "Embed chat, agents and tool calling directly into your own application through a typed API and client SDKs.",
    Icon: Code2,
  },
  {
    href: "/evaluations_and_benchmarks",
    label: "Benchmarks",
    title: "Evaluations and benchmarks",
    description:
      "Turn prompts, models and tools into measurable pipelines and run them on every change of your stack.",
    Icon: FlaskConical,
  },
  {
    href: "/faq_page",
    label: "FAQ",
    title: "Frequently asked questions",
    description:
      "Licensing, hosting, data ownership, model choice and what it takes to run Open-Chat in production.",
    Icon: HelpCircle,
  },
]

const PILLARS = [
  {
    Icon: Lock,
    title: "Open source and fully in your control",
    body: "Audit the code, run it on your own infrastructure and keep every conversation, tool call and metric inside your perimeter.",
  },
  {
    Icon: Cpu,
    title: "Model agnostic by design",
    body: "Bring the models, endpoints and providers you already use - local, private or commercial - and switch per workspace instead of per rewrite.",
  },
  {
    Icon: ShieldCheck,
    title: "Built for regulated workloads",
    body: "Sessions, scoped access, audit trails and integration-level permissions are first-class, so pilots survive the security review.",
  },
]

export default function Page() {
  const navigateTo = (to: string) => navigate(to)
  const embedUrl = toYouTubeEmbedUrl(RELEASE_VIDEO_URL)

  return (
    <div className="min-h-screen bg-background text-foreground">
      <header className="sticky top-0 z-40 border-b border-border/60 bg-background/85 backdrop-blur">
        <div className="mx-auto flex w-full max-w-6xl items-center justify-between gap-4 px-4 py-3">
          <button
            type="button"
            onClick={() => navigateTo("/")}
            className="flex items-center gap-2 text-sm font-semibold tracking-tight"
          >
            <Sparkles className="size-4" aria-hidden="true" />
            Open-Chat
          </button>
          <nav className="flex flex-wrap items-center justify-end gap-1" aria-label="Product pages">
            <Button
              variant="ghost"
              size="sm"
              className="h-7 rounded-full px-2.5 text-xs font-normal text-muted-foreground"
              onClick={() => navigateTo("/product_overview")}
            >
              Overview
            </Button>
            {PRODUCT_PAGES.map(({ href, label }) => (
              <Button
                key={href}
                variant="ghost"
                size="sm"
                className="h-7 rounded-full px-2.5 text-xs font-normal text-muted-foreground"
                onClick={() => navigateTo(href)}
              >
                {label}
              </Button>
            ))}
            <Button
              variant="ghost"
              size="sm"
              className="h-7 rounded-full px-2.5 text-xs font-normal text-muted-foreground"
              onClick={() => navigateTo("/docs")}
            >
              <BookOpen className="mr-1.5 size-3.5" aria-hidden="true" />
              Docs
            </Button>
          </nav>
        </div>
      </header>

      <main className="mx-auto w-full max-w-6xl px-4 pb-20 pt-12">
        <section className="max-w-3xl">
          <Text type={TextTypes.Heading2} tag="h1" bold>
            What can Open-Chat be used for?
          </Text>
          <p className="mt-4 text-base leading-relaxed text-muted-foreground">
            Open-Chat is an open-source alternative to closed AI chat platforms: a self-hosted core with
            pluggable integrations, so the same deployment can power an internal AI assistant, an embedded
            product copilot and an automated evaluation pipeline. Pick the scenario that matches your team.
          </p>
          <div className="mt-6 flex flex-wrap gap-2">
            <Button size="sm" className="rounded-full" onClick={() => navigateTo("/company_ai_integration")}>
              Start with the company AI layer
              <ArrowRight className="ml-1.5 size-3.5" aria-hidden="true" />
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="rounded-full"
              onClick={() => navigateTo("/in_product_ai_framework")}
            >
              Or embed it in your product
            </Button>
          </div>
        </section>

        <section className="mt-14 grid gap-4 sm:grid-cols-2" aria-label="Product landing pages">
          {PRODUCT_PAGES.map(({ href, label, title, description, Icon }) => (
            <button
              key={href}
              type="button"
              onClick={() => navigateTo(href)}
              className="group rounded-xl border border-border/60 bg-card/40 p-5 text-left transition-colors hover:border-border hover:bg-accent/40"
            >
              <div className="flex items-center gap-2 text-xs uppercase tracking-wide text-muted-foreground">
                <Icon className="size-4" />
                {label}
              </div>
              <div className="mt-3 text-base font-semibold">{title}</div>
              <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{description}</p>
              <span className="mt-4 inline-flex items-center gap-1 text-sm text-foreground">
                Read more
                <ArrowRight className="size-3.5 transition-transform group-hover:translate-x-0.5" aria-hidden="true" />
              </span>
            </button>
          ))}
        </section>

        <section className="mt-16 grid gap-6 sm:grid-cols-3" aria-label="Why Open-Chat">
          {PILLARS.map(({ Icon, title, body }) => (
            <div key={title} className="rounded-xl border border-border/60 p-5">
              <Icon className="size-5" aria-hidden="true" />
              <div className="mt-3 text-sm font-semibold">{title}</div>
              <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{body}</p>
            </div>
          ))}
        </section>

        <section className="mt-16 max-w-3xl" aria-label="Release announcement">
          <Text type={TextTypes.Heading5} tag="h2" bold>
            See the release announcement
          </Text>
          <p className="mt-2 text-sm text-muted-foreground">
            A short walkthrough of the platform: chat, integrations, tools and the self-hosted deployment model.
          </p>
          <div className="mt-4 aspect-video w-full overflow-hidden rounded-xl border border-border/60 bg-black">
            {embedUrl ? (
              <iframe
                src={embedUrl}
                title="Open-Chat release announcement"
                className="h-full w-full"
                allow="encrypted-media; picture-in-picture"
                allowFullScreen
              />
            ) : (
              <div className="flex h-full w-full items-center justify-center text-sm text-muted-foreground">
                <PlayCircle className="mr-2 size-4" aria-hidden="true" />
                Video coming soon
              </div>
            )}
          </div>
        </section>
      </main>

      <footer className="border-t border-border/60">
        <div className="mx-auto flex w-full max-w-6xl flex-wrap items-center justify-between gap-3 px-4 py-6 text-xs text-muted-foreground">
          <span>Open-Chat - open source, self-hosted, fully under your control.</span>
          <div className="flex items-center gap-3">
            <button type="button" onClick={() => navigateTo("/docs")} className="hover:text-foreground">
              Documentation
            </button>
            <button type="button" onClick={() => navigateTo("/")} className="hover:text-foreground">
              Back to start
            </button>
          </div>
        </div>
      </footer>
    </div>
  )
}
