import * as React from "react"
import { cn } from "../../lib/utils"
import { ChevronDown } from "lucide-react"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "../dialog"

type BotModel = {
  title: string;
  description?: string;
  configuration?: {
    model?: string;
    backend?: string;
  };
}

export function BotSelector({
    contact,
    selectedModel,
    setSelectedModel,
    defaultCollapsed = true,
}: {
    contact: any,
    selectedModel: string,
    setSelectedModel: (model: string) => void
    defaultCollapsed?: boolean
}) {
    const [collapsed, setCollapsed] = React.useState(defaultCollapsed)
    const [showNoModelsHelp, setShowNoModelsHelp] = React.useState(false)
    const [searchQuery, setSearchQuery] = React.useState("")
    const models = (contact?.profile_data?.models ?? []) as BotModel[]
    const normalizedQuery = searchQuery.trim().toLowerCase()
    const queryTerms = normalizedQuery.split(/\s+/).filter(Boolean)
    const filteredModels = models.filter((model) => {
        if (queryTerms.length === 0) {
            return true
        }

        const searchable = [
            model.title,
            model.description,
            model.configuration?.backend,
            model.configuration?.model,
        ]
            .filter(Boolean)
            .join(" ")
            .toLowerCase()

        return queryTerms.every((term) => searchable.includes(term))
    })

    if (!models.length) {
        return (
            <>
                <button
                    type="button"
                    onClick={() => setShowNoModelsHelp(true)}
                    className="w-full rounded-md border border-amber-300 bg-amber-100 px-3 py-2 text-left text-sm font-semibold text-amber-900 transition-colors hover:bg-amber-200"
                >
                    No models found?!
                </button>

                <Dialog open={showNoModelsHelp} onOpenChange={setShowNoModelsHelp}>
                    <DialogContent className="max-w-lg">
                        <DialogHeader>
                            <DialogTitle>No models are available yet</DialogTitle>
                            <DialogDescription>
                                Set provider environment variables, then restart the server so model access can be refreshed.
                            </DialogDescription>
                        </DialogHeader>

                        <div className="space-y-3 text-sm">
                            <div>
                                <div className="font-semibold">Provider API key env vars</div>
                                <ul className="mt-1 list-disc space-y-1 pl-5 text-muted-foreground">
                                    <li><code>OPENAI_API_KEY</code></li>
                                    <li><code>ANTHROPIC_API_KEY</code></li>
                                    <li><code>DEEPINFRA_API_KEY</code></li>
                                    <li><code>GROQ_API_KEY</code></li>
                                    <li><code>LITELLM_API_KEY</code></li>
                                    <li><code>MSGMATE_CLUSTER_API_KEY</code></li>
                                </ul>
                            </div>

                            <div>
                                <div className="font-semibold">Provider host env vars</div>
                                <ul className="mt-1 list-disc space-y-1 pl-5 text-muted-foreground">
                                    <li><code>LITELLM_API_HOST</code> (LiteLLM base URL)</li>
                                    <li><code>MSGMATE_CLUSTER_HOST</code> (Msgmate cluster base URL)</li>
                                    <li><code>OLLAMA_API_HOST</code> (Ollama base URL)</li>
                                </ul>
                            </div>

                            <p className="text-muted-foreground">
                                After updating env vars, restart the server. If this is mobile native, use the server restart action in the app.
                            </p>
                        </div>
                    </DialogContent>
                </Dialog>
            </>
        )
    }

    return (
        <div className="w-full space-y-2">
            <button
                type="button"
                onClick={() => {
                    setCollapsed((prev) => {
                        const nextCollapsed = !prev
                        if (nextCollapsed) {
                            setSearchQuery("")
                        }
                        return nextCollapsed
                    })
                }}
                className="flex w-full items-start justify-between rounded-md border border-border bg-card px-2.5 py-2 text-left text-xs font-medium leading-tight hover:bg-muted/40 md:px-3 md:text-sm"
            >
                <span className="min-w-0 pr-2 whitespace-normal break-words leading-5">{selectedModel || "Select model"}</span>
                <ChevronDown className={cn("mt-0.5 size-4 shrink-0 transition-transform", { "rotate-180": !collapsed })} />
            </button>

            {!collapsed ? (
              <div className="space-y-2">
                <input
                  type="text"
                  value={searchQuery}
                  onChange={(event) => setSearchQuery(event.target.value)}
                  placeholder="Search by provider or model"
                  className="w-full rounded-md border border-border bg-card px-2.5 py-2 text-xs outline-none ring-offset-background placeholder:text-muted-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 md:px-3 md:text-sm"
                />

                <div className="grid max-h-[44vh] gap-2 overflow-y-auto pr-1 md:max-h-[28rem] md:grid-cols-2">
                  {filteredModels.map((model) => (
                    <button
                      key={model.title}
                      type="button"
                      onClick={() => {
                        setSelectedModel(model.title)
                        setCollapsed(true)
                        setSearchQuery("")
                      }}
                      className={cn(
                        "rounded-md border px-2.5 py-2 text-left text-xs transition-colors hover:bg-muted/40 md:px-3 md:text-sm",
                        selectedModel === model.title ? "border-primary bg-muted/50" : "border-border bg-card"
                      )}
                    >
                      <div className="break-words font-medium leading-5">{model.title}</div>
                      {model.description ? (
                        <div className="line-clamp-3 break-words text-xs text-muted-foreground">{model.description}</div>
                      ) : null}
                    </button>
                  ))}
                </div>

                {filteredModels.length === 0 ? (
                  <div className="rounded-md border border-dashed border-border px-3 py-2 text-xs text-muted-foreground md:text-sm">
                    No models match that search.
                  </div>
                ) : null}
              </div>
            ) : null}
        </div>
    )
}

export function BotDisplay({
    selectedModel,
    className,
}: {
    selectedModel: string
    className?: string
}) {
    return (
        <div className={cn("inline-flex max-w-[18rem] items-center rounded-md border border-border bg-card px-3 py-2 text-sm font-medium text-foreground shadow-sm", className)}>
            <span className="truncate">{selectedModel || "No model"}</span>
        </div>
    )
}
