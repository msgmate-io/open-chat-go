import React, { useEffect, useMemo, useRef, useState } from "react"
import useSWR, { mutate as mutateCache } from "swr"
import { fetcher } from "../../lib/utils"
import { isMobileAppRuntime } from "../../lib/mobile-app"
import { ChevronDown, Info, Trash2 } from "lucide-react"
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Input,
  ModelCard,
  Badge,
  Text,
  TextTypes,
} from "@open-chat-go/ui"

type ModelRow = {
  uuid: string
  model_id: string
  title: string
  description: string
  is_public: boolean
  is_owned: boolean
  is_default?: boolean
  bots?: string[]
  configuration?: Record<string, unknown>
}

type ModelsResponse = {
  page: number
  page_size: number
  total_rows: number
  total_pages: number
  rows: ModelRow[]
  filters?: {
    hosters: string[]
    sources: string[]
    bots?: Array<{ uuid: string; name: string }>
  }
}

type SelfUser = { is_admin?: boolean }

type ModelsFilterQueryState = {
  page: number
  hoster: string
  source: string
  search: string
  bot: string
  botUUID: string
}

function parsePositiveInt(raw: string | null): number {
  if (!raw) return 1
  const value = Number.parseInt(raw, 10)
  if (!Number.isFinite(value) || value < 1) return 1
  return value
}

function readFilterQueryStateFromLocation(): ModelsFilterQueryState {
  if (typeof window === "undefined") {
    return { page: 1, hoster: "", source: "", search: "", bot: "", botUUID: "" }
  }
  const params = new URLSearchParams(window.location.search)
  return {
    page: parsePositiveInt(params.get("page")),
    hoster: (params.get("hoster") ?? "").trim(),
    source: (params.get("source") ?? "").trim(),
    search: (params.get("q") ?? "").trim(),
    bot: (params.get("bot") ?? "").trim(),
    botUUID: (params.get("bot_uuid") ?? "").trim(),
  }
}

