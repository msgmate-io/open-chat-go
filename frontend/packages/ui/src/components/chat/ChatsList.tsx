import { ChatItemCompact } from "./ChatItem"
import { ProfileCard } from "./ProfileCard"
import { LoadingSpinner } from "./loading-spinner"
import { isToday, isYesterday, isWithinLast7Days } from "../../lib/date"
import useSWR from 'swr'
import { DefaultChats } from "./DefaultChats"
import { NewChatCard } from "./NewChatCard"
import type React from "react"
import { useState, useCallback, useEffect, useMemo, useRef } from "react"
import {
    DropdownMenu,
    DropdownMenuCheckboxItem,
    DropdownMenuContent,
    DropdownMenuLabel,
    DropdownMenuSeparator,
    DropdownMenuTrigger
} from "../dropdown-menu"
import { Button } from "../button"
import { Text, TextTypes } from "../text"
import { SlidersHorizontal } from "lucide-react"
import { useCurrentUser } from "../../integration/hooks/use-current-user"

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
    }, [])

    useEffect(() => {
        const onPopState = () => {
            setChatTypeFilters(getChatTypesFromUrl())
        }

        window.addEventListener("popstate", onPopState)
        return () => window.removeEventListener("popstate", onPopState)
    }, [])

    useEffect(() => {
        syncChatTypesToUrl(chatTypeFilters)
    }, [chatTypeFilters, syncChatTypesToUrl])

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
        if (!to.startsWith("/chat") || isDefaultChatTypes(chatTypeFilters)) {
            navigateTo(to)
            return
        }

        const separator = to.includes("?") ? "&" : "?"
        navigateTo(`${to}${separator}${CHAT_TYPE_QUERY_PARAM}=${encodeURIComponent(serializeChatTypes(chatTypeFilters))}`)
    }, [chatTypeFilters, navigateTo])

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
    
    const chatsListUrl = useCallback(() => {
        if (!showChats) {
            return null
        }
        const url = '/api/v1/chats/list'
        const base = `${url}?chat_types=${serializeChatTypes(chatTypeFilters)}`
        return seeAll && isAdmin ? `${base}&scope=all` : base
    }, [chatTypeFilters, showChats, seeAll, isAdmin]);

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

    const { data: actionTasksCount } = useSWR<{ count: number }>(
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
            }
        }
        document.addEventListener("visibilitychange", onVisibilityChange)
        return () => document.removeEventListener("visibilitychange", onVisibilityChange)
    }, [mutateChatStates, mutateChats])

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
            const isDefaultFilter = isDefaultChatTypes(chatTypeFilters)

            return (
                <div className="flex flex-col items-center px-4 py-8 text-center">
                    <div className="mb-3 text-muted-foreground">
                        <ExploreChatsIcon />
                    </div>
                    <Text type={TextTypes.Body5} tag="p" bold>
                        {isDefaultFilter ? "No chats yet" : `No ${activeFilterLabel.toLowerCase()} yet`}
                    </Text>
                    <Text type={TextTypes.Body7} color="muted" className="mt-1 mb-4">
                        {isDefaultFilter
                            ? "Start a new chat or try one of the default options above."
                            : "Try another filter or start a new chat in this category."}
                    </Text>
                    {!isDefaultFilter ? (
                        <Button
                            variant="ghost"
                            size="sm"
                            className="mb-2"
                            onClick={handleResetFilters}
                        >
                            Back to conversations
                        </Button>
                    ) : null}
                    <Button onClick={() => navigateWithFilter('/chat/new')} size="sm">
                        New chat
                    </Button>
                </div>
            );
        }

        let lastDivider: string | null = null

        return chats.rows.flatMap((chat: { uuid: string; latest_message?: { text?: string } }) => {
            const chatDate = new Date();
            let divider = null;

            if (isToday(chatDate)) {
                if (lastDivider !== 'Today') {
                    divider = renderDivider('Today');
                    lastDivider = 'Today';
                }
            } else if (isYesterday(chatDate)) {
                if (lastDivider !== 'Yesterday') {
                    divider = renderDivider('Yesterday');
                    lastDivider = 'Yesterday';
                }
            } else if (isWithinLast7Days(chatDate)) {
                if (lastDivider !== 'Previous 7 Days') {
                    divider = renderDivider('Previous 7 Days');
                    lastDivider = 'Previous 7 Days';
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
                />,
            ].filter(Boolean);
        });
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
                                <FilterMenu />
                            </div>
                        </div>
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
