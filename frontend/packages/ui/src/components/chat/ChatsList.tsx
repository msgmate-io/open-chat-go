import { ChatItemCompact } from "./ChatItem"
import { ProfileCard } from "./ProfileCard"
import { LoadingSpinner } from "./loading-spinner"
import { getChatTimeBucket } from "../../lib/date"
import useSWR from 'swr'
import { DefaultChats } from "./DefaultChats"
import { NewChatCard } from "./NewChatCard"
import type React from "react"
import { useState, useCallback, useEffect, useMemo, useRef } from "react"
import {
    DropdownMenu,
    DropdownMenuCheckboxItem,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuSeparator,
    DropdownMenuTrigger
} from "../dropdown-menu"
import { Button } from "../button"
import { Input } from "../input"
import { Text, TextTypes } from "../text"
import { Ban, Calendar, Check, Search, SlidersHorizontal, Tag, X } from "lucide-react"
import { useCurrentUser } from "../../integration/hooks/use-current-user"
import { listChatSearchActions } from "../../integration/components/chat/chat-ui/registry"

const fetcher = (...args: [RequestInfo, RequestInit?]) => fetch(...args).then(res => res.json())

export const ExploreChatsIcon = () => (
    <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" fill="none" viewBox="0 0 24 24" className="icon-md">
        <path
            fill="currentColor"
            fillRule="evenodd"
            d="M6.75 4.5a2.25 2.25 0 1 0 0 4.5 2.25 2.25 0 0 0 0-4.5M2.5 6.75a4.25 4.25 0 1 1 8.5 0 4.25 4.25 0 0 1-8.5 0M17.25 4.5a2.25 2.25 0 1 0 0 4.5 2.25 2.25 0 0 0 0-4.5M13 6.75a4.25 4.25 0 1 1 8.5 0 4.25 4.25 0 0 1-8.5 0M6.75 15a2.25 2.25 0 1 0 0 4.5 2.25 2.25 0 0 0 0-4.5M2.5 17.25a4.25 4.25 0 1 1 8.5 0 4.25 4.25 0 0 1-8.5 0M17.25 15a2.25 2.25 0 1 0 0 4.5 2.25 2.25 0 0 0 0-4.5M13 17.25a4.25 4.25 0 1 1 8.5 0 4.25 4.25 0 0 1-8.5 0"
            clipRule="evenodd"
        />
    </svg>
);

export const SettingsIcon = () => (
    <SlidersHorizontal className="size-4" />
);

// Small removable chip used to surface an active search / time / tag filter.
// Excluded-tag chips use a destructive tone so they read as "hide this tag".
const FilterChip = ({ label, onRemove, tone = "default" }: { label: string; onRemove: () => void; tone?: "default" | "exclude" }) => (
    <span
        className={
            tone === "exclude"
                ? "inline-flex max-w-full items-center gap-1 rounded-full border border-destructive/40 bg-destructive/10 px-2 py-[2px] text-[10px] text-destructive"
                : "inline-flex max-w-full items-center gap-1 rounded-full border border-border/70 bg-secondary px-2 py-[2px] text-[10px] text-foreground"
        }
    >
        {tone === "exclude" ? <Ban className="size-3" /> : null}
        <span className="max-w-[10rem] truncate">{label}</span>
        <button
            type="button"
            aria-label={`Remove ${label} filter`}
            className="text-muted-foreground transition-colors hover:text-foreground"
            onClick={onRemove}
        >
            <X className="size-3" />
        </button>
    </span>
);

type ChatType = "conversation" | "integration" | "interaction"

const CHAT_TYPE_QUERY_PARAM = "chat_type"
const CHAT_TYPE_ORDER: ChatType[] = ["conversation", "integration", "interaction"]
const DEFAULT_CHAT_TYPES: ChatType[] = ["conversation", "interaction"]
const CHAT_TYPE_LABELS: Record<ChatType, string> = {
    conversation: "Conversations",
    integration: "Integrations",
    interaction: "Interactions",
}

const isChatType = (value: string | null): value is ChatType => {
    return value === "conversation" || value === "integration" || value === "interaction"
}

const parseChatTypes = (value: string | null): ChatType[] => {
    if (!value) {
        return []
    }
    const parsed = value
        .split(",")
        .map((entry) => entry.trim())
        .filter(isChatType)
    return CHAT_TYPE_ORDER.filter((type) => parsed.includes(type))
}

const getChatTypesFromUrl = (): ChatType[] => {
    if (typeof window === "undefined") {
        return DEFAULT_CHAT_TYPES
    }
    const parsed = parseChatTypes(new URLSearchParams(window.location.search).get(CHAT_TYPE_QUERY_PARAM))
    return parsed.length > 0 ? parsed : DEFAULT_CHAT_TYPES
}

