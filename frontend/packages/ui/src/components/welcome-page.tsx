import { Button } from "./button";
import { Text, TextTypes } from "./text";

const links = [
  {
    title: "Typography",
    description: "Type scale, font families, and the Text component.",
    story: "design-system-typography--overview",
  },
  {
    title: "Color scheme",
    description: "Semantic tokens for light and dark themes.",
    story: "design-system-color-scheme--playground",
  },
  {
    title: "Chat UI",
    description: "Composed chat patterns — lists, messages, and inputs.",
    story: "chat-examples-chat-page--default",
  },
  {
    title: "Components",
    description: "Buttons, inputs, dialogs, and other building blocks.",
    story: "components-button--primary",
  },
] as const;

function FeatureCard({
  title,
  description,
}: {
  title: string;
  description: string;
}) {
  return (
    <div className="surface-interactive p-5">
      <Text type={TextTypes.Heading7} tag="h3" bold>
        {title}
      </Text>
      <Text type={TextTypes.Body6} color="muted" className="mt-2">
        {description}
      </Text>
    </div>
  );
}

export function WelcomePage() {
  return (
    <div className="mx-auto max-w-4xl px-6 py-12">
      <div className="space-y-4">
        <Text
          type={TextTypes.Body7}
          tag="p"
          color="muted"
          className="font-mono uppercase tracking-wide"
        >
          Msgmate.io · Design System
        </Text>
        <Text type={TextTypes.Heading3} tag="h1">
          Open-Chat
        </Text>
        <Text type={TextTypes.Body4} color="muted">
          A decentralized, self-hostable chat platform with support for proprietary and
          open-source AI backends. Open-Chat gives you full control over models, bots, and
          data — run completions on your own GPU, plug in any OpenAI-compatible API, or choose
          from hosted providers without exposing API keys.
        </Text>
      </div>

      <section className="mt-10 space-y-4">
        <Text type={TextTypes.Heading5} tag="h2">
          What makes Open-Chat different
        </Text>
        <ul className="space-y-3">
          <li>
            <Text type={TextTypes.Body5}>
              <Text tag="span" bold>
                Self-hostable
              </Text>{" "}
              — deploy on your infrastructure with Docker or Kubernetes via Msgmate.io tooling.
            </Text>
          </li>
          <li>
            <Text type={TextTypes.Body5}>
              <Text tag="span" bold>
                Model-agnostic
              </Text>{" "}
              — llama.cpp, LocalAI, Groq, OpenAI, DeepInfra, and more through one chat interface.
            </Text>
          </li>
          <li>
            <Text type={TextTypes.Body5}>
              <Text tag="span" bold>
                Bot-ready
              </Text>{" "}
              — bots authenticate like users, join websocket channels, and can serve any purpose.
            </Text>
          </li>
          <li>
            <Text type={TextTypes.Body5}>
              <Text tag="span" bold>
                Full API
              </Text>{" "}
              — programmatic chat endpoints for integrations, automation, and custom clients.
            </Text>
          </li>
        </ul>
      </section>

      <section className="mt-10">
        <Text type={TextTypes.Heading5} tag="h2" className="mb-4">
          Explore the design system
        </Text>
        <div className="grid gap-4 sm:grid-cols-2">
          {links.map((link) => (
            <FeatureCard key={link.title} title={link.title} description={link.description} />
          ))}
        </div>
      </section>

      <section className="mt-10">
        <Text type={TextTypes.Heading5} tag="h2" className="mb-2">
          Surfaces & depth
        </Text>
        <Text type={TextTypes.Body6} color="muted" className="mb-4 max-w-2xl">
          Subtle shadows, ring highlights, and layered backgrounds add depth without heavy
          borders. Use <code className="font-mono text-xs">surface-panel</code>,{" "}
          <code className="font-mono text-xs">surface-interactive</code>, and{" "}
          <code className="font-mono text-xs">surface-sunken</code> utility classes.
        </Text>
        <div className="grid gap-4 sm:grid-cols-3">
          <div className="surface-panel p-4">
            <Text type={TextTypes.Body6} bold>
              Panel
            </Text>
            <Text type={TextTypes.Body7} color="muted" className="mt-1">
              Static sections and cards
            </Text>
          </div>
          <div className="surface-interactive p-4">
            <Text type={TextTypes.Body6} bold>
              Interactive
            </Text>
            <Text type={TextTypes.Body7} color="muted" className="mt-1">
              Hover for lift + shadow
            </Text>
          </div>
          <div className="surface-sunken p-4">
            <Text type={TextTypes.Body6} bold>
              Sunken
            </Text>
            <Text type={TextTypes.Body7} color="muted" className="mt-1">
              Inset wells and code areas
            </Text>
          </div>
        </div>
      </section>

      <section className="mt-10 surface-panel p-6">
        <Text type={TextTypes.Heading6} tag="h2" className="mb-2">
          About Msgmate.io
        </Text>
        <Text type={TextTypes.Body5} color="muted">
          Msgmate.io maintains Open-Chat and related infrastructure. The project is in closed
          beta — reach out at{" "}
          <a href="mailto:tim@msgmate.io" className="text-primary underline-offset-4 hover:underline">
            tim@msgmate.io
          </a>{" "}
          for early access, or browse the{" "}
          <a
            href="https://github.com/msgmate-io/open-chat-go"
            target="_blank"
            rel="noopener noreferrer"
            className="text-primary underline-offset-4 hover:underline"
          >
            repository
          </a>{" "}
          on GitHub.
        </Text>
        <div className="mt-4 flex flex-wrap gap-3">
          <Button variant="brand" asChild>
            <a href="https://github.com/msgmate-io/open-chat-go" target="_blank" rel="noopener noreferrer">
              View on GitHub
            </a>
          </Button>
          <Button variant="outline" asChild>
            <a href="https://msgmate-io.github.io/open-chat-go/" target="_blank" rel="noopener noreferrer">
              Deployed Storybook
            </a>
          </Button>
        </div>
      </section>

      <Text type={TextTypes.Body7} color="muted" className="mt-10 block text-center">
        Use the sidebar to browse foundations, components, and chat examples. Toggle the theme
        toolbar to preview light and dark modes.
      </Text>
    </div>
  );
}
