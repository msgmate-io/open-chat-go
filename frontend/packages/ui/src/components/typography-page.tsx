import type { ReactNode } from "react";
import {
  bodyTextTypes,
  fontFamilies,
  formatTextStyleDetails,
  headingTextTypes,
  textStyleDefinitions,
  TextTypes,
  type TextType,
} from "../tokens/typography";
import { Text } from "./text";

function TypographyRow({ type }: { type: TextType }) {
  const def = textStyleDefinitions[type];
  return (
    <div className="grid gap-3 border-b border-border py-6 md:grid-cols-[minmax(0,1fr)_minmax(0,280px)] md:items-center">
      <Text type={type}>{type}</Text>
      <dl className="space-y-1">
        <div className="flex flex-wrap gap-x-4 gap-y-1">
          <div>
            <dt className="sr-only">Category</dt>
            <dd>
              <Text type={TextTypes.Body7} className="font-mono uppercase tracking-wide" color="muted">
                {def.category}
              </Text>
            </dd>
          </div>
          <div>
            <dt className="sr-only">Font family</dt>
            <dd>
              <Text type={TextTypes.Body6} color="muted">
                {fontFamilies[def.fontFamily].name}
              </Text>
            </dd>
          </div>
        </div>
        <div>
          <dt className="sr-only">Scale details</dt>
          <dd>
            <Text type={TextTypes.Body6} color="muted">
              {formatTextStyleDetails(def)}
            </Text>
          </dd>
        </div>
        <div>
          <dt className="sr-only">Default element</dt>
          <dd>
            <Text type={TextTypes.Body6} color="muted">
              Default tag: <code className="font-mono text-xs">&lt;{def.defaultTag}&gt;</code>
            </Text>
          </dd>
        </div>
      </dl>
    </div>
  );
}

function FontFamilyCard({
  title,
  name,
  usage,
  sample,
}: {
  title: string;
  name: string;
  usage: string;
  sample: ReactNode;
}) {
  return (
    <div className="surface-panel p-6">
      <Text type={TextTypes.Body7} className="font-mono uppercase tracking-wide" color="muted">
        {title}
      </Text>
      <Text type={TextTypes.Body4} tag="p" bold className="mt-2">
        {name}
      </Text>
      <Text type={TextTypes.Body6} color="muted" className="mt-1">
        {usage}
      </Text>
      <div className="mt-4 border-t border-border pt-4">{sample}</div>
    </div>
  );
}

export function TypographyPage() {
  return (
    <div className="mx-auto max-w-5xl px-6 py-10">
      <div className="space-y-3">
        <Text type={TextTypes.Body7} className="font-mono uppercase tracking-wide" color="muted">
          Design System
        </Text>
        <Text type={TextTypes.Heading3} tag="h1">
          Typography
        </Text>
        <Text type={TextTypes.Body4} color="muted">
          All typography is expressed through the <code className="font-mono text-sm">&lt;Text&gt;</code>{" "}
          component and the shared <code className="font-mono text-sm">TextTypes</code> scale. Sizes use{" "}
          <code className="font-mono text-sm">rem</code> so they respect user font settings.
        </Text>
      </div>

      <section className="mt-10">
        <Text type={TextTypes.Heading5} tag="h2" className="mb-4">
          Font families
        </Text>
        <div className="grid gap-4 md:grid-cols-2">
          <FontFamilyCard
            title="Headings"
            name={fontFamilies.heading.name}
            usage={fontFamilies.heading.usage}
            sample={<Text type={TextTypes.Heading4}>The quick brown fox jumps over the lazy dog</Text>}
          />
          <FontFamilyCard
            title="Body"
            name={fontFamilies.body.name}
            usage={fontFamilies.body.usage}
            sample={<Text type={TextTypes.Body4}>The quick brown fox jumps over the lazy dog</Text>}
          />
        </div>
      </section>

      <section className="mt-12">
        <Text type={TextTypes.Heading5} tag="h2" className="mb-2">
          Headings
        </Text>
        <Text type={TextTypes.Body5} color="muted" className="mb-2">
          Used to summarize and highlight sections. Font family: {fontFamilies.heading.name}.
        </Text>
        {headingTextTypes.map((type) => (
          <TypographyRow key={type} type={type} />
        ))}
      </section>

      <section className="mt-12">
        <Text type={TextTypes.Heading5} tag="h2" className="mb-2">
          Body
        </Text>
        <Text type={TextTypes.Body5} color="muted" className="mb-2">
          Used for paragraphs, labels, and UI copy. Font family: {fontFamilies.body.name}.
        </Text>
        {bodyTextTypes.map((type) => (
          <TypographyRow key={type} type={type} />
        ))}
      </section>

      <section className="mt-12 rounded-xl border border-border/80 bg-card p-6 shadow-sm ring-1 ring-black/[0.03] dark:ring-white/[0.04]">
        <Text type={TextTypes.Heading6} tag="h2" className="mb-4">
          Semantic text colors
        </Text>
        <div className="grid gap-3 sm:grid-cols-2">
          <Text color="foreground">foreground — primary copy</Text>
          <Text color="muted">muted — secondary / hint text</Text>
          <Text color="primary">primary — links and emphasis</Text>
          <Text color="destructive">destructive — errors</Text>
          <Text color="brand">brand — marketing accents</Text>
          <Text color="success">success — positive states</Text>
        </div>
      </section>
    </div>
  );
}