const getExplicitChatTypesFromUrl = (): ChatType[] | null => {
    if (typeof window === "undefined") {
        return null
    }
    const raw = new URLSearchParams(window.location.search).get(CHAT_TYPE_QUERY_PARAM)
    if (!raw) {
        return null
    }
    const parsed = parseChatTypes(raw)
    return parsed.length > 0 ? parsed : null
}

const isDefaultChatTypes = (types: ChatType[]): boolean => {
    if (types.length !== DEFAULT_CHAT_TYPES.length) {
        return false
    }
    return DEFAULT_CHAT_TYPES.every((type) => types.includes(type))
}

const serializeChatTypes = (types: ChatType[]): string =>
    CHAT_TYPE_ORDER.filter((type) => types.includes(type)).join(",")

const SEARCH_QUERY_PARAM = "q"
const TIME_FROM_QUERY_PARAM = "time_from"
const TIME_TO_QUERY_PARAM = "time_to"
const TAGS_QUERY_PARAM = "tags"
const EXCLUDE_TAGS_QUERY_PARAM = "exclude_tags"

// Chats generated by the internal automations and the git trigger integration
// are noisy for a human reading their chat list, so hide them unless the user
// explicitly opts back in via the tag filter.
const DEFAULT_EXCLUDED_TAGS = ["automation", "trigger"]

const getStringParam = (name: string): string => {
    if (typeof window === "undefined") {
        return ""
    }
    return new URLSearchParams(window.location.search).get(name) ?? ""
}

const parseTagsParam = (value: string): string[] =>
    value
        .split(",")
        .map((tag) => tag.trim().toLowerCase())
        .filter((tag) => tag.length > 0)

const isDefaultExcludedTags = (tags: string[]): boolean => {
    if (tags.length !== DEFAULT_EXCLUDED_TAGS.length) {
        return false
    }
    return DEFAULT_EXCLUDED_TAGS.every((tag) => tags.includes(tag))
}

// An absent `exclude_tags` param means "use the defaults"; an explicitly empty
// one (`?exclude_tags=`) means the user cleared the exclusion entirely, so the
// two cases must stay distinguishable.
const getExcludeTagsFromUrl = (): string[] => {
    if (typeof window === "undefined") {
        return DEFAULT_EXCLUDED_TAGS
    }
    const raw = new URLSearchParams(window.location.search).get(EXCLUDE_TAGS_QUERY_PARAM)
    if (raw === null) {
        return DEFAULT_EXCLUDED_TAGS
    }
    return parseTagsParam(raw)
}

// datetime-local inputs work in the browser's local time; the API expects
// RFC3339, so convert explicitly when reading/writing.
const toLocalInputValue = (iso: string): string => {
    if (!iso) {
        return ""
    }
    const date = new Date(iso)
    if (Number.isNaN(date.getTime())) {
        return ""
    }
    const pad = (value: number) => String(value).padStart(2, "0")
    return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

const localInputToISO = (value: string): string => {
    if (!value) {
        return ""
    }
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) {
        return ""
    }
    return date.toISOString()
}

const formatTimeChip = (value: string): string => {
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) {
        return value
    }
    return date.toLocaleString(undefined, {
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
    })
}

// Chat types can be namespaced (e.g. "interaction:foo"); map any such value
// onto the filter buckets the UI supports (matches backend prefix matching).
const normalizeChatType = (value: unknown): ChatType | null => {
    if (typeof value !== "string" || value.length === 0) {
        return null
    }
    if (value === "interaction" || value.startsWith("interaction:")) {
        return "interaction"
    }
    if (value === "integration" || value.startsWith("integration:")) {
        return "integration"
    }
    return "conversation"
}