export function ModelsCatalogView({
  mode = "private",
  showAdminDefaultSyncControls = false,
}: {
  mode?: "private" | "public"
  showAdminDefaultSyncControls?: boolean
}) {
  const [initialQueryState] = useState<ModelsFilterQueryState>(() => readFilterQueryStateFromLocation())
  const [page, setPage] = useState(initialQueryState.page)
  const [selectedModelUUIDs, setSelectedModelUUIDs] = useState<string[]>([])
  const [hosterFilter, setHosterFilter] = useState(initialQueryState.hoster)
  const [sourceFilter, setSourceFilter] = useState(initialQueryState.source)
  const [searchQuery, setSearchQuery] = useState(initialQueryState.search)
  const [botFilter, setBotFilter] = useState(initialQueryState.bot)
  const [botUUIDFilter, setBotUUIDFilter] = useState(initialQueryState.botUUID)
  const [isBulkDialogOpen, setIsBulkDialogOpen] = useState(false)
  const [detailModelUUID, setDetailModelUUID] = useState<string | null>(null)
  const [detailModelSeed, setDetailModelSeed] = useState<ModelRow | null>(null)
  const [targetBotUUID, setTargetBotUUID] = useState("")
  const [isApplyingSelection, setIsApplyingSelection] = useState(false)
  const [isReloadingDefaults, setIsReloadingDefaults] = useState(false)
  const [removingDefaultUUID, setRemovingDefaultUUID] = useState<string>("")
  const [defaultsActionStatus, setDefaultsActionStatus] = useState<string>("")
  const [isMobileRuntime, setIsMobileRuntime] = useState(false)
  const [showNativeScrollDebug, setShowNativeScrollDebug] = useState(false)
  const [nativeScrollDebugText, setNativeScrollDebugText] = useState("")
  const containerRef = useRef<HTMLDivElement | null>(null)
  const pageSize = 9
  const isPrivate = mode === "private"
  const { data: selfUser } = useSWR<SelfUser>(isPrivate ? "/api/v1/user/self" : null, fetcher)
  const canManageDefaultModels = isPrivate && selfUser?.is_admin && showAdminDefaultSyncControls
  const { data: providerCapabilities } = useSWR<{ defaults_file_writable?: boolean }>(
    canManageDefaultModels ? "/api/v1/admin/models/providers" : null,
    fetcher,
  )
  const defaultsFileWritable = providerCapabilities?.defaults_file_writable ?? false

  const listURL = useMemo(() => {
    const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) })
    if (hosterFilter) params.set("hoster", hosterFilter)
    if (sourceFilter) params.set("source", sourceFilter)
    if (searchQuery.trim()) params.set("q", searchQuery.trim())
    if (isPrivate && botFilter) params.set("bot", botFilter)
    if (isPrivate && botUUIDFilter) params.set("bot_uuid", botUUIDFilter)
    const base = "/api/v1/models"
    return `${base}?${params.toString()}`
  }, [botFilter, botUUIDFilter, hosterFilter, isPrivate, page, pageSize, searchQuery, sourceFilter])

  const { data, isLoading, error, mutate: revalidateModelsList } = useSWR<ModelsResponse>(listURL, fetcher)
  const detailModelURL = useMemo(() => {
    if (!detailModelUUID) return null
    return `/api/v1/models/${encodeURIComponent(detailModelUUID)}`
  }, [detailModelUUID])
  const { data: detailModelData, isLoading: isLoadingDetailModel } = useSWR<ModelRow>(detailModelURL, fetcher)
  const detailModel = detailModelData ?? detailModelSeed

  const selectedBotName = useMemo(() => {
    const options = data?.filters?.bots ?? []
    return options.find((bot) => bot.uuid === botUUIDFilter)?.name ?? ""
  }, [botUUIDFilter, data?.filters?.bots])

  const hasActiveFilters = Boolean(hosterFilter || sourceFilter || botFilter || botUUIDFilter || searchQuery.trim())
  const showDefaultCatalogControls = canManageDefaultModels && defaultsFileWritable && !isMobileRuntime

  useEffect(() => {
    setIsMobileRuntime(isMobileAppRuntime())
  }, [])

  useEffect(() => {
    if (typeof window === "undefined") return
    const params = new URLSearchParams(window.location.search)
    setShowNativeScrollDebug(params.get("debugNativeScroll") === "1")
  }, [])

  useEffect(() => {
    if (!isMobileRuntime || !showNativeScrollDebug) return
    const node = containerRef.current
    if (!node) return

    const updateDebug = () => {
      const max = Math.max(0, node.scrollHeight - node.clientHeight)
      setNativeScrollDebugText(`native-scroll top=${Math.round(node.scrollTop)} max=${Math.round(max)} h=${node.clientHeight}`)
    }

    updateDebug()
    node.addEventListener("scroll", updateDebug, { passive: true })
    return () => node.removeEventListener("scroll", updateDebug)
  }, [isMobileRuntime, showNativeScrollDebug])

  useEffect(() => {
    if (typeof window === "undefined") return

    const params = new URLSearchParams()
    if (page > 1) params.set("page", String(page))
    if (hosterFilter) params.set("hoster", hosterFilter)
    if (sourceFilter) params.set("source", sourceFilter)
    if (searchQuery.trim()) params.set("q", searchQuery.trim())
    if (isPrivate && botFilter) params.set("bot", botFilter)
    if (isPrivate && botUUIDFilter) params.set("bot_uuid", botUUIDFilter)

    const nextSearch = params.toString()
    const nextURL = `${window.location.pathname}${nextSearch ? `?${nextSearch}` : ""}${window.location.hash}`
    const currentURL = `${window.location.pathname}${window.location.search}${window.location.hash}`
    if (nextURL !== currentURL) {
      window.history.replaceState(window.history.state, "", nextURL)
    }
  }, [botFilter, botUUIDFilter, hosterFilter, isPrivate, page, searchQuery, sourceFilter])

  useEffect(() => {
    if (typeof window === "undefined") return

    const onPopState = () => {
      const next = readFilterQueryStateFromLocation()
      setPage(next.page)
      setHosterFilter(next.hoster)
      setSourceFilter(next.source)
      setSearchQuery(next.search)
      setBotFilter(next.bot)
      setBotUUIDFilter(next.botUUID)
    }

    window.addEventListener("popstate", onPopState)
    return () => window.removeEventListener("popstate", onPopState)
  }, [])

  const applySelectionToBot = async (action: "add_missing" | "remove_selected") => {
    if (!targetBotUUID || selectedModelUUIDs.length === 0) return
    setIsApplyingSelection(true)
    try {
      const response = await fetch(`/api/v1/admin/bots/${targetBotUUID}/models/selection`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ action, model_uuids: selectedModelUUIDs }),
      })
      if (!response.ok) throw new Error(await response.text())
      setIsBulkDialogOpen(false)
      setSelectedModelUUIDs([])
      await Promise.all([
        revalidateModelsList(),
        mutateCache((key) => typeof key === "string" && key.startsWith("/api/v1/models"), undefined, { revalidate: true }),
      ])
    } finally {
      setIsApplyingSelection(false)
    }
  }

  const reloadDefaultsFromDisk = async () => {
    setDefaultsActionStatus("")
    setIsReloadingDefaults(true)
    try {
      const response = await fetch("/api/v1/admin/models/defaults/reload-from-disk", {
        method: "POST",
        credentials: "include",
      })
      if (!response.ok) {
        throw new Error(await response.text())
      }
      const payload = await response.json() as {
        rows?: { created?: number; updated?: number; deleted?: number }
      }
      const rows = payload.rows ?? {}
      setDefaultsActionStatus(
        `Defaults reloaded (created: ${rows.created ?? 0}, updated: ${rows.updated ?? 0}, deleted: ${rows.deleted ?? 0}).`,
      )
      await Promise.all([
        revalidateModelsList(),
        mutateCache((key) => typeof key === "string" && key.startsWith("/api/v1/models"), undefined, { revalidate: true }),
      ])
    } catch (error) {
      setDefaultsActionStatus(error instanceof Error ? error.message : "Failed to reload defaults")
    } finally {
      setIsReloadingDefaults(false)
    }
  }

  const removeDefaultModel = async (modelUUID: string) => {
    setDefaultsActionStatus("")
    setRemovingDefaultUUID(modelUUID)
    try {
      const response = await fetch(`/api/v1/admin/models/defaults/${encodeURIComponent(modelUUID)}`, {
        method: "DELETE",
        credentials: "include",
      })
      if (!response.ok) {
        throw new Error(await response.text())
      }
      const payload = await response.json() as { persisted_to_file?: boolean }
      setDefaultsActionStatus(
        `Removed default model (${payload.persisted_to_file ? "defaults file + DB" : "DB only"}).`,
      )
      setDetailModelUUID(null)
      setDetailModelSeed(null)
      await Promise.all([
        revalidateModelsList(),
        mutateCache((key) => typeof key === "string" && key.startsWith("/api/v1/models"), undefined, { revalidate: true }),
      ])
    } catch (error) {
      setDefaultsActionStatus(error instanceof Error ? error.message : "Failed to remove default model")
    } finally {
      setRemovingDefaultUUID("")
    }
  }

  const cards = useMemo(() => {
    return (data?.rows ?? []).map((row) => {
      const configuration = row.configuration ?? {}
      const backend = typeof configuration.backend === "string" ? configuration.backend : undefined
      const maxTokens = typeof configuration.max_tokens === "number" ? configuration.max_tokens : undefined
      const context = typeof configuration.context === "number" ? configuration.context : undefined
      return {
        row,
        card: {
          uuid: row.uuid,
          title: row.title,
          modelId: row.model_id,
          description: row.description,
          backend,
          maxTokens,
          context,
          isDefault: row.is_default,
          bots: row.bots ?? [],
        },
      }
    })
  }, [data?.rows])

  return (
    <div
      ref={containerRef}
      className="max-h-full overflow-y-auto touch-pan-y [-webkit-overflow-scrolling:touch] space-y-4 pb-[calc(var(--openchat-safe-bottom,0px)+1rem)]"
      style={{ WebkitOverflowScrolling: "touch", touchAction: "pan-y" }}
    >
      {isMobileRuntime && showNativeScrollDebug ? (
        <Text type={TextTypes.Body7} color="muted">{nativeScrollDebugText || "native-scroll pending..."}</Text>
      ) : null}
      <div className="flex flex-col gap-3">
        <div className="min-w-0">
          <Text type={TextTypes.Heading5} tag="h1" bold>Models Overview</Text>
          <Text type={TextTypes.Body5} color="muted">Browse available chat models and their core settings.</Text>
          <Text type={TextTypes.Body6} color="muted" className="mt-1">
            {data?.total_rows ?? 0} total{isPrivate ? `, ${selectedModelUUIDs.length} selected` : ""}
          </Text>
        </div>

        <div className="w-full">
          <Input
            value={searchQuery}
            onChange={(e) => {
              setSearchQuery(e.target.value)
              setPage(1)
            }}
            placeholder="Search models..."
            className="h-9 w-full md:max-w-md"
          />
        </div>

        <div className="grid grid-cols-2 gap-2">
          <DropdownMenu><DropdownMenuTrigger asChild><Button variant="outline" size="sm" className="h-8 w-full justify-between px-2 text-xs"><span className="truncate">Hoster {hosterFilter ? `: ${hosterFilter}` : "(all)"}</span><ChevronDown className="h-3.5 w-3.5 opacity-70" /></Button></DropdownMenuTrigger><DropdownMenuContent align="start"><DropdownMenuLabel>Filter by model hoster</DropdownMenuLabel><DropdownMenuSeparator /><DropdownMenuItem onClick={() => { setHosterFilter(""); setPage(1) }}>All hosters</DropdownMenuItem>{(data?.filters?.hosters ?? []).map((hoster) => <DropdownMenuItem key={hoster} onClick={() => { setHosterFilter(hoster); setPage(1) }}>{hoster}</DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu>
          <DropdownMenu><DropdownMenuTrigger asChild><Button variant="outline" size="sm" className="h-8 w-full justify-between px-2 text-xs"><span className="truncate">Source {sourceFilter ? `: ${sourceFilter}` : "(all)"}</span><ChevronDown className="h-3.5 w-3.5 opacity-70" /></Button></DropdownMenuTrigger><DropdownMenuContent align="start"><DropdownMenuLabel>Filter by model source</DropdownMenuLabel><DropdownMenuSeparator /><DropdownMenuItem onClick={() => { setSourceFilter(""); setPage(1) }}>All sources</DropdownMenuItem>{(data?.filters?.sources ?? []).map((source) => <DropdownMenuItem key={source} onClick={() => { setSourceFilter(source); setPage(1) }}>{source}</DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu>
          {isPrivate ? (
            <>
              <DropdownMenu><DropdownMenuTrigger asChild><Button variant="outline" size="sm" className="h-8 w-full justify-between px-2 text-xs"><span className="truncate">Bot {botFilter ? `: ${botFilter}` : "(all)"}</span><ChevronDown className="h-3.5 w-3.5 opacity-70" /></Button></DropdownMenuTrigger><DropdownMenuContent align="start"><DropdownMenuLabel>Filter by bot</DropdownMenuLabel><DropdownMenuSeparator /><DropdownMenuItem onClick={() => { setBotFilter(""); setPage(1) }}>All bots</DropdownMenuItem>{(data?.filters?.bots ?? []).map((bot) => <DropdownMenuItem key={bot.uuid} onClick={() => { setBotFilter(bot.name); setPage(1) }}>{bot.name}</DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu>
              <div className="flex items-center gap-2">
                <DropdownMenu><DropdownMenuTrigger asChild><Button variant="outline" size="sm" className="h-8 min-w-0 flex-1 justify-between px-2 text-xs"><span className="truncate">Membership {selectedBotName ? `: ${selectedBotName}` : "(all)"}</span><ChevronDown className="h-3.5 w-3.5 opacity-70" /></Button></DropdownMenuTrigger><DropdownMenuContent align="start"><DropdownMenuLabel>Filter by bot assignment</DropdownMenuLabel><DropdownMenuSeparator /><DropdownMenuItem onClick={() => { setBotUUIDFilter(""); setPage(1) }}>All membership states</DropdownMenuItem>{(data?.filters?.bots ?? []).map((bot) => <DropdownMenuItem key={bot.uuid} onClick={() => { setBotUUIDFilter(bot.uuid); setPage(1) }}>{bot.name}</DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu>
                <Button variant="ghost" size="sm" className="h-8 shrink-0 px-2 text-xs" disabled={!hasActiveFilters} onClick={() => { setHosterFilter(""); setSourceFilter(""); setBotFilter(""); setBotUUIDFilter(""); setSearchQuery(""); setPage(1) }}>Clear</Button>
              </div>
            </>
          ) : (
            <Button variant="ghost" size="sm" className="h-8 w-full px-2 text-xs" disabled={!hasActiveFilters} onClick={() => { setHosterFilter(""); setSourceFilter(""); setBotFilter(""); setBotUUIDFilter(""); setSearchQuery(""); setPage(1) }}>Clear</Button>
          )}
        </div>
      </div>

      {isPrivate && selfUser?.is_admin ? (
        <div className="flex min-h-14 items-center justify-between rounded-xl border border-border/70 bg-card/80 p-3">
          <Text type={TextTypes.Body6} color="muted">{selectedModelUUIDs.length > 0 ? `Bulk selection actions for ${selectedModelUUIDs.length} selected model(s)` : "Select one or more models to enable bot bulk actions"}</Text>
          <Button variant="default" size="sm" disabled={selectedModelUUIDs.length === 0} onClick={() => setIsBulkDialogOpen(true)}>Add or remove selection from bot</Button>
        </div>
      ) : null}

      {showDefaultCatalogControls ? (
        <div className="space-y-2 rounded-xl border border-border/70 bg-card/80 p-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <Text type={TextTypes.Body6} color="muted">
              Default model catalog file controls
            </Text>
            <div className="flex flex-wrap items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={reloadDefaultsFromDisk}
                disabled={isReloadingDefaults}
              >
                {isReloadingDefaults ? "Reloading..." : "Reload Defaults from Disk"}
              </Button>
            </div>
          </div>
          {defaultsActionStatus ? (
            <Text type={TextTypes.Body7} color="muted">{defaultsActionStatus}</Text>
          ) : null}
        </div>
      ) : null}

      {canManageDefaultModels && !showDefaultCatalogControls ? (
        <div className="space-y-2 rounded-xl border border-border/70 bg-card/80 p-3">
          <Text type={TextTypes.Body7} color="muted">
            The defaults file is not writable in this environment, so file controls are hidden. Use
            Provider Search to add models to the DB. Add or remove models from the catalog via the
            Provider Search tab or per-model actions.
          </Text>
          {defaultsActionStatus ? (
            <Text type={TextTypes.Body7} color="muted">{defaultsActionStatus}</Text>
          ) : null}
        </div>
      ) : null}

      {isLoading ? <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">{Array.from({ length: pageSize }).map((_, idx) => <div key={idx} className="h-44 animate-pulse rounded-xl bg-muted" />)}</div> : null}
      {error ? <Text type={TextTypes.Body5} color="muted">Failed to load models.</Text> : null}
      {!isLoading && !error ? (
        <>
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
            {cards.map(({ row, card }) => (
              <ModelCard
                key={row.uuid}
                model={card}
                selectable={isPrivate && !!selfUser?.is_admin}
                selected={selectedModelUUIDs.includes(row.uuid)}
                onSelectChange={(checked) => setSelectedModelUUIDs((prev) => checked ? Array.from(new Set([...prev, row.uuid])) : prev.filter((id) => id !== row.uuid))}
                actions={(
                  <div className="flex w-full gap-2">
                    <Button size="sm" variant="outline" className="flex-1" onClick={() => {
                      setDetailModelSeed(row)
                      setDetailModelUUID(row.uuid)
                    }}>
                      <Info className="mr-1 h-4 w-4" />
                      Details
                    </Button>
                    {canManageDefaultModels && row.is_default ? (
                      <Button
                        size="sm"
                        variant="outline"
                        className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                        disabled={removingDefaultUUID === row.uuid}
                        onClick={() => removeDefaultModel(row.uuid)}
                      >
                        <Trash2 className="mr-1 h-4 w-4" />
                        {removingDefaultUUID === row.uuid ? "Removing" : "Remove"}
                      </Button>
                    ) : null}
                  </div>
                )}
              />
            ))}
          </div>
          <div className="flex items-center justify-between rounded-xl border border-border/70 bg-card/80 p-3">
            <Button variant="outline" size="sm" disabled={page <= 1} onClick={() => setPage((prev) => Math.max(1, prev - 1))}>Previous</Button>
            <Text type={TextTypes.Body6}>Page {data?.page ?? page} of {Math.max(1, data?.total_pages ?? 1)}</Text>
            <Button variant="outline" size="sm" disabled={page >= (data?.total_pages ?? 1)} onClick={() => setPage((prev) => prev + 1)}>Next</Button>
          </div>
        </>
      ) : null}

      <Dialog open={isBulkDialogOpen} onOpenChange={setIsBulkDialogOpen}>
        <DialogContent>
          <DialogHeader><DialogTitle>Apply selected models to a bot</DialogTitle><DialogDescription>Add missing selected models to a bot profile, or remove selected models from it.</DialogDescription></DialogHeader>
          <div className="space-y-2">
            <Text type={TextTypes.Body6} color="muted">Target bot</Text>
            <DropdownMenu>
              <DropdownMenuTrigger asChild><Button variant="outline" className="w-full justify-between"><span>{(data?.filters?.bots ?? []).find((bot) => bot.uuid === targetBotUUID)?.name || "Select a bot"}</span><ChevronDown className="h-4 w-4 opacity-70" /></Button></DropdownMenuTrigger>
              <DropdownMenuContent align="start" className="min-w-56">{(data?.filters?.bots ?? []).map((bot) => <DropdownMenuItem key={bot.uuid} onClick={() => setTargetBotUUID(bot.uuid)}>{bot.name}</DropdownMenuItem>)}</DropdownMenuContent>
            </DropdownMenu>
          </div>
          <DialogFooter>
            <Button variant="outline" disabled={!targetBotUUID || isApplyingSelection} onClick={() => applySelectionToBot("remove_selected")}>Remove selected from bot</Button>
            <Button disabled={!targetBotUUID || isApplyingSelection} onClick={() => applySelectionToBot("add_missing")}>Add missing selected to bot</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(detailModelUUID)} onOpenChange={(open) => {
        if (!open) {
          setDetailModelUUID(null)
          setDetailModelSeed(null)
        }
      }}>
        <DialogContent className="max-h-[88vh] max-w-4xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle className="break-all">{detailModel?.title || detailModel?.model_id || "Model Details"}</DialogTitle>
            <DialogDescription className="break-all">{detailModel?.model_id || ""}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            {isLoadingDetailModel ? (
              <div className="rounded-xl border border-border/70 bg-card/90 p-3">
                <Text type={TextTypes.Body6} color="muted">Loading latest model details...</Text>
              </div>
            ) : null}

            <div className="rounded-xl border border-border/70 bg-card/90 p-3">
              <Text type={TextTypes.Body6} bold>Description</Text>
              <Text type={TextTypes.Body6} color="muted" className="mt-1">{detailModel?.description || "No description available."}</Text>
              <div className="mt-3 flex flex-wrap gap-2">
                {detailModel?.is_public ? <Badge variant="secondary">Public</Badge> : <Badge variant="outline">Private</Badge>}
                {detailModel?.is_owned ? <Badge variant="secondary">Owned by you</Badge> : null}
                {detailModel?.is_default ? <Badge variant="outline">Default catalog model</Badge> : null}
              </div>
            </div>

            <div className="rounded-xl border border-border/70 bg-card/90 p-3">
              <Text type={TextTypes.Body6} bold>Configuration</Text>
              <pre className="mt-3 max-h-72 overflow-auto rounded-lg border border-border/60 bg-muted/30 p-3 text-xs leading-5">
                {JSON.stringify(detailModel?.configuration ?? {}, null, 2)}
              </pre>
            </div>

            {canManageDefaultModels && detailModel?.is_default && detailModelUUID ? (
              <div className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-destructive/40 bg-destructive/5 p-3">
                <div className="min-w-0">
                  <Text type={TextTypes.Body6} bold>Default catalog</Text>
                  <Text type={TextTypes.Body7} color="muted" className="mt-0.5">
                    {defaultsFileWritable
                      ? "Removing deletes this model from the defaults file and the DB."
                      : "Removing deletes this model from the DB (defaults file is not writable here)."}
                  </Text>
                </div>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={removingDefaultUUID === detailModelUUID}
                  onClick={() => removeDefaultModel(detailModelUUID)}
                >
                  {removingDefaultUUID === detailModelUUID ? "Removing..." : "Remove from defaults"}
                </Button>
              </div>
            ) : null}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
