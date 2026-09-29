import React, { useMemo, useState } from "react"
import useSWR from "swr"
import { fetcher } from "@/lib/utils"
import { ChevronDown, ExternalLink, FileCode2, Info } from "lucide-react"
import {
  Badge,
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Input,
  Text,
  TextTypes,
} from "@open-chat-go/ui"

function SchemaBlock({ title, schema }: { title: string; schema: Record<string, unknown> | undefined }) {
  const pretty = JSON.stringify(schema ?? { type: "object", properties: {} }, null, 2)
  return (
    <div className="rounded-xl border border-border/70 bg-card/90 p-3">
      <div className="mb-2 flex items-center gap-2">
        <FileCode2 className="h-4 w-4 text-muted-foreground" />
        <Text type={TextTypes.Body6} bold>{title}</Text>
      </div>
      <pre className="max-h-72 overflow-auto rounded-lg border border-border/60 bg-muted/30 p-3 text-xs leading-5">
        {pretty}
      </pre>
    </div>
  )
}

type ToolRow = {
  name: string
  function_name: string
  description: string
  type: string
  source_path?: string
  source_line?: number
  source_url?: string
  requires_init?: boolean
  requires_confirmation?: boolean
  stop_on_first_confirmable_tool_call?: boolean
  confirmation_block_message?: string
  required?: string[]
  call_schema?: Record<string, unknown>
  init_schema?: Record<string, unknown>
}

type ToolsResponse = {
  page: number
  page_size: number
  total_rows: number
  total_pages: number
  rows: ToolRow[]
  filters: {
    types: string[]
  }
}