export function ChatsList({
    chatUUID,
    leftPannelCollapsed,
    onToggleCollapse,
    navigateTo,
    themeSelector,
    avatarSrc,
    hideCollapseToggle = false,
    title = "Chats",
    topSection,
    showDefaultChats = true,
    showChats = true,
}: {
    chatUUID: string | null,
    leftPannelCollapsed: boolean;
    onToggleCollapse: () => void;    
    navigateTo: (to: string) => void
    themeSelector: React.ReactNode
    avatarSrc?: string
    hideCollapseToggle?: boolean
    title?: string
    topSection?: React.ReactNode
    showDefaultChats?: boolean
    showChats?: boolean
}) {
    const [filterMenuOpen, setFilterMenuOpen] = useState(false)
    const [chatTypeFilters, setChatTypeFilters] = useState<ChatType[]>(() => getChatTypesFromUrl())
    // Captured once so a direct link that explicitly carries chat_type is never overridden.
    const [initialExplicitChatTypes] = useState<ChatType[] | null>(() => getExplicitChatTypesFromUrl())
    // Distinguishes a user's manual filter choice from an auto-derived one.
    const userSelectedChatTypeRef = useRef(false)

    // Text search over chat titles (the magnifying-glass filter).
    const [searchOpen, setSearchOpen] = useState(() => getStringParam(SEARCH_QUERY_PARAM).length > 0)
    const [searchQuery, setSearchQuery] = useState(() => getStringParam(SEARCH_QUERY_PARAM))
    // Multi-select tag/category filter. Each tag can be included, excluded or
    // neutral; excludes hide chats carrying the tag.
    const [tagsMenuOpen, setTagsMenuOpen] = useState(false)
    const [tagFilters, setTagFilters] = useState<string[]>(() => parseTagsParam(getStringParam(TAGS_QUERY_PARAM)))
    const [excludeTagFilters, setExcludeTagFilters] = useState<string[]>(() => getExcludeTagsFromUrl())
    // Start/end time range filter. `timeFrom`/`timeTo` are the applied RFC3339
    // values (used for requests/URL); the drafts hold the local input value.
    const [timeOpen, setTimeOpen] = useState(false)
    const [timeFrom, setTimeFrom] = useState(() => getStringParam(TIME_FROM_QUERY_PARAM))
    const [timeTo, setTimeTo] = useState(() => getStringParam(TIME_TO_QUERY_PARAM))
    const [timeFromDraft, setTimeFromDraft] = useState(() => toLocalInputValue(getStringParam(TIME_FROM_QUERY_PARAM)))
    const [timeToDraft, setTimeToDraft] = useState(() => toLocalInputValue(getStringParam(TIME_TO_QUERY_PARAM)))
    const [timeError, setTimeError] = useState("")

    const syncChatTypesToUrl = useCallback((nextChatTypes: ChatType[]) => {
        if (typeof window === "undefined") {
            return
        }

        const nextUrl = new URL(window.location.href)
        if (isDefaultChatTypes(nextChatTypes)) {
            nextUrl.searchParams.delete(CHAT_TYPE_QUERY_PARAM)
        } else {
            nextUrl.searchParams.set(CHAT_TYPE_QUERY_PARAM, serializeChatTypes(nextChatTypes))
        }

        window.history.replaceState(window.history.state, "", `${nextUrl.pathname}${nextUrl.search}${nextUrl.hash}`)
    }, [])

    const syncExcludeTagsToUrl = useCallback((nextExcludeTags: string[]) => {
        if (typeof window === "undefined") {
            return
        }

        const nextUrl = new URL(window.location.href)
        if (isDefaultExcludedTags(nextExcludeTags)) {
            nextUrl.searchParams.delete(EXCLUDE_TAGS_QUERY_PARAM)
        } else {
            nextUrl.searchParams.set(EXCLUDE_TAGS_QUERY_PARAM, nextExcludeTags.join(","))
        }

        window.history.replaceState(window.history.state, "", `${nextUrl.pathname}${nextUrl.search}${nextUrl.hash}`)
    }, [])

    const handleChatTypeToggle = useCallback((value: string, checked: boolean) => {
        if (!isChatType(value)) {
            return
        }
        userSelectedChatTypeRef.current = true
        setChatTypeFilters((current) => {
            if (checked) {
                return current.includes(value) ? current : [...current, value]
            }
            if (!current.includes(value) || current.length === 1) {
                return current
            }
            return current.filter((type) => type !== value)
        })
    }, [])

    const handleResetFilters = useCallback(() => {
        userSelectedChatTypeRef.current = true
        setChatTypeFilters(DEFAULT_CHAT_TYPES)
        setSearchQuery("")
        setTagFilters([])
        setExcludeTagFilters(DEFAULT_EXCLUDED_TAGS)
        setTimeFrom("")
        setTimeTo("")
        setTimeFromDraft("")
        setTimeToDraft("")
        setTimeError("")
    }, [])

    const handleTagToggle = useCallback((tag: string, checked: boolean) => {
        setTagFilters((current) => {
            if (checked) {
                return current.includes(tag) ? current : [...current, tag]
            }
            return current.filter((entry) => entry !== tag)
        })
    }, [])

    // Cycles a tag through neutral -> include -> exclude -> neutral. A tag is
    // never both included and excluded.
    const handleTagCycle = useCallback((tag: string) => {
        if (tagFilters.includes(tag)) {
            setTagFilters((current) => current.filter((entry) => entry !== tag))
            setExcludeTagFilters((current) => (current.includes(tag) ? current : [...current, tag]))
            return
        }
        if (excludeTagFilters.includes(tag)) {
            setExcludeTagFilters((current) => current.filter((entry) => entry !== tag))
            return
        }
        setTagFilters((current) => (current.includes(tag) ? current : [...current, tag]))
    }, [tagFilters, excludeTagFilters])

    const handleApplyTimeRange = useCallback(() => {
        const fromISO = localInputToISO(timeFromDraft)
        const toISO = localInputToISO(timeToDraft)
        if (fromISO && toISO && new Date(fromISO).getTime() > new Date(toISO).getTime()) {
            setTimeError("Start must be before end")
            return
        }
        setTimeError("")
        setTimeFrom(fromISO)
        setTimeTo(toISO)
        setTimeOpen(false)
    }, [timeFromDraft, timeToDraft])

    const handleClearTimeRange = useCallback(() => {
        setTimeFrom("")
        setTimeTo("")
        setTimeFromDraft("")
        setTimeToDraft("")
        setTimeError("")
    }, [])

    const setUrlParam = useCallback((name: string, value: string) => {
        if (typeof window === "undefined") {
            return
        }
        const nextUrl = new URL(window.location.href)
        if (value) {
            nextUrl.searchParams.set(name, value)
        } else {
            nextUrl.searchParams.delete(name)
        }
        window.history.replaceState(window.history.state, "", `${nextUrl.pathname}${nextUrl.search}${nextUrl.hash}`)
    }, [])

    useEffect(() => {
        const onPopState = () => {
            setChatTypeFilters(getChatTypesFromUrl())
            setSearchQuery(getStringParam(SEARCH_QUERY_PARAM))
            setTagFilters(parseTagsParam(getStringParam(TAGS_QUERY_PARAM)))
            setExcludeTagFilters(getExcludeTagsFromUrl())
            setTimeFrom(getStringParam(TIME_FROM_QUERY_PARAM))
            setTimeTo(getStringParam(TIME_TO_QUERY_PARAM))
            setTimeFromDraft(toLocalInputValue(getStringParam(TIME_FROM_QUERY_PARAM)))
            setTimeToDraft(toLocalInputValue(getStringParam(TIME_TO_QUERY_PARAM)))
        }

        window.addEventListener("popstate", onPopState)
        return () => window.removeEventListener("popstate", onPopState)
    }, [])

    useEffect(() => {
        syncChatTypesToUrl(chatTypeFilters)
    }, [chatTypeFilters, syncChatTypesToUrl])

    useEffect(() => {
        setUrlParam(SEARCH_QUERY_PARAM, searchQuery.trim())
    }, [searchQuery, setUrlParam])

    useEffect(() => {
        setUrlParam(TAGS_QUERY_PARAM, tagFilters.join(","))
    }, [tagFilters, setUrlParam])

    useEffect(() => {
        syncExcludeTagsToUrl(excludeTagFilters)
    }, [excludeTagFilters, syncExcludeTagsToUrl])

    useEffect(() => {
        setUrlParam(TIME_FROM_QUERY_PARAM, timeFrom)
        setUrlParam(TIME_TO_QUERY_PARAM, timeTo)
    }, [timeFrom, timeTo, setUrlParam])

    const { data: currentChat } = useSWR(
        chatUUID ? `/api/v1/chats/${chatUUID}` : null,
        fetcher,
        { revalidateOnFocus: false }
    )

    // A chat page opened without an explicit filter should preselect the filter
    // that matches the chat's own type; the sync effect above then appends it to
    // the URL (replaceState, no reload).
    useEffect(() => {
        if (!chatUUID || initialExplicitChatTypes || userSelectedChatTypeRef.current) {
            return
        }
        const derivedChatType = normalizeChatType(currentChat?.chat_type)
        if (!derivedChatType) {
            return
        }
        setChatTypeFilters((current) => (current.includes(derivedChatType) ? current : [...current, derivedChatType]))
    }, [chatUUID, currentChat?.chat_type, initialExplicitChatTypes])

    const navigateWithFilter = useCallback((to: string) => {
        if (!to.startsWith("/chat")) {
            navigateTo(to)
            return
        }

        const params = new URLSearchParams()
        if (!isDefaultChatTypes(chatTypeFilters)) {
            params.set(CHAT_TYPE_QUERY_PARAM, serializeChatTypes(chatTypeFilters))
        }
        if (searchQuery.trim()) {
            params.set(SEARCH_QUERY_PARAM, searchQuery.trim())
        }
        if (tagFilters.length > 0) {
            params.set(TAGS_QUERY_PARAM, tagFilters.join(","))
        }
        if (!isDefaultExcludedTags(excludeTagFilters)) {
            params.set(EXCLUDE_TAGS_QUERY_PARAM, excludeTagFilters.join(","))
        }
        if (timeFrom) {
            params.set(TIME_FROM_QUERY_PARAM, timeFrom)
        }
        if (timeTo) {
            params.set(TIME_TO_QUERY_PARAM, timeTo)
        }

        const query = params.toString()
        if (!query) {
            navigateTo(to)
            return
        }

        const separator = to.includes("?") ? "&" : "?"
        navigateTo(`${to}${separator}${query}`)
    }, [chatTypeFilters, searchQuery, tagFilters, excludeTagFilters, timeFrom, timeTo, navigateTo])

    // If the chat that is currently open gets deleted from the list, leave the
    // now-missing chat page.
    const handleChatDeleted = useCallback((deletedChatUUID: string) => {
        if (chatUUID && deletedChatUUID === chatUUID) {
            navigateTo("/chat/new")
        }
    }, [chatUUID, navigateTo])

    const activeFilterLabel = useMemo(
        () =>
            CHAT_TYPE_ORDER.filter((type) => chatTypeFilters.includes(type))
                .map((type) => CHAT_TYPE_LABELS[type])
                .join(", "),
        [chatTypeFilters]
    )
    const { data: currentUser } = useCurrentUser()
    const isAdmin = currentUser?.is_admin === true
    const [seeAll, setSeeAll] = useState(false)

    const hasSearchFilter = searchQuery.trim().length > 0
    const hasTagFilter = tagFilters.length > 0
    const hasExcludeTagFilter = excludeTagFilters.length > 0
    const hasNonDefaultExcludeTagFilter = !isDefaultExcludedTags(excludeTagFilters)
    const hasTimeFilter = Boolean(timeFrom || timeTo)
    const extraFilterCount = (hasSearchFilter ? 1 : 0) + (hasTagFilter ? 1 : 0) + (hasTimeFilter ? 1 : 0) + (hasNonDefaultExcludeTagFilter ? 1 : 0)
    
    const chatsListUrl = useCallback(() => {
        if (!showChats) {
            return null
        }
        const params = new URLSearchParams()
        params.set("chat_types", serializeChatTypes(chatTypeFilters))
        if (searchQuery.trim()) {
            params.set(SEARCH_QUERY_PARAM, searchQuery.trim())
        }
        if (tagFilters.length > 0) {
            params.set(TAGS_QUERY_PARAM, tagFilters.join(","))
        }
        if (excludeTagFilters.length > 0) {
            params.set(EXCLUDE_TAGS_QUERY_PARAM, excludeTagFilters.join(","))
        }
        if (timeFrom) {
            params.set(TIME_FROM_QUERY_PARAM, timeFrom)
        }
        if (timeTo) {
            params.set(TIME_TO_QUERY_PARAM, timeTo)
        }
        if (seeAll && isAdmin) {
            params.set("scope", "all")
        }
        return `/api/v1/chats/list?${params.toString()}`
    }, [chatTypeFilters, searchQuery, tagFilters, excludeTagFilters, timeFrom, timeTo, showChats, seeAll, isAdmin])

    const { data: availableTags } = useSWR<{ tags?: string[] }>(
        showChats ? `/api/v1/chats/tags` : null,
        fetcher,
        { revalidateOnFocus: true }
    )

    const knownTags = useMemo(() => {
        const set = new Set<string>(availableTags?.tags ?? [])
        tagFilters.forEach((tag) => set.add(tag))
        excludeTagFilters.forEach((tag) => set.add(tag))
        return Array.from(set).sort()
    }, [availableTags, tagFilters, excludeTagFilters])

    const { data: chats, isLoading, mutate: mutateChats } = useSWR(chatsListUrl, fetcher, {
        // Keep the list fresh so newly created chats show up without a manual
        // reload, but pause polling while the tab is hidden.
        refreshInterval: () => (typeof document !== "undefined" && document.hidden ? 0 : 7000),
        revalidateOnFocus: true,
    })
    const { data: defaultBotContact, isLoading: defaultBotLoading } = useSWR<{ contact_token?: string; name?: string } | null>(
        showChats ? `/api/v1/contacts/default-bot` : null,
        fetcher,
    )

    const botChatUuids = useMemo(() => {
        if (!chats?.rows) {
            return null
        }
        const uuids = chats.rows
            .filter((chat: { uuid?: string; partner?: { is_automated?: boolean } }) =>
                Boolean(chat?.uuid && chat?.partner?.is_automated)
            )
            .map((chat: { uuid?: string }) => chat.uuid as string)
        return uuids.length > 0 ? uuids : null
    }, [chats])

    const chatStatesKey = useMemo(
        () => (botChatUuids ? `/api/v1/chats/states?chat_uuids=${botChatUuids.join(",")}` : null),
        [botChatUuids]
    )

    const { data: chatStates, mutate: mutateChatStates } = useSWR(
        chatStatesKey,
        fetcher,
        {
            refreshInterval: () => (typeof document !== "undefined" && document.hidden ? 0 : 3000),
            revalidateOnFocus: true,
        }
    )

    const { data: actionTasksCount, mutate: mutateActionTasksCount } = useSWR<{ count: number }>(
        showDefaultChats ? `/api/v1/chats/action-tasks?count_only=1` : null,
        fetcher,
        {
            refreshInterval: () => (typeof document !== "undefined" && document.hidden ? 0 : 5000),
            revalidateOnFocus: true,
        }
    )

    useEffect(() => {
        if (typeof document === "undefined") {
            return
        }
        const onVisibilityChange = () => {
            if (!document.hidden) {
                mutateChatStates()
                mutateChats()
                mutateActionTasksCount()
            }
        }
        document.addEventListener("visibilitychange", onVisibilityChange)
        return () => document.removeEventListener("visibilitychange", onVisibilityChange)
    }, [mutateChatStates, mutateChats, mutateActionTasksCount])

    const chatStateByUuid = useMemo(() => {
        const map: Record<string, string> = {}
        for (const row of chatStates?.states ?? []) {
            map[row.chat_uuid] = row.state
        }
        return map
    }, [chatStates])

    const actionCount = typeof actionTasksCount?.count === "number" ? actionTasksCount.count : 0

    const FilterMenu = () => (
        <DropdownMenu open={filterMenuOpen} onOpenChange={setFilterMenuOpen}>
            <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon" className="relative size-7" aria-label="Filter chats">
                    <SettingsIcon />
                    {!isDefaultChatTypes(chatTypeFilters) ? (
                        <span className="bg-primary absolute top-1 right-1 size-1.5 rounded-full" aria-hidden="true" />
                    ) : null}
                </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-56" onCloseAutoFocus={(e) => e.preventDefault()}>
                <DropdownMenuLabel>Filter chats</DropdownMenuLabel>
                <DropdownMenuSeparator />
                {CHAT_TYPE_ORDER.map((chatType) => (
                    <DropdownMenuCheckboxItem
                        key={chatType}
                        checked={chatTypeFilters.includes(chatType)}
                        onCheckedChange={(checked) => handleChatTypeToggle(chatType, checked === true)}
                        onSelect={(event) => event.preventDefault()}
                    >
                        {CHAT_TYPE_LABELS[chatType]}
                    </DropdownMenuCheckboxItem>
                ))}
            </DropdownMenuContent>
        </DropdownMenu>
    );

    const TagsMenu = () => (
        <DropdownMenu open={tagsMenuOpen} onOpenChange={setTagsMenuOpen}>
            <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon" className="relative size-7" aria-label="Filter by tag">
                    <Tag className="size-4" />
                    {hasTagFilter || hasNonDefaultExcludeTagFilter ? (
                        <span className="bg-primary absolute top-1 right-1 size-1.5 rounded-full" aria-hidden="true" />
                    ) : null}
                </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-60" onCloseAutoFocus={(e) => e.preventDefault()}>
                <DropdownMenuLabel>Filter by tag</DropdownMenuLabel>
                <div className="px-2 pb-1.5">
                    <Text type={TextTypes.Body7} color="muted">
                        Click to include, again to exclude
                    </Text>
                </div>
                <DropdownMenuSeparator />
                {knownTags.length === 0 ? (
                    <div className="px-3 py-2">
                        <Text type={TextTypes.Body7} color="muted">
                            No tags yet
                        </Text>
                    </div>
                ) : (
                    knownTags.map((tag) => {
                        const state = tagFilters.includes(tag)
                            ? "include"
                            : excludeTagFilters.includes(tag)
                                ? "exclude"
                                : "neutral"
                        return (
                            <DropdownMenuItem
                                key={tag}
                                className="relative pl-8"
                                onSelect={(event) => {
                                    event.preventDefault()
                                    handleTagCycle(tag)
                                }}
                            >
                                <span className="absolute left-2 flex size-3.5 items-center justify-center">
                                    {state === "include" ? <Check className="text-primary size-4" /> : null}
                                    {state === "exclude" ? <Ban className="text-destructive size-4" /> : null}
                                </span>
                                <span className={state === "exclude" ? "text-muted-foreground line-through" : undefined}>
                                    {tag}
                                </span>
                            </DropdownMenuItem>
                        )
                    })
                )}
            </DropdownMenuContent>
        </DropdownMenu>
    );

    const renderDivider = (label: string) => (
        <div className="chat-list-divider" key={label}>
            <Text type={TextTypes.Body7} color="muted" tag="span" bold>
                {label}
            </Text>
        </div>
    );

    const renderChatItems = () => {
        if (!chats) {
            return (
                <div className="flex h-40 items-center justify-center">
                    <LoadingSpinner />
                </div>
            );
        }

        if (chats.rows.length === 0) {
            const isDefaultFilter = isDefaultChatTypes(chatTypeFilters) && extraFilterCount === 0

            return (
                <div className="flex flex-col items-center px-4 py-8 text-center">
                    <div className="mb-3 text-muted-foreground">
                        <ExploreChatsIcon />
                    </div>
                    <Text type={TextTypes.Body5} tag="p" bold>
                        {!isDefaultChatTypes(chatTypeFilters)
                            ? `No ${activeFilterLabel.toLowerCase()} yet`
                            : extraFilterCount > 0
                                ? "No matching chats"
                                : "No chats yet"}
                    </Text>
                    <Text type={TextTypes.Body7} color="muted" className="mt-1 mb-4">
                        {isDefaultFilter
                            ? "Start a new chat or try one of the default options above."
                            : extraFilterCount > 0
                                ? "Try adjusting or clearing your filters."
                                : "Try another filter or start a new chat in this category."}
                    </Text>
                    {!isDefaultFilter ? (
                        <Button
                            variant="ghost"
                            size="sm"
                            className="mb-2"
                            onClick={handleResetFilters}
                        >
                            Clear filters
                        </Button>
                    ) : null}
                    <Button onClick={() => navigateWithFilter('/chat/new')} size="sm">
                        New chat
                    </Button>
                </div>
            );
        }

        // Sort by most recent activity so the fine-grained time dividers group
        // consecutive rows into a single, readable heading.
        const rows = [...chats.rows].sort(
            (
                a: { latest_message_at?: string },
                b: { latest_message_at?: string }
            ) => {
                const aTime = a.latest_message_at ? new Date(a.latest_message_at).getTime() : 0
                const bTime = b.latest_message_at ? new Date(b.latest_message_at).getTime() : 0
                return bTime - aTime
            }
        )

        let lastDivider: string | null = null

        return rows.flatMap(
            (chat: {
                uuid: string
                settings?: { title?: string }
                latest_message?: { text?: string }
                latest_message_at?: string
                tags?: string[]
            }) => {
                const parsed = chat.latest_message_at ? new Date(chat.latest_message_at) : null
                const chatDate = parsed && !Number.isNaN(parsed.getTime()) ? parsed : null
                let divider = null

                if (chatDate) {
                    const bucket = getChatTimeBucket(chatDate)
                    if (bucket.key !== lastDivider) {
                        divider = renderDivider(bucket.label)
                        lastDivider = bucket.key
                    }
                }

                return [
                    divider,
                    <ChatItemCompact
                        chat={chat}
                        key={`chat_${chat.uuid}`}
                        isSelected={chat.uuid === chatUUID}
                        navigateTo={navigateWithFilter}
                        state={chatStateByUuid[chat.uuid]}
                        tags={chat.tags}
                        onDeleted={handleChatDeleted}
                    />,
                ].filter(Boolean)
            }
        )
    };

    return (
        <div className="flex h-full w-full flex-col">
            <NewChatCard
                leftPannelCollapsed={leftPannelCollapsed}
                onToggleCollapse={onToggleCollapse}
                navigateTo={navigateWithFilter}
                showCollapseToggle={!hideCollapseToggle}
                title={title}
            />
            <div className="chat-list-scroll">
                {topSection}
                {showDefaultChats ? (
                    <DefaultChats
                        navigateTo={navigateWithFilter}
                        defaultBotContact={defaultBotContact}
                        isBotLoading={defaultBotLoading}
                        botAvatarSrc={avatarSrc}
                        actionCount={actionCount}
                    />
                ) : null}
                {showChats ? (
                    <>
                        <div className="chat-list-divider">
                            <Text type={TextTypes.Body7} color="muted" tag="span" bold>
                                Filters
                            </Text>
                            <div className="flex items-center gap-1">
                                {isAdmin ? (
                                    <Button
                                        variant={seeAll ? "default" : "ghost"}
                                        size="sm"
                                        className="h-6 px-2 text-[10px]"
                                        onClick={() => setSeeAll((value) => !value)}
                                        title="Show chats owned by all users (admin)"
                                    >
                                        See all
                                    </Button>
                                ) : null}
                                <Button
                                    variant={searchOpen ? "default" : "ghost"}
                                    size="icon"
                                    className="relative size-7"
                                    aria-label="Search chat titles"
                                    onClick={() => setSearchOpen((value) => !value)}
                                >
                                    <Search className="size-4" />
                                    {hasSearchFilter ? (
                                        <span className="bg-primary absolute top-1 right-1 size-1.5 rounded-full" aria-hidden="true" />
                                    ) : null}
                                </Button>
                                <Button
                                    variant={timeOpen || hasTimeFilter ? "default" : "ghost"}
                                    size="icon"
                                    className="relative size-7"
                                    aria-label="Filter by time range"
                                    onClick={() => setTimeOpen((value) => !value)}
                                >
                                    <Calendar className="size-4" />
                                    {hasTimeFilter ? (
                                        <span className="bg-primary absolute top-1 right-1 size-1.5 rounded-full" aria-hidden="true" />
                                    ) : null}
                                </Button>
                                <TagsMenu />
                                <FilterMenu />
                            </div>
                        </div>
                        {searchOpen ? (
                            <div className="flex flex-col gap-1 px-3 pb-2">
                                <Input
                                    autoFocus
                                    value={searchQuery}
                                    onChange={(event) => setSearchQuery(event.target.value)}
                                    placeholder="Search chat titles"
                                    className="h-7 text-xs"
                                />
                                {searchQuery.trim() ? (
                                    <div className="flex flex-wrap items-center gap-1">
                                        {listChatSearchActions().map((action) => {
                                            const SearchAction = action.Component
                                            return (
                                                <SearchAction
                                                    key={action.id}
                                                    query={searchQuery.trim()}
                                                    navigateTo={navigateTo}
                                                />
                                            )
                                        })}
                                    </div>
                                ) : null}
                            </div>
                        ) : null}
                        {timeOpen ? (
                            <div className="mx-3 mb-2 rounded-md border border-border/70 bg-card p-2">
                                <div className="grid grid-cols-2 gap-2">
                                    <label className="flex flex-col gap-1">
                                        <span className="text-[9px] uppercase tracking-wide text-muted-foreground">From</span>
                                        <Input
                                            type="datetime-local"
                                            value={timeFromDraft}
                                            onChange={(event) => setTimeFromDraft(event.target.value)}
                                            className="h-7 text-xs"
                                        />
                                    </label>
                                    <label className="flex flex-col gap-1">
                                        <span className="text-[9px] uppercase tracking-wide text-muted-foreground">To</span>
                                        <Input
                                            type="datetime-local"
                                            value={timeToDraft}
                                            onChange={(event) => setTimeToDraft(event.target.value)}
                                            className="h-7 text-xs"
                                        />
                                    </label>
                                </div>
                                {timeError ? (
                                    <Text type={TextTypes.Body7} className="mt-1 block text-[10px] text-red-500">
                                        {timeError}
                                    </Text>
                                ) : null}
                                <div className="mt-2 flex justify-end gap-1">
                                    <Button
                                        variant="ghost"
                                        size="sm"
                                        className="h-6 px-2 text-[10px]"
                                        onClick={handleClearTimeRange}
                                    >
                                        Clear
                                    </Button>
                                    <Button
                                        size="sm"
                                        className="h-6 px-2 text-[10px]"
                                        onClick={handleApplyTimeRange}
                                    >
                                        Apply
                                    </Button>
                                </div>
                            </div>
                        ) : null}
                        {extraFilterCount > 0 || hasExcludeTagFilter ? (
                            <div className="flex flex-wrap items-center gap-1 px-3 pb-2">
                                {hasSearchFilter ? (
                                    <FilterChip
                                        label={`Search: ${searchQuery.trim()}`}
                                        onRemove={() => setSearchQuery("")}
                                    />
                                ) : null}
                                {hasTimeFilter ? (
                                    <FilterChip
                                        label={
                                            timeFrom && timeTo
                                                ? `${formatTimeChip(timeFrom)} → ${formatTimeChip(timeTo)}`
                                                : timeFrom
                                                    ? `Since ${formatTimeChip(timeFrom)}`
                                                    : `Until ${formatTimeChip(timeTo)}`
                                        }
                                        onRemove={handleClearTimeRange}
                                    />
                                ) : null}
                                {tagFilters.map((tag) => (
                                    <FilterChip
                                        key={tag}
                                        label={tag}
                                        onRemove={() => handleTagToggle(tag, false)}
                                    />
                                ))}
                                {excludeTagFilters.map((tag) => (
                                    <FilterChip
                                        key={`exclude_${tag}`}
                                        tone="exclude"
                                        label={`Not ${tag}`}
                                        onRemove={() => handleTagCycle(tag)}
                                    />
                                ))}
                                {!isDefaultChatTypes(chatTypeFilters) || extraFilterCount > 0 || hasNonDefaultExcludeTagFilter ? (
                                    <button
                                        type="button"
                                        className="text-[10px] text-muted-foreground underline-offset-2 hover:underline"
                                        onClick={handleResetFilters}
                                    >
                                        Clear all
                                    </button>
                                ) : null}
                            </div>
                        ) : null}
                        {!isDefaultChatTypes(chatTypeFilters) ? (
                            <div className="px-4 pb-1">
                                <Text type={TextTypes.Body7} color="muted" className="text-[10px] uppercase tracking-wide">
                                    Showing: {activeFilterLabel}
                                </Text>
                            </div>
                        ) : null}
                        {!isLoading ? renderChatItems() : (
                            <div className="flex h-40 items-center justify-center">
                                <LoadingSpinner />
                            </div>
                        )}
                    </>
                ) : null}
            </div>
            <div className="chat-list-footer">
                <ProfileCard navigateTo={navigateTo} themeSelector={themeSelector} avatarSrc={avatarSrc} />
            </div>
        </div>
    );
}
