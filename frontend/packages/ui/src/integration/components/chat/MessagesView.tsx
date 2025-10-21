import useSWR, { mutate } from "swr"
import { MessageItem } from "./MessageItem"
import React, { useEffect, useState, useRef, forwardRef, useCallback, type ReactNode } from 'react';
import { CollapseIndicator } from "../CollapseIndicator";
import { usePartialMessageStore } from "./PartialMessages";
import { useSidePanelCollapse } from "./ChatBase";
import { BotDisplay, Button, DropdownMenuItem, LoadingSpinner, MessageInputWithFiles, Text, TextTypes } from "@open-chat-go/ui";
import { InteractionChatInfo } from "./InteractionChatInfo";
import { resolveChatUIExtension, type ChatUIContext, type ChatUISendPayload } from "./chat-ui/index";
import { buildChatRunCommand } from "../../lib/open-chat-run";
import { MessageInputOptionsMenuItems } from "./MessageInputOptionsButton";
import { APIRequestError, fetcher } from "../../lib/utils";
import { useBreakpoint } from "../utils";
import { navigate } from "vike/client/router";
import { Mic } from "lucide-react";

const ScrollButton = React.memo(({ scrollRef }: { scrollRef: React.RefObject<HTMLDivElement> }) => {
    const [shouldShow, setShouldShow] = useState(false);
    const scrollTimeoutRef = useRef<NodeJS.Timeout | null>(null);

    const scrollToBottom = () => {
        if (scrollRef.current) {
            const scrollElement = scrollRef.current;
            const maxScroll = scrollElement.scrollHeight - scrollElement.clientHeight;
            scrollElement.scrollTo({
                top: maxScroll,
                behavior: 'smooth'
            });
        }
    };

    const handleScroll = useCallback(() => {
        if (!scrollRef.current) return;
        
        if (scrollTimeoutRef.current) {
            clearTimeout(scrollTimeoutRef.current);
        }
        
        scrollTimeoutRef.current = setTimeout(() => {
            const { scrollTop, scrollHeight, clientHeight } = scrollRef.current!;
            const distance = Math.abs(scrollHeight - clientHeight - scrollTop);
            setShouldShow(distance >= 150);
        }, 100);
    }, []);

    useEffect(() => {
        const scrollElement = scrollRef.current;
        if (scrollElement) {
            scrollElement.addEventListener('scroll', handleScroll);
            return () => scrollElement.removeEventListener('scroll', handleScroll);
        }
    }, [handleScroll]);

    useEffect(() => {
        return () => {
            if (scrollTimeoutRef.current) {
                clearTimeout(scrollTimeoutRef.current);
            }
        };
    }, []);

    return (
        <button 
            onClick={scrollToBottom}
            className={`fixed bottom-36 right-8 bg-primary text-primary-foreground hover:bg-primary/90 rounded-full p-3 shadow-lg transition-all duration-300 transform ${
                shouldShow ? 'opacity-100 pointer-events-auto translate-y-0' : 'opacity-0 pointer-events-none translate-y-4'
            }`}
        >
            <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 14l-7 7m0 0l-7-7m7 7V3" />
            </svg>
        </button>
    );
});