export function ToolsCatalogView({ mode = "private" }: { mode?: "private" | "public" }) {
  const [page, setPage] = useState(1)
  const [searchQuery, setSearchQuery] = useState("")
  const [typeFilter, setTypeFilter] = useState("")
  const [requiresInitFilter, setRequiresInitFilter] = useState("")
  const [detailToolName, setDetailToolName] = useState<string | null>(null)
  const [detailToolSeed, setDetailToolSeed] = useState<ToolRow | null>(null)
  const isPrivate = mode === "private"
  const pageSize = 12

  const listURL = useMemo(() => {
    const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) })
    if (searchQuery.trim()) params.set("q", searchQuery.trim())
    if (typeFilter) params.set("type", typeFilter)
    if (isPrivate && requiresInitFilter) params.set("requires_init", requiresInitFilter)
    return `/api/v1/tools?${params.toString()}`
  }, [isPrivate, page, pageSize, requiresInitFilter, searchQuery, typeFilter])

  const { data, isLoading, error } = useSWR<ToolsResponse>(listURL, fetcher)
  const detailURL = useMemo(() => {
    if (!detailToolName) return null
    return `/api/v1/tools/${encodeURIComponent(detailToolName)}`
  }, [detailToolName])
  const { data: detailToolData, isLoading: isLoadingDetailTool } = useSWR<ToolRow>(detailURL, fetcher)
  const detailTool = detailToolData ?? detailToolSeed
  const hasActiveFilters = Boolean(searchQuery.trim() || typeFilter || requiresInitFilter)

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 xl:flex-row xl:items-start xl:justify-between">
        <div className="min-w-0">
          <Text type={TextTypes.Heading5} tag="h1" bold>Platform Tools</Text>
          <Text type={TextTypes.Body5} color="muted">Discover runtime tools, inspect source-linked docs, and review precise call/init schemas.</Text>
          <Text type={TextTypes.Body6} color="muted" className="mt-1">{data?.total_rows ?? 0} total tools</Text>
        </div>
        <div className="flex w-full flex-wrap items-center justify-end gap-2 xl:w-auto xl:max-w-[68%]">
          <Input
            value={searchQuery}
            onChange={(e) => {
              setSearchQuery(e.target.value)
              setPage(1)
            }}
            placeholder="Search tools..."
            className="h-9 w-[210px]"
          />
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm" className="min-w-40 justify-between">
                <span className="truncate">Type {typeFilter ? `: ${typeFilter}` : "(all)"}</span>
                <ChevronDown className="h-4 w-4 opacity-70" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuLabel>Filter by tool type</DropdownMenuLabel>
              <DropdownMenuSeparator />
              <DropdownMenuItem onClick={() => { setTypeFilter(""); setPage(1) }}>All types</DropdownMenuItem>
              {(data?.filters?.types ?? []).map((type) => (
                <DropdownMenuItem key={type} onClick={() => { setTypeFilter(type); setPage(1) }}>{type}</DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
          {isPrivate ? (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="outline" size="sm" className="min-w-48 justify-between">
                  <span className="truncate">Init Required {requiresInitFilter ? `: ${requiresInitFilter}` : "(all)"}</span>
                  <ChevronDown className="h-4 w-4 opacity-70" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuLabel>Filter by init requirement</DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem onClick={() => { setRequiresInitFilter(""); setPage(1) }}>All</DropdownMenuItem>
                <DropdownMenuItem onClick={() => { setRequiresInitFilter("true"); setPage(1) }}>Requires init</DropdownMenuItem>
                <DropdownMenuItem onClick={() => { setRequiresInitFilter("false"); setPage(1) }}>No init required</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          ) : null}
          <Button variant="ghost" size="sm" disabled={!hasActiveFilters} onClick={() => {
            setSearchQuery("")
            setTypeFilter("")
            setRequiresInitFilter("")
            setPage(1)
          }}>Clear</Button>
        </div>
      </div>

      {isLoading ? <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">{Array.from({ length: pageSize }).map((_, idx) => <div key={idx} className="h-36 animate-pulse rounded-xl bg-muted" />)}</div> : null}
      {error ? <Text type={TextTypes.Body5} color="muted">Failed to load tools.</Text> : null}

      {!isLoading && !error ? (
        <>
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
            {(data?.rows ?? []).map((tool) => (
              <div key={tool.name} className="rounded-2xl border border-border/70 bg-gradient-to-b from-card to-card/70 p-4 shadow-sm">
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <Text type={TextTypes.Body4} bold className="break-all">{tool.name}</Text>
                    <Text type={TextTypes.Body7} color="muted" className="mt-1 break-all">{tool.function_name}</Text>
                  </div>
                  <div className="flex shrink-0 flex-col items-end gap-1">
                    <Badge variant="outline">{tool.type || "tool"}</Badge>
                    {isPrivate ? (
                      <Badge variant={tool.requires_init ? "secondary" : "outline"}>
                        {tool.requires_init ? "Init required" : "No init"}
                      </Badge>
                    ) : null}
                    {isPrivate ? (
                      <Badge variant={tool.requires_confirmation ? "secondary" : "outline"}>
                        {tool.requires_confirmation ? "Confirm required" : "No confirmation"}
                      </Badge>
                    ) : null}
                  </div>
                </div>
                <div className="mt-3 rounded-lg border border-border/60 bg-muted/20 p-3">
                  <Text type={TextTypes.Body6} color="muted" className="line-clamp-4 min-h-16">{tool.description || "No description provided."}</Text>
                </div>
                {isPrivate ? (
                  <div className="mt-3 flex flex-wrap gap-2">
                    {(tool.required ?? []).length > 0 ? <Badge variant="outline">{tool.required?.length} required param(s)</Badge> : null}
                  </div>
                ) : null}
                <div className="mt-4 flex items-center gap-2">
                  <Button size="sm" variant="outline" onClick={() => {
                    setDetailToolSeed(tool)
                    setDetailToolName(tool.name)
                  }}>
                    <Info className="mr-1 h-4 w-4" />
                    Details
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={!tool.source_url}
                    onClick={() => {
                      if (!tool.source_url) return
                      window.open(tool.source_url, "_blank", "noopener,noreferrer")
                    }}
                  >
                    <ExternalLink className="mr-1 h-4 w-4" />
                    Source
                  </Button>
                </div>
              </div>
            ))}
          </div>

          <div className="flex items-center justify-between rounded-xl border border-border/70 bg-card/80 p-3">
            <Button variant="outline" size="sm" disabled={page <= 1} onClick={() => setPage((prev) => Math.max(1, prev - 1))}>Previous</Button>
            <Text type={TextTypes.Body6}>Page {data?.page ?? page} of {Math.max(1, data?.total_pages ?? 1)}</Text>
            <Button variant="outline" size="sm" disabled={page >= (data?.total_pages ?? 1)} onClick={() => setPage((prev) => prev + 1)}>Next</Button>
          </div>
        </>
      ) : null}

      <Dialog open={Boolean(detailToolName)} onOpenChange={(open) => {
        if (!open) {
          setDetailToolName(null)
          setDetailToolSeed(null)
        }
      }}>
        <DialogContent className="max-h-[88vh] max-w-6xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle className="break-all">{detailTool?.name || detailToolName || "Tool Details"}</DialogTitle>
            <DialogDescription className="break-words">{detailTool?.description || "No description available."}</DialogDescription>
          </DialogHeader>

          <div className="space-y-4">
            {isLoadingDetailTool ? (
              <div className="rounded-xl border border-border/70 bg-card/90 p-3">
                <Text type={TextTypes.Body6} color="muted">Loading latest tool details...</Text>
              </div>
            ) : null}
            <div className="rounded-xl border border-border/70 bg-gradient-to-r from-muted/20 to-card p-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <Text type={TextTypes.Body6} bold>Source Location</Text>
                  <Text type={TextTypes.Body7} color="muted" className="mt-1 break-all">
                    {detailTool?.source_path ? `${detailTool.source_path}:${detailTool.source_line || 1}` : "Not available"}
                  </Text>
                </div>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={!detailTool?.source_url}
                  onClick={() => {
                    if (!detailTool?.source_url) return
                    window.open(detailTool.source_url, "_blank", "noopener,noreferrer")
                  }}
                >
                  <ExternalLink className="mr-1 h-4 w-4" />
                  Open Source
                </Button>
              </div>
            </div>

            <div className="grid gap-3 md:grid-cols-2">
              <SchemaBlock title="Call Schema" schema={detailTool?.call_schema} />
              <SchemaBlock title="Init Schema" schema={detailTool?.init_schema} />
            </div>

            <div className="rounded-xl border border-border/70 bg-card/90 p-3">
              <Text type={TextTypes.Body6} bold>Quick Summary</Text>
              <div className="mt-2 flex flex-wrap gap-2">
                <Badge variant="outline">Type: {detailTool?.type || "tool"}</Badge>
                {isPrivate ? <Badge variant={detailTool?.requires_init ? "secondary" : "outline"}>{detailTool?.requires_init ? "Init required" : "No init required"}</Badge> : null}
                {isPrivate ? <Badge variant={detailTool?.requires_confirmation ? "secondary" : "outline"}>{detailTool?.requires_confirmation ? "Confirmation required" : "No confirmation required"}</Badge> : null}
                {isPrivate && detailTool?.requires_confirmation ? (
                  <Badge variant={detailTool?.stop_on_first_confirmable_tool_call ? "secondary" : "outline"}>
                    {detailTool?.stop_on_first_confirmable_tool_call ? "Stop on first confirmable call" : "Continue after confirm request"}
                  </Badge>
                ) : null}
                {(detailTool?.required ?? []).length > 0 ? <Badge variant="outline">Required call params: {(detailTool?.required ?? []).join(", ")}</Badge> : <Badge variant="outline">No required call params</Badge>}
              </div>
            </div>

            {isPrivate && detailTool?.requires_confirmation && detailTool?.confirmation_block_message ? (
              <div className="rounded-xl border border-border/70 bg-card/90 p-3">
                <Text type={TextTypes.Body6} bold>Confirmation Block Message</Text>
                <Text type={TextTypes.Body7} color="muted" className="mt-1">Returned to the model when a confirmable tool call is blocked pending approval.</Text>
                <pre className="mt-3 max-h-56 overflow-auto rounded-lg border border-border/60 bg-muted/30 p-3 text-xs leading-5">{detailTool.confirmation_block_message}</pre>
              </div>
            ) : null}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
