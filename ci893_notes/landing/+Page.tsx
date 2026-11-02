"use client"

import React from "react"
import LandingLayout, { CtaBand, Card, PageHero, Section } from "../../components/LandingLayout"
import { ProviderLogoWall } from "../../components/ProviderLogos"
import {
  Activity,
  CheckCircle2,
  FlaskConical,
  Gauge,
  GitBranch,
  Lock,
  Repeat,
  RotateCcw,
  Workflow,
} from "lucide-react"

const PROBLEM = [
  {
    title: "Models change underneath you",
    body: "A provider ships a new version, quality shifts, and nothing in your stack tells you which workflows just broke.",
  },
  {
    title: "Vendor benchmarks are not your use-cases",
    body: "Leaderboard scores say nothing about whether your prompt, your tools and your data still produce the right answer.",
  },
  {
    title: "Failures surface in production",
    body: "Without a pipeline that replays real scenarios, the first person to notice a regression is a customer.",
  },
]

const PIPELINE = [
  {
    icon: <FlaskConical className="size-5" aria-hidden="true" />,
    title: "Simulation tests",
    body: "Capture real scenarios and replay them for every use-case, so a change is measured against how the system is actually used.",
  },
  {
    icon: <GitBranch className="size-5" aria-hidden="true" />,
    title: "End-to-end suites",
    body: "Run against real integrations, auth and permissions — not mocks — so the test path matches production.",
  },
  {
    icon: <Repeat className="size-5" aria-hidden="true" />,
    title: "Re-run on every change",
    body: "The suite re-runs on each new model, provider or configuration, turning an upgrade into a measurable event.",
  },
  {
    icon: <Gauge className="size-5" aria-hidden="true" />,
    title: "Capability reports",
    body: "We compare providers and models and publish the methodology and capability reports, so model choice is a decision, not a vibe.",
  },
  {
    icon: <Workflow className="size-5" aria-hidden="true" />,
    title: "Automated pipelines",
    body: "Wire the same layer into large-scale development and optimisation pipelines that test and verify each change before release.",
  },
  {
    icon: <RotateCcw className="size-5" aria-hidden="true" />,
    title: "Reversible by default",
    body: "Every version and configuration can be reverted, so a regression is a rollback — not an incident.",
  },
]

export default function Page() {
  return (
    <LandingLayout>
      <PageHero
        eyebrow="Evaluations & benchmarks"
        icon={<FlaskConical className="size-3.5 text-primary" aria-hidden="true" />}
        title="Test AI like you test code."
        intro="Open-Chat turns prompts, models, tools and integrations into measurable pipelines, so every change to your stack is evaluated against your own scenarios before it reaches a user. Reliable AI is a process, not a promise."
      />

      <Section
        eyebrow="The problem"
        title="You cannot ship what you cannot measure"
        intro="AI systems drift quietly. Without an owned test pipeline, every model change is an unmeasured risk."
      >
        <div className="grid gap-4 sm:grid-cols-3">
          {PROBLEM.map((item) => (
            <Card key={item.title} title={item.title}>
              {item.body}
            </Card>
          ))}
        </div>
      </Section>

      <Section
        eyebrow="The pipeline"
        title="One layer for evaluation, benchmarking and release gates"
        intro="The same core that runs your assistants runs your tests — real integrations, real permissions, real scenarios."
      >
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {PIPELINE.map(({ icon, title, body }) => (
            <Card key={title} icon={icon} title={title}>
              {body}
            </Card>
          ))}
        </div>
      </Section>

      <Section
        eyebrow="Benchmarks"
        title="Compare models and providers on your terms"
        intro="Swap or combine providers and re-run the same suite, so capability reports reflect your use-cases across the models you can actually use."
      >
        <ProviderLogoWall />
      </Section>

      <Section
        eyebrow="Proven on ourselves"
        title="We run every change through the same pipeline"
        intro="Open-Chat uses its own coding and management agents to develop itself, so every feature is tested on our own work first."
      >
        <div className="grid gap-4 sm:grid-cols-3">
          <div className="rounded-xl border border-border/60 p-5">
            <CheckCircle2 className="size-5" aria-hidden="true" />
            <div className="mt-3 text-sm font-semibold">Developed by agents</div>
            <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
              Rapidly test and develop new features on the same layer we sell.
            </p>
          </div>
          <div className="rounded-xl border border-border/60 p-5">
            <Gauge className="size-5" aria-hidden="true" />
            <div className="mt-3 text-sm font-semibold">Verified before release</div>
            <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
              Each change is measured and verified before it ships.
            </p>
          </div>
          <div className="rounded-xl border border-border/60 p-5">
            <Lock className="size-5" aria-hidden="true" />
            <div className="mt-3 text-sm font-semibold">Private suites, public method</div>
            <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
              Full test suites stay private; methodology and capability reports are published.
            </p>
          </div>
        </div>
        <div className="mt-4 flex items-center gap-2 text-xs text-muted-foreground">
          <Activity className="size-4" aria-hidden="true" />
          Every change is tested, approved, logged and reversible.
        </div>
      </Section>

      <CtaBand
        title="Put your AI under test"
        body="Wire Open-Chat into your pipelines, replay your real scenarios on every model change and move fast without breaking things."
        primary={{ label: "See the company AI layer", href: "/company_ai_integration" }}
        secondary={{ label: "Read the FAQ", href: "/faq_page" }}
      />
    </LandingLayout>
  )
}