export function MessagesScroll({ 
    chatUUID,
    user,
    hideInput = false,
    footerSlot = null,
    messages,
    chat,
    mutateMessages,
}: {
    messages: any,
    chat: any,
    chatUUID: string | null,
    user: any,
    hideInput: boolean;
    footerSlot?: ReactNode;
    mutateMessages: any;
}) {
    const [text, setText] = useState("");
    const { partialMessages, removePartialMessage, addPartialMessage } = usePartialMessageStore()
    const [isSendingMessage, setIsSendingMessage] = useState(false)

    const scrollRef = useRef<HTMLDivElement>(null) as React.RefObject<HTMLDivElement>;
    const inputRef = useRef<HTMLTextAreaElement>(null);
    
    const shouldLoadIntegrations = Boolean(chatUUID && chat?.partner?.is_automated);

    // Hydrate a mid-stream snapshot after a page reload: while a bot reply is
    // still streaming, the DB keeps a throttled snapshot so progress is not
    // lost when the client reconnects.
    useEffect(() => {
        if (!chatUUID) return;
        let cancelled = false;
        (async () => {
            try {
                const res = await fetch(`/api/v1/chats/${chatUUID}/messages/streaming`, {
                    credentials: "include",
                });
                if (res.status === 204 || !res.ok) return;
                const snapshot = await res.json();
                if (cancelled || !snapshot?.session_id) return;
                const existing = usePartialMessageStore.getState().partialMessages[chatUUID];
                if (existing) return;
                addPartialMessage(chatUUID, {
                    text: snapshot.text || "",
                    thoughts: snapshot.reasoning || [],
                    meta_data: { partial_phase: "responding" },
                    tool_calls: snapshot.tool_calls || [],
                    seq: snapshot.seq || 0,
                    session_id: snapshot.session_id,
                }, snapshot.session_id);
            } catch {
                // Hydration is best-effort only.
            }
        })();
        return () => {
            cancelled = true;
        };
    }, [chatUUID, addPartialMessage]);

    const { data: integrations } = useSWR<{ rows?: Array<{ name: string }> }>(
        shouldLoadIntegrations ? "/api/v1/integrations/list" : null,
        fetcher,
    );
    const isVoiceIntegrationEnabled = Boolean(
        integrations?.rows?.some((integration) => integration.name === "voice")
    );
    const { isSm } = useBreakpoint("sm");
    const leftPannelCollapsed = useSidePanelCollapse(state => state.isCollapsed);
    const onToggleCollapse = useSidePanelCollapse(state => state.toggle);
    const onSidebarButtonClick = isSm ? onToggleCollapse : () => navigate("/chat");

    useEffect(() => {
        if (scrollRef.current) {
            const scrollElement = scrollRef.current;
            const maxScroll = scrollElement.scrollHeight - scrollElement.clientHeight;
            scrollElement.scrollTop = maxScroll;
        }
    }, [messages, partialMessages]);

    const onSendMessage = async (
        payload?: ChatUISendPayload | Array<{ file_id: string; display_name?: string }>,
    ) => {
        const normalized = Array.isArray(payload)
            ? { text, attachments: payload }
            : {
                  text: payload?.text ?? text,
                  tool_init: payload?.tool_init,
                  attachments: payload?.attachments,
              };
        const messageText = (normalized.text || "").trim();
        const attachments = normalized.attachments;
        if (!messageText && (!attachments || attachments.length === 0)) return;

        setIsSendingMessage(true);
        try {
            const messageData: any = {
                text: messageText
            };

            if (attachments && attachments.length > 0) {
                messageData.attachments = attachments;
            }

            if (normalized.tool_init && Object.keys(normalized.tool_init).length > 0) {
                messageData.tool_init = normalized.tool_init;
            }

            const response = await fetch(`/api/v1/chats/${chatUUID}/messages/send`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify(messageData)
            });

            if (!response.ok) {
                const errorText = await response.text();
                console.error('Failed to send message:', errorText);
                return;
            }

            const newMessage = await response.json();
            
            // Ensure the new message has the correct structure for the mutation
            const messageWithAttachments = {
                ...newMessage,
                meta_data: newMessage.meta_data || {}
            };
            
            mutateMessages({
                ...messages,
                rows: [messageWithAttachments, ...(messages?.rows ?? [])]
            }, false);
            
            // Clear input after successful send
            setText('');
        } catch (error) {
            console.error('Error sending message:', error);
        } finally {
            setIsSendingMessage(false);
        }
    };

    const onStopBotResponse = () => {
        fetch(`/api/v1/chats/${chatUUID}/signals/interrupt`, {
            method: "POST",
        })
    }
    
    const hasPartialMessage = chatUUID ? (
        partialMessages?.[chatUUID]?.text?.length > 0 || 
        partialMessages?.[chatUUID]?.thoughts?.length > 0 ||
        (partialMessages?.[chatUUID]?.tool_calls?.length ?? 0) > 0
    ) : false;

    const isBotResponding = hasPartialMessage || Boolean((chat as any)?.status?.is_active);

    const chatUIContext: ChatUIContext = {
        chatUUID,
        chat,
        user,
        messages,
        status: (chat as any)?.status ?? null,
        isBotResponding,
        stopBotResponse: onStopBotResponse,
        contact: null,
        selectedModel: "",
        selectedModelConfig: {},
        onSendMessage: (payload) => {
            void onSendMessage(payload);
        },
        isLoading: isSendingMessage,
        navigateTo: (to) => navigate(to),
    };
    // chat_backend selects the chat UI extension; legacy chats stored the chat
    // backend name in backend, so fall back to it.
    const uiExtension = resolveChatUIExtension(chat?.config?.chat_backend ?? chat?.config?.backend);

    const renderMessages = () => {
        if (!messages || !chat || !user) {
            return (
                <div className="flex flex-grow w-full h-full items-center content-center justify-center">
                    <LoadingSpinner />
                </div>
            );
        }

        if ((messages.rows?.length || 0) === 0) {
            return (
                <div className="mx-auto my-6 w-full max-w-2xl">
                    <div className="rounded-2xl border border-border/70 bg-card/80 px-5 py-6 text-center shadow-sm">
                        <Text type={TextTypes.Heading6} tag="h2" bold>
                            There are no messages in this chat yet
                        </Text>
                        <Text type={TextTypes.Body6} color="muted" className="mt-2">
                            Send the first message to begin chatting.
                        </Text>
                    </div>
                </div>
            );
        }

        // Create a reversed copy of the messages array
        const reversedMessages = [...messages.rows].reverse();
        
        let lastUserMessageUUID: string | undefined;

        return reversedMessages.map((message: any) => {
            const isAutomatedSender = Boolean(message?.sender_is_automated);
            const isEventMessage = String(message?.data_type || "") === "event";

            let rerunSourceMessageUUID: string | undefined;
            if (isAutomatedSender) {
                rerunSourceMessageUUID = lastUserMessageUUID;
            } else if (!isEventMessage) {
                lastUserMessageUUID = message.uuid;
            }

            return (
                <MessageItem
                    key={`msg_${message.uuid}`}
                    message={{
                        uuid: message.uuid,
                        text: message.text || '',
                        send_at: message.send_at || '',
                        thoughts: message.reasoning || [],
                        meta_data: message?.meta_data,
                        tool_calls: message?.tool_calls || []
                    }}
                    chat={chat}
                    chatUUID={chatUUID || undefined}
                    rerunSourceMessageUUID={rerunSourceMessageUUID}
                    selfIsSender={user?.uuid === message.sender_uuid}
                    isBotChat={true}
                    interactionState={(chat as any)?.status?.state}
                />
            );
        });
    };
    return (
        <div className="chat-messages-column">
            {uiExtension?.ChatDetails ? (
                <uiExtension.ChatDetails
                    ctx={chatUIContext}
                    showMobileHeaderControls={Boolean(isSm && chat?.chat_type === "interaction")}
                    leftPannelCollapsed={leftPannelCollapsed}
                    onToggleSidebar={onSidebarButtonClick}
                />
            ) : (
                <InteractionChatInfo
                    chat={chat}
                    user={user}
                    showMobileHeaderControls={Boolean(isSm && chat?.chat_type === "interaction")}
                    leftPannelCollapsed={leftPannelCollapsed}
                    onToggleSidebar={onSidebarButtonClick}
                />
            )}
            <div
                ref={scrollRef}
                className="scrollbar-hidden flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-2 pb-3 pt-12 md:px-4 md:pb-4"
                style={{
                    paddingBottom: "calc(0.75rem + var(--openchat-safe-bottom, 0px))",
                }}
            >
                {renderMessages()}
                {chatUUID && chat && user && (chat as any)?.status?.is_active && (chat as any)?.status?.source === "queue" && !hasPartialMessage && (
                    <MessageItem
                        key={`queued_${chatUUID}`}
                        message={{
                            uuid: `queued_${chatUUID}`,
                            text: "",
                            thoughts: [],
                            meta_data: { queue_state: "queued", finished: false },
                            tool_calls: [],
                            is_generating: true,
                        }}
                        chat={chat}
                        chatUUID={chatUUID || undefined}
                        selfIsSender={false}
                        isBotChat={true}
                        interactionState={(chat as any)?.status?.state}
                    />
                )}
                {chatUUID && chat && user && partialMessages?.[chatUUID] && (
                    <MessageItem 
                        key={`msg_${chatUUID}`} 
                        message={{
                            uuid: `partial_${chatUUID}`,
                            text: partialMessages[chatUUID]?.text || '',
                            thoughts: partialMessages[chatUUID]?.thoughts || [],
                            meta_data: partialMessages[chatUUID]?.meta_data,
                            tool_calls: partialMessages[chatUUID]?.tool_calls,
                            is_generating: partialMessages[chatUUID]?.meta_data?.finished !== true
                        }} 
                        chat={chat} 
                        chatUUID={chatUUID || undefined}
                        selfIsSender={
                            partialMessages[chatUUID]?.meta_data?.partial_sender === "user"
                                ? true
                                : Boolean((chat as any)?.sender_uuid && user?.uuid === (chat as any).sender_uuid)
                        } 
                        isBotChat={true} 
                        interactionState={(chat as any)?.status?.state}
                    />
                )}
            </div>
            <ScrollButton scrollRef={scrollRef} />
            {!hideInput &&
                (() => {
                    const input = uiExtension?.MessageInput;
                    const isPreStart = input?.isPreStart(chatUIContext) ?? false;
                    const PreStartComponent = isPreStart ? input?.PreStart : undefined;
                    const ActiveComponent = !isPreStart ? input?.Active : undefined;
                    if (PreStartComponent) {
                        return (
                            <PreStartComponent ctx={chatUIContext} text={text} setText={setText} />
                        );
                    }
                    if (ActiveComponent) {
                        return (
                            <ActiveComponent ctx={chatUIContext} text={text} setText={setText} />
                        );
                    }
                    return (
                        <MessageInputWithFiles
                            text={text}
                            setText={setText}
                            isLoading={isSendingMessage}
                            isBotResponding={isBotResponding}
                            stopBotResponse={onStopBotResponse}
                            onSendMessage={onSendMessage}
                            ref={inputRef}
                            botConfig={chat?.config}
                            footerMenuItems={
                                <>
                                    {isVoiceIntegrationEnabled ? (
                                        <DropdownMenuItem
                                            onClick={() => {
                                                if (!chatUUID) {
                                                    return;
                                                }
                                                navigate(`/integrations/voice/chat/${encodeURIComponent(chatUUID)}`);
                                            }}
                                            className="gap-2"
                                        >
                                            <Mic className="size-4" />
                                            Voice session
                                        </DropdownMenuItem>
                                    ) : null}
                                    {chat?.partner?.is_automated || chat?.partner?.is_bot ? (
                                        <MessageInputOptionsMenuItems
                                            getCommand={() => buildChatRunCommand(chat, text)}
                                        />
                                    ) : null}
                                </>
                            }
                        />
                    );
                })()}
            {hideInput && footerSlot}
        </div>
    );
}


