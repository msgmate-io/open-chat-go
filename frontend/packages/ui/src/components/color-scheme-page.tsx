import { Button } from "./button";
import { semanticColorTokens, radiusToken } from "../tokens/colors";

function ColorSwatch({ name, cssVar }: { name: string; cssVar: string }) {
  const isForeground = name.includes("foreground");
  return (
    <div className="overflow-hidden rounded-lg border border-border">
      <div
        className="flex h-20 items-end p-2"
        style={{
          background: isForeground ? "var(--background)" : `var(${cssVar})`,
          color: isForeground
            ? `var(${cssVar})`
            : `var(--${name.replace("-foreground", "")}-foreground, var(--foreground))`,
        }}
      >
        {!isForeground && (
          <span className="rounded bg-background/80 px-1.5 py-0.5 font-mono text-[10px] text-foreground">
            Aa
          </span>
        )}
        {isForeground && <span className="text-lg font-semibold">Aa</span>}
      </div>
      <div className="space-y-0.5 border-t border-border bg-card p-2.5">
        <p className="font-mono text-xs font-medium text-foreground">{name}</p>
        <p className="font-mono text-[10px] text-muted-foreground">{cssVar}</p>
      </div>
    </div>
  );
}

export function ColorSchemePage({
  showControlsHint = false,
}: {
  showControlsHint?: boolean;
}) {
  const groups = [
    {
      title: "Surfaces",
      tokens: semanticColorTokens.filter((t) =>
        [
          "background",
          "foreground",
          "card",
          "card-foreground",
          "popover",
          "popover-foreground",
        ].includes(t.name)
      ),
    },
    {
      title: "Actions",
      tokens: semanticColorTokens.filter((t) =>
        [
          "primary",
          "primary-foreground",
          "secondary",
          "secondary-foreground",
          "brand",
          "brand-foreground",
        ].includes(t.name)
      ),
    },
    {
      title: "Feedback",
      tokens: semanticColorTokens.filter((t) =>
        [
          "muted",
          "muted-foreground",
          "accent",
          "accent-foreground",
          "destructive",
          "destructive-foreground",
          "success",
          "success-foreground",
        ].includes(t.name)
      ),
    },
    {
      title: "Chrome",
      tokens: semanticColorTokens.filter((t) =>
        ["border", "input", "ring"].includes(t.name)
      ),
    },
    {
      title: "Sidebar",
      tokens: semanticColorTokens.filter((t) => t.name.startsWith("sidebar")),
    },
  ];

  return (
    <div className="mx-auto max-w-5xl space-y-10 p-6 text-foreground">
      <header className="space-y-2">
        <h1 className="text-2xl font-bold">Color scheme</h1>
        <p className="max-w-2xl text-sm text-muted-foreground">
          Semantic tokens are defined in{" "}
          <code className="rounded bg-muted px-1 py-0.5 text-xs">
            packages/ui/src/styles/semantic.css
          </code>
          . Use the toolbar <strong>Theme</strong> toggle for light/dark; use{" "}
          <strong>Controls</strong> to override individual tokens on top of that
          theme. Prefer Tailwind classes like{" "}
          <code className="rounded bg-muted px-1 py-0.5 text-xs">bg-primary</code>
          , not raw palette utilities.
        </p>
        {showControlsHint && (
          <p className="rounded-md border border-brand/40 bg-brand/10 px-3 py-2 text-xs text-foreground">
            Controls sync with the Theme toolbar. Overrides you set in Controls
            are kept when switching light/dark; unchanged tokens follow{" "}
            <code className="font-mono">semantic.css</code>.
          </p>
        )}
        <p className="text-xs text-muted-foreground">
          Radius: <code className="font-mono">{radiusToken.cssVar}</code> (
          {radiusToken.description})
        </p>
      </header>

      {groups.map((group) => (
        <section key={group.title} className="space-y-3">
          <h2 className="text-lg font-semibold">{group.title}</h2>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4">
            {group.tokens.map((token) => (
              <ColorSwatch
                key={token.name}
                name={token.name}
                cssVar={token.cssVar}
              />
            ))}
          </div>
        </section>
      ))}

      <section className="space-y-3">
        <h2 className="text-lg font-semibold">Button variants</h2>
        <div className="flex flex-wrap gap-2">
          <Button variant="default">default</Button>
          <Button variant="secondary">secondary</Button>
          <Button variant="outline">outline</Button>
          <Button variant="ghost">ghost</Button>
          <Button variant="destructive">destructive</Button>
          <Button variant="brand">brand</Button>
          <Button variant="neutral">neutral</Button>
        </div>
      </section>

      <section className="space-y-3">
        <h2 className="text-lg font-semibold">Sample surfaces</h2>
        <div className="grid gap-3 md:grid-cols-2">
          <div className="rounded-lg border border-border bg-card p-4 text-card-foreground">
            <p className="font-medium">Card</p>
            <p className="text-sm text-muted-foreground">Muted hint text</p>
          </div>
          <div className="rounded-lg border border-input bg-background p-4">
            <input
              className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm text-foreground"
              placeholder="Input on background"
              readOnly
            />
          </div>
        </div>
      </section>
    </div>
  );
}
