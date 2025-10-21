import React, { useEffect, useMemo, useRef, useState } from 'react';
import { BotSelector, Button, ComposerOptionsMenu, DropdownMenuItem, MessageInput, LoadingSpinner, Text, TextTypes } from "@open-chat-go/ui";
import { NewBotChatCard } from './NewBotChat';
import { CollapseIndicator } from "@open-chat-go/ui";
import { useSidePanelCollapse } from "@open-chat-go/ui";
import { useBreakpoint } from "@/components/utils";
import useSWR from "swr";
import { fetcher } from "@/lib/utils";
import { Mic } from "lucide-react";
import { clearPendingToolInit, getPendingToolInit } from "@/lib/chat-tool-init-store";
import {
    asToolInitMap,
    getMissingRequiredToolInitFields,
    pickToolInitForTools,
    resolveRequiredToolInitDescriptors,
} from "@/lib/tool-init";
import { buildOpenChatRunCommand, sanitizeRunChatConfig } from "@open-chat-go/ui";
import { resolveChatUIExtension, type ChatUIContext } from "@open-chat-go/ui";
import { MessageInputOptionsMenuItems } from "@open-chat-go/ui";

type BotModel = {
    title: string;
    description?: string;
    configuration?: Record<string, unknown>;
}

type ContactResponse = {
    name?: string;
    user_uuid?: string;
    is_automated?: boolean;
    profile_data?: {
        models?: BotModel[];
    };
}

type ToolsResponse = {
    rows: Array<{
        name: string;
        requires_init?: boolean;
        init_schema?: Record<string, unknown>;
    }>;
}

type IntegrationsResponse = {
    rows: Array<{
        name: string;
    }>;
}

function asStringArray(value: unknown): string[] {
    if (!Array.isArray(value)) {
        return []
    }
    return value.map((entry) => String(entry || "").trim()).filter(Boolean)
}

function sanitizeBotModelSelectionConfig(raw: Record<string, unknown>): Record<string, unknown> {
    const next: Record<string, unknown> = { ...raw }
    for (const key of ["tools", "tool_init", "integrations", "mcp_tools", "system_prompt"]) {
        delete next[key]
    }
    return next
}