const MAX_LOAD_RETRIES = 4;

export function MessagesView({ 
        chatUUID = null, 
        hideInput = false,
        footerSlot = null,
    }: {
        chatUUID: string | null,
        hideInput?: boolean,
        footerSlot?: ReactNode,
    }) {

    const { data: chat, error: chatError, mutate: reloadChat } = useSWR(`/api/v1/chats/${chatUUID}`, fetcher)
    const { data: messages, error: messagesError, mutate: mutateMessages } = useSWR(`/api/v1/chats/${chatUUID}/messages/list`, fetcher)
    const { data: user, error: userError, mutate: reloadUser } = useSWR(`/api/v1/user/self`, fetcher)
    const isAutomatedPartner = Boolean(chat?.partner?.is_automated || chat?.partner?.is_bot);
    const shouldPollStatus = Boolean(chatUUID && chat?.chat_type === "interaction" && isAutomatedPartner);
    const { data: interactionStatus, mutate: mutateInteractionStatus } = useSWR(
        shouldPollStatus ? `/api/v1/chats/${chatUUID}/status` : null,
        fetcher,
        {
            refreshInterval: 800,
            revalidateOnFocus: true,
        }
    );
    const chatWithStatus = chat ? { ...chat, status: interactionStatus } : chat;
    const leftPannelCollapsed = useSidePanelCollapse(state => state.isCollapsed);
    const onToggleCollapse = useSidePanelCollapse(state => state.toggle);
    const { isSm } = useBreakpoint("sm");
    const onSidebarButtonClick = isSm ? onToggleCollapse : () => navigate("/chat");

    // Hidden tabs pause SWR polling, so explicitly revalidate the interaction
    // status when the tab becomes visible again.
    useEffect(() => {
        if (typeof document === "undefined") {
            return;
        }
        const onVisibilityChange = () => {
            if (!document.hidden) {
                mutateInteractionStatus();
            }
        };
        document.addEventListener("visibilitychange", onVisibilityChange);
        return () => document.removeEventListener("visibilitychange", onVisibilityChange);
    }, [mutateInteractionStatus]);

    // Without a websocket the final bot message only lands in the DB once the
    // interaction becomes inactive, so refetch on the active -> inactive edge.
    const wasInteractionActiveRef = useRef(false);
    useEffect(() => {
        const isActive = Boolean((interactionStatus as any)?.is_active);
        if (wasInteractionActiveRef.current && !isActive) {
            mutateMessages();
        }
        wasInteractionActiveRef.current = isActive;
    }, [interactionStatus, mutateMessages]);

    const hasLoadError = Boolean(chatError || messagesError || userError);
    const isOfflineCacheMiss = [chatError, messagesError, userError].some((error) => {
        if (!(error instanceof APIRequestError)) {
            return false;
        }
        return error.code === "mobile_device_offline";
    });

    // A transient 4xx/5xx on a just-created chat must not immediately surface as
    // a hard error. Keep the spinner and retry with backoff before giving up.
    const isDataMissing = !chat || !messages || !user;
    const [loadRetryCount, setLoadRetryCount] = useState(0);
    const [loadingRetryExhausted, setLoadingRetryExhausted] = useState(false);

    useEffect(() => {
        if (!hasLoadError || !isDataMissing) {
            if (!hasLoadError) {
                setLoadRetryCount(0);
                setLoadingRetryExhausted(false);
            }
            return;
        }
        if (isOfflineCacheMiss || loadRetryCount >= MAX_LOAD_RETRIES) {
            setLoadingRetryExhausted(true);
            return;
        }
        const delay = Math.min(400 * 2 ** loadRetryCount, 4000);
        const timer = setTimeout(() => {
            setLoadRetryCount((count) => count + 1);
            reloadChat();
            mutateMessages();
            reloadUser();
        }, delay);
        return () => clearTimeout(timer);
    }, [hasLoadError, isDataMissing, isOfflineCacheMiss, loadRetryCount, reloadChat, mutateMessages, reloadUser]);

    const showLoadError = isOfflineCacheMiss || (hasLoadError && loadingRetryExhausted);

    if (showLoadError) {
        return (
            <div className="flex h-full w-full flex-col items-center justify-center gap-3 px-4 text-center">
                <Text type={TextTypes.Body5} bold>
                    {isOfflineCacheMiss ? "This chat is not available offline yet." : "Could not load this chat yet."}
                </Text>
                <Text type={TextTypes.Body6} color="muted">
                    {isOfflineCacheMiss
                        ? "Reconnect once to load this chat, then it can be opened again while offline."
                        : "This can happen briefly right after login while session data is still being picked up."}
                </Text>
                <div className="flex items-center gap-2">
                    <Button
                        size="sm"
                        onClick={() => {
                            reloadChat();
                            mutateMessages();
                            reloadUser();
                        }}
                    >
                        Retry
                    </Button>
                    {isOfflineCacheMiss ? (
                        <Button size="sm" variant="outline" onClick={() => navigate("/chat")}>
                            Back to chats
                        </Button>
                    ) : null}
                </div>
            </div>
        );
    }

    return (
        <div className="flex h-full min-h-0 w-full flex-col items-center px-2 md:px-4">
            {chat?.admin_view ? (
                <div className="mb-1 w-full rounded-md bg-amber-500/90 px-4 py-2 text-center text-sm font-medium text-black">
                    Admin view: rendering the chat between{" "}
                    <strong>{chat.admin_view.user1?.name || chat.admin_view.user1?.username || "user"}</strong> and{" "}
                    <strong>{chat.admin_view.user2?.name || chat.admin_view.user2?.username || "user"}</strong>. You are
                    not a participant in this chat.
                </div>
            ) : null}
            {!(chat?.chat_type === "interaction") ? (
                <div className="absolute left-0 top-0 z-40 ml-2 mt-2 flex items-center gap-1.5 rounded-xl border border-border/60 bg-card/90 px-1.5 py-1 shadow-sm backdrop-blur-sm md:ml-3 md:mt-3 md:gap-2 md:px-2">
                    {leftPannelCollapsed ? (
                        <CollapseIndicator
                            leftPannelCollapsed={leftPannelCollapsed}
                            onToggleCollapse={onSidebarButtonClick}
                        />
                    ) : null}
                    <BotDisplay selectedModel={chat?.config?.model} className="max-w-[11rem] px-2 py-1 text-xs md:max-w-[18rem] md:px-3 md:py-2 md:text-sm" />
                </div>
            ) : null}
                <MessagesScroll
                    chatUUID={chatUUID}
                    user={user}
                    hideInput={hideInput}
                    footerSlot={footerSlot}
                    messages={messages}
                    chat={chatWithStatus}
                    mutateMessages={mutateMessages}
                />
        </div>
    );
}
