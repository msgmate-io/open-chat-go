import { Badge } from "./badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./card"
import { cn } from "../lib/utils"

export type ModelCardData = {
  title: string
  modelId: string
  description?: string
  backend?: string
  maxTokens?: number
  context?: number
  isDefault?: boolean
  bots?: string[]
}

export function ModelCard({
  model,
  selectable = false,
  selected = false,
  onSelectChange,
  actions,
}: {
  model: ModelCardData
  selectable?: boolean
  selected?: boolean
  onSelectChange?: (selected: boolean) => void
  actions?: React.ReactNode
}) {
  return (
    <Card
      className={cn(
        "relative flex h-full touch-pan-y flex-col border border-border/70 bg-card/95 transition-[border-color,box-shadow,background-color]",
        selected && "border-primary/70 bg-primary/5 shadow-md ring-1 ring-primary/30",
      )}
    >
      {selectable ? (
        <label className="absolute right-3 top-3 z-10 flex cursor-pointer items-center gap-1 rounded-md bg-background/90 px-1.5 py-1 text-xs">
          <input
            type="checkbox"
            checked={selected}
            onChange={(event) => onSelectChange?.(event.target.checked)}
          />
          Select
        </label>
      ) : null}
      <CardHeader className="pb-3">
        <div className="flex items-start justify-between gap-2">
          <CardTitle className="line-clamp-2 text-base">{model.title || model.modelId}</CardTitle>
        </div>
        <CardDescription className="font-mono text-xs text-muted-foreground">
          {model.modelId}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-1 flex-col space-y-3">
        <p className="line-clamp-3 text-sm text-muted-foreground">
          {model.description || "No description available."}
        </p>
        <div className="flex flex-wrap gap-2">
          {model.backend ? <Badge variant="outline">{model.backend}</Badge> : null}
          {typeof model.maxTokens === "number" ? (
            <Badge variant="outline">max {model.maxTokens}</Badge>
          ) : null}
          {typeof model.context === "number" ? <Badge variant="outline">ctx {model.context}</Badge> : null}
          <Badge
            variant="outline"
            className="border-emerald-400/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
          >
            {(model.bots?.length ?? 0) > 0
              ? `bots: ${model.bots?.join(", ")}`
              : "bots: none"}
          </Badge>
        </div>
        {actions ? <div className="mt-auto pt-1">{actions}</div> : null}
      </CardContent>
    </Card>
  )
}