export function StartChat({
    contactToken,
    navigateTo
}: {
    contactToken: string,
    navigateTo: (to: string) => void
}) {
    const { data: contact, isLoading } = useSWR<ContactResponse>(`/api/v1/contacts/${contactToken}`, fetcher)
    const { data: toolsData } = useSWR<ToolsResponse>("/api/v1/tools?page=1&page_size=400", fetcher)
    const [text, setText] = useState("")
    const [selectedModel, setSelectedModel] = useState("")
    const [isStarting, setIsStarting] = useState(false)
    const [startError, setStartError] = useState<string | null>(null)
    const textareaRef = useRef<HTMLTextAreaElement>(null)
    const leftPannelCollapsed = useSidePanelCollapse(state => state.isCollapsed);
    const onToggleCollapse = useSidePanelCollapse(state => state.toggle);
    const { isSm } = useBreakpoint("sm");
    const onSidebarButtonClick = isSm ? onToggleCollapse : () => navigateTo("/chat");

    const botModels = contact?.profile_data?.models ?? []
    const isBotContact = contact?.is_automated === true
    const selectedModelConfig = useMemo(
        () => (botModels.find((model) => model.title === selectedModel)?.configuration ?? {}) as Record<string, unknown>,
        [botModels, selectedModel],
    )
    const selectedTools = useMemo(() => asStringArray(selectedModelConfig.tools), [selectedModelConfig])
    const requiredToolInitDescriptors = useMemo(
        () => resolveRequiredToolInitDescriptors(selectedTools, toolsData?.rows ?? []),
        [selectedTools, toolsData?.rows],
    )
    // chat_backend selects the chat UI extension; legacy profiles stored the
    // chat backend name in backend, so fall back to it.
    const uiExtension = resolveChatUIExtension(
        (selectedModelConfig.chat_backend ?? selectedModelConfig.backend) as string | undefined,
    )
    const hasPreStart = Boolean(uiExtension?.MessageInput?.PreStart)
    const { data: integrations } = useSWR<IntegrationsResponse>(
        isBotContact ? "/api/v1/integrations/list" : null,
        fetcher,
    )
    const isVoiceIntegrationEnabled = Boolean(
        integrations?.rows?.some((integration) => integration.name === "voice")
    )

    useEffect(() => {
        if (!selectedModel && botModels.length > 0) {
            setSelectedModel(botModels[0]?.title ?? "")
        }
    }, [botModels, selectedModel])

    useEffect(() => {
        if (!isBotContact || !selectedModel) {
            return
        }
        if (requiredToolInitDescriptors.length === 0) {
            return
        }
        // The pre-start input collects the required tool init fields inline,
        // so no redirect to the dedicated init page is needed.
        if (hasPreStart) {
            return
        }

        const pending = asToolInitMap(getPendingToolInit(contactToken, selectedModel))
        const missing = getMissingRequiredToolInitFields(requiredToolInitDescriptors, pending)
        if (missing.length === 0) {
            return
        }

        navigateTo(`/chat/new/${contactToken}/init`)
    }, [contactToken, isBotContact, navigateTo, requiredToolInitDescriptors, selectedModel, hasPreStart])

    const startChat = async (
        nextRoute: "chat" | "voice" = "chat",
        toolInitOverride?: Record<string, unknown>,
        sharedConfigOverride?: Record<string, unknown>,
    ) => {
        if (isStarting) return

        setIsStarting(true)
        setStartError(null)
        try {
            if (isBotContact && requiredToolInitDescriptors.length > 0 && !hasPreStart) {
                const pending = asToolInitMap(getPendingToolInit(contactToken, selectedModel))
                const missing = getMissingRequiredToolInitFields(requiredToolInitDescriptors, pending)
                if (missing.length > 0) {
                    setStartError(`Missing required tool init fields: ${missing.join(", ")}`)
                    navigateTo(`/chat/new/${contactToken}/init`)
                    return
                }
            }

            const sharedConfig: Record<string, unknown> = {
                ...(isBotContact
                    ? sanitizeBotModelSelectionConfig(selectedModelConfig)
                    : selectedModelConfig),
                ...sharedConfigOverride,
            }

            if (isBotContact && requiredToolInitDescriptors.length > 0 && !hasPreStart) {
                const pending = asToolInitMap(getPendingToolInit(contactToken, selectedModel))
                const toolInitPayload = pickToolInitForTools(requiredToolInitDescriptors, pending)
                if (Object.keys(toolInitPayload).length > 0) {
                    sharedConfig.tool_init = toolInitPayload
                }
            }

            if (toolInitOverride && Object.keys(toolInitOverride).length > 0) {
                sharedConfig.tool_init = {
                    ...(sharedConfig.tool_init as Record<string, unknown> | undefined),
                    ...toolInitOverride,
                }
            }

            const response = await fetch("/api/v1/chats/create", {
                method: "POST",
                credentials: "include",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({
                    contact_token: contactToken,
                    first_message: text.trim(),
                    chat_type: "conversation",
                    shared_config: sharedConfig,
                }),
            })

            if (!response.ok) {
                const message = await response.text()
                throw new Error(message || `Failed to create chat (${response.status})`)
            }

            const chat = await response.json()
            if (!chat?.uuid) {
                throw new Error("Chat created but missing uuid")
            }

            setText("")
            if (isBotContact && selectedModel) {
                clearPendingToolInit(contactToken, selectedModel)
            }
            if (nextRoute === "voice") {
                navigateTo(`/integrations/voice/chat/${encodeURIComponent(chat.uuid)}`)
                return
            }
            navigateTo(`/chat/${chat.uuid}`)
        } catch (err) {
            setStartError(err instanceof Error ? err.message : "Failed to start chat")
        } finally {
            setIsStarting(false)
        }
    }

    const buildStartRunCommand = () => {
        const message = text.trim()
        if (!message || !isBotContact) {
            return null
        }
        let toolInit: Record<string, unknown> | undefined
        if (requiredToolInitDescriptors.length > 0) {
            const pending = asToolInitMap(getPendingToolInit(contactToken, selectedModel))
            const payload = pickToolInitForTools(requiredToolInitDescriptors, pending)
            if (Object.keys(payload).length > 0) {
                toolInit = payload
            }
        }
        return buildOpenChatRunCommand({
            botIdentifier: contact?.user_uuid ?? contactToken,
            message,
            chatConfig: sanitizeRunChatConfig(selectedModelConfig),
            toolInit,
        })
    }

    const chatUIContext: ChatUIContext = {
        chatUUID: null,
        chat: null,
        user: null,
        messages: { rows: [] },
        status: null,
        isBotResponding: false,
        stopBotResponse: () => {},
        contact: {
            contact_token: contactToken,
            name: contact?.name,
            is_automated: contact?.is_automated,
            user_uuid: contact?.user_uuid,
            profile_data: contact?.profile_data as Record<string, unknown> | undefined,
        },
        selectedModel,
        selectedModelConfig,
        onSendMessage: (payload) => {
            void startChat("chat", payload.tool_init, payload.shared_config)
        },
        isLoading: isStarting,
        navigateTo,
    }

    const preStartScrollInset =
        "max(var(--openchat-keyboard-bottom, 0px), var(--openchat-keyboard-bottom-visual, 0px), var(--openchat-safe-bottom, 0px))";

    const handlePreStartFocusCapture = (event: React.FocusEvent<HTMLDivElement>) => {
        const target = event.target;
        if (!(target instanceof HTMLElement)) {
            return;
        }
        const isMobileRuntime =
            typeof document !== "undefined" &&
            document.documentElement.getAttribute("data-openchat-runtime") === "mobile";
        if (!isMobileRuntime) {
            return;
        }
        const tag = target.tagName.toLowerCase();
        const isInputLike = tag === "input" || tag === "textarea" || tag === "select" || target.isContentEditable;
        if (!isInputLike) {
            return;
        }
        window.setTimeout(() => {
            target.scrollIntoView({ block: "center", inline: "nearest" });
        }, 120);
    };

    if (isLoading) {
        return <div className="flex h-full items-center justify-center">
            <LoadingSpinner size={48} />
        </div>
    }

    return (
        <div className="relative flex h-full w-full flex-col">
            <div className="absolute left-0 top-0 z-40 w-full px-2 pt-2 md:px-3 md:pt-3">
                <div className="flex w-full max-w-[44rem] items-center gap-2 rounded-xl border border-border/60 bg-card/90 px-2 py-1 shadow-sm backdrop-blur-sm">
                    {leftPannelCollapsed ? (
                        <CollapseIndicator leftPannelCollapsed={leftPannelCollapsed} onToggleCollapse={onSidebarButtonClick} />
                    ) : null}
                    {isBotContact ? (
                        <div className="min-w-0 flex-1">
                            <BotSelector
                                contact={contact}
                                selectedModel={selectedModel || botModels[0]?.title || ""}
                                setSelectedModel={setSelectedModel}
                                defaultCollapsed
                            />
                        </div>
                    ) : null}
                </div>
            </div>

            <div
                className={`mx-auto flex h-full w-full max-w-4xl flex-1 min-h-0 flex-col px-4 pb-2 ${hasPreStart ? "overflow-y-auto" : ""}`}
                style={hasPreStart ? {
                    paddingBottom: `calc(${preStartScrollInset} + 0.5rem)`,
                    scrollPaddingBottom: `calc(${preStartScrollInset} + 0.5rem)`,
                } : undefined}
                onFocusCapture={hasPreStart ? handlePreStartFocusCapture : undefined}
            >
                <div className={hasPreStart
                    ? "flex shrink-0 flex-col items-center gap-4 pt-16 pb-4"
                    : "flex flex-1 min-h-0 flex-col items-center justify-center gap-4 overflow-auto py-4"}>
                    <Text type={TextTypes.Heading4} tag="h1" bold className="px-2 text-center text-xl leading-tight md:text-3xl">
                        {isBotContact ? `Start an interaction with ${contact?.name}` : `Start a chat with ${contact?.name}`}
                    </Text>

                    {isBotContact ? (
                        <div className="w-full rounded-lg border border-border bg-card p-3">
                            <NewBotChatCard startChat={(message) => {
                                setText(message)
                                requestAnimationFrame(() => textareaRef.current?.focus())
                            }} />
                        </div>
                    ) : null}
                </div>

                <div className={hasPreStart ? "mt-2 shrink-0 space-y-2" : "mt-auto space-y-2"}>
                    {uiExtension?.MessageInput?.PreStart ? (
                        <uiExtension.MessageInput.PreStart
                            ctx={chatUIContext}
                            text={text}
                            setText={setText}
                        />
                    ) : (
                        <MessageInput
                            ref={textareaRef}
                            text={text}
                            setText={setText}
                            onSendMessage={() => startChat("chat")}
                            isLoading={isStarting}
                            placeholder={isBotContact ? "Ask something to start the interaction..." : "Write your first message..."}
                            footerOptions={
                                isBotContact ? (
                                    <ComposerOptionsMenu>
                                        {isBotContact && isVoiceIntegrationEnabled ? (
                                            <DropdownMenuItem
                                                onClick={() => startChat("voice")}
                                                disabled={isStarting}
                                                className="gap-2"
                                            >
                                                <Mic className="size-4" />
                                                Voice mode
                                            </DropdownMenuItem>
                                        ) : null}
                                        <MessageInputOptionsMenuItems getCommand={buildStartRunCommand} />
                                    </ComposerOptionsMenu>
                                ) : undefined
                            }
                        />
                    )}
                    {startError ? <div className="px-1 text-sm text-destructive">{startError}</div> : null}
                </div>
            </div>
        </div>
    );
}
