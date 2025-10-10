import { useEffect, useRef, useState } from "react";
import useSWR from "swr";
import { mutate } from "swr";
import { navigate } from "vike/client/router";
import { WebsocketHandler } from "../WebsocketHandler";
import { ChatBase } from "./ChatBase";
import { MessagesView } from "./MessagesView";
import { usePartialMessageStore } from "./PartialMessages";
import { fetcher } from "../../lib/utils";
import { Badge, Button, Card, CardContent, CardDescription, CardHeader, CardTitle } from "@open-chat-go/ui";

type VoiceSessionResponse = {
  session_uuid: string;
  state: string;
};

type ChatSummary = {
  partner?: {
    is_automated?: boolean;
    is_bot?: boolean;
    name?: string;
  };
};

function VoiceLevelIndicator({ level }: { level: number }) {
  const clamped = Math.max(0, Math.min(level, 1));
  const bars = Array.from({ length: 5 }, (_, idx) => {
    const phase = idx / 5;
    const intensity = Math.max(0, clamped- phase * 0.08);
    const height = 8 + Math.round(intensity * 34) + idx * 2;
    return { height, active: intensity > 0.06 };
  });

  return (
    <div className="flex h-14 items-end justify-center gap-1.5 rounded-xl border border-border/70 bg-card px-3 py-2">
      {bars.map((bar, idx) => (
        <span
          key={idx}
          className={`w-2 rounded-full transition-all duration-120 ${bar.active ? "bg-primary" : "bg-muted"}`}
          style={{ height: `${bar.height}px` }}
        />
      ))}
    </div>
  );
}

function VoiceComposer({ chatUUID }: { chatUUID: string }) {
  const [sessionUUID, setSessionUUID] = useState("");
  const [sessionState, setSessionState] = useState("idle");
  const [statusMessage, setStatusMessage] = useState("");
  const [isBusy, setIsBusy] = useState(false);
  const [isListening, setIsListening] = useState(false);
  const [level, setLevel] = useState(0);
  const [partialTranscript, setPartialTranscript] = useState("");
  const [committedTranscript, setCommittedTranscript] = useState("");
  const addPartialMessage = usePartialMessageStore((state) => state.addPartialMessage);
  const removePartialMessage = usePartialMessageStore((state) => state.removePartialMessage);

  const localStreamRef = useRef<MediaStream | null>(null);
  const peerRef = useRef<RTCPeerConnection | null>(null);
  const eventsSocketRef = useRef<WebSocket | null>(null);
  const remoteAudioRef = useRef<HTMLAudioElement | null>(null);
  const sessionUUIDRef = useRef("");
  const commitProbeRef = useRef(0);

  const clearVoiceDraft = () => {
    const scopedSession = sessionUUIDRef.current || undefined;
    removePartialMessage(chatUUID, scopedSession);
  };

  const upsertVoiceDraft = (text: string, scopedSessionUUID: string) => {
    const trimmed = text.trim();
    if (!trimmed) {
      removePartialMessage(chatUUID, scopedSessionUUID);
      return;
    }
    removePartialMessage(chatUUID, scopedSessionUUID);
    addPartialMessage(
      chatUUID,
      {
        text: trimmed,
        thoughts: [],
        meta_data: {
          input_mode: "voice",
          partial_sender: "user",
          voice_session_id: scopedSessionUUID,
          finished: false,
        },
        tool_calls: [],
      },
      scopedSessionUUID,
    );
  };

  const reconcileCommittedDraft = (text: string, scopedSessionUUID: string) => {
    const normalizedText = text.trim();
    if (!normalizedText) {
      removePartialMessage(chatUUID, scopedSessionUUID);
      return;
    }
    const probeID = ++commitProbeRef.current;
    const messagesKey = `/api/v1/chats/${encodeURIComponent(chatUUID)}/messages/list`;
    void (async () => {
      for (let attempt = 0; attempt < 12; attempt++) {
        if (probeID !== commitProbeRef.current) {
          return;
        }
        const page = (await mutate(messagesKey)) as { rows?: Array<Record<string, unknown>> } | undefined;
        const rows = Array.isArray(page?.rows) ? page.rows : [];
        const hasCommittedMessage = rows.some((row) => {
          const rowText = String(row?.text || "").trim();
          if (!rowText || rowText !== normalizedText) {
            return false;
          }
          const meta = (row?.meta_data || row?.metaData || {}) as Record<string, unknown>;
          const voiceSessionID = String(meta?.voice_session_id || "");
          return voiceSessionID === scopedSessionUUID;
        });
        if (hasCommittedMessage) {
          removePartialMessage(chatUUID, scopedSessionUUID);
          return;
        }
        await new Promise((resolve) => window.setTimeout(resolve, 250));
      }
      if (probeID === commitProbeRef.current) {
        removePartialMessage(chatUUID, scopedSessionUUID);
      }
    })();
  };

  const teardownVoiceRuntime = () => {
    if (eventsSocketRef.current) {
      eventsSocketRef.current.close();
      eventsSocketRef.current = null;
    }
    if (peerRef.current) {
      peerRef.current.close();
      peerRef.current = null;
    }
    if (localStreamRef.current) {
      for (const track of localStreamRef.current.getTracks()) {
        track.stop();
      }
      localStreamRef.current = null;
    }
    setLevel(0);
    setIsListening(false);
    setPartialTranscript("");
    clearVoiceDraft();
  };

  useEffect(() => {
    return () => teardownVoiceRuntime();
  }, []);

  const connectLevelEventsSocket = (nextSessionUUID: string) => {
    if (eventsSocketRef.current) {
      eventsSocketRef.current.close();
      eventsSocketRef.current = null;
    }
    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const wsURL = `${protocol}//${window.location.host}/api/v1/chats/${encodeURIComponent(chatUUID)}/voice/sessions/${encodeURIComponent(nextSessionUUID)}/ws`;
    const ws = new WebSocket(wsURL);
    ws.onmessage = (event) => {
      try {
        const payload = JSON.parse(event.data);
        const content = payload?.content || {};
        if (String(content?.session_id || "") !== nextSessionUUID) {
          return;
        }
        if (payload?.type === "voice_input_level") {
          const parsedLevel = Number(content?.level);
          if (Number.isFinite(parsedLevel)) {
            setLevel(Math.max(0, Math.min(1, parsedLevel)));
          }
          return;
        }
          if (payload?.type === "voice_transcript_partial") {
            const text = String(content?.text || "").trim();
            if (import.meta.env.DEV) {
              console.debug("[voice] partial", { text, content });
            }
            setPartialTranscript(text);
            upsertVoiceDraft(text, nextSessionUUID);
            return;
          }
        if (payload?.type === "voice_transcript_committed") {
          const text = String(content?.text || "").trim();
          if (import.meta.env.DEV) {
            console.debug("[voice] committed", { text, content });
          }
            if (text) {
              setCommittedTranscript(text);
              upsertVoiceDraft(text, nextSessionUUID);
              reconcileCommittedDraft(text, nextSessionUUID);
            }
            setPartialTranscript("");
          }
      } catch {
        // Ignore malformed event packets.
      }
    };
    ws.onclose = () => {
      if (eventsSocketRef.current === ws) {
        eventsSocketRef.current = null;
      }
    };
    eventsSocketRef.current = ws;
  };

  const startPeerConnection = async (nextSessionUUID: string) => {
    const stream = await navigator.mediaDevices.getUserMedia({
      audio: {
        echoCancellation: true,
        noiseSuppression: true,
        autoGainControl: true,
      },
    });
    localStreamRef.current = stream;

    const pc = new RTCPeerConnection();
    peerRef.current = pc;

    for (const track of stream.getTracks()) {
      pc.addTrack(track, stream);
    }

    pc.ontrack = (event) => {
      const [remoteStream] = event.streams;
      if (!remoteStream) {
        return;
      }
      if (remoteAudioRef.current) {
        remoteAudioRef.current.srcObject = remoteStream;
        void remoteAudioRef.current.play().catch(() => {});
      }
    };

    pc.onconnectionstatechange = () => {
      const state = pc.connectionState;
      if (state === "connected") {
        setIsListening(true);
        setSessionState("listening");
      } else if (state === "failed" || state === "disconnected" || state === "closed") {
        setIsListening(false);
      }
    };

    pc.onicecandidate = (event) => {
      if (!event.candidate) {
        return;
      }
      const candidate = event.candidate;
      void fetch(
        `/api/v1/chats/${encodeURIComponent(chatUUID)}/voice/sessions/${encodeURIComponent(nextSessionUUID)}/webrtc/ice`,
        {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            candidate: candidate.candidate,
            sdp_mid: candidate.sdpMid,
            sdp_mline_index: candidate.sdpMLineIndex,
          }),
        },
      ).catch(() => {});
    };

    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);

    const offerResponse = await fetch(
      `/api/v1/chats/${encodeURIComponent(chatUUID)}/voice/sessions/${encodeURIComponent(nextSessionUUID)}/webrtc/offer`,
      {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          type: pc.localDescription?.type || "offer",
          sdp: pc.localDescription?.sdp || "",
        }),
      },
    );
    if (!offerResponse.ok) {
      throw new Error((await offerResponse.text()) || "Failed to establish voice peer connection");
    }
    const answer = (await offerResponse.json()) as { type?: string; sdp?: string };
    await pc.setRemoteDescription({
      type: (answer.type as RTCSdpType) || "answer",
      sdp: answer.sdp || "",
    });
  };

  const startVoice = async () => {
    setIsBusy(true);
    setStatusMessage("");
    try {
      teardownVoiceRuntime();
      const response = await fetch(`/api/v1/chats/${encodeURIComponent(chatUUID)}/voice/sessions`, {
        method: "POST",
        credentials: "include",
      });
      if (!response.ok) {
        throw new Error((await response.text()) || "Failed to create voice session");
      }
      const payload = (await response.json()) as VoiceSessionResponse;
      const nextSessionUUID = payload.session_uuid || "";
      if (!nextSessionUUID) {
        throw new Error("Voice session UUID missing");
      }
      setSessionUUID(nextSessionUUID);
      sessionUUIDRef.current = nextSessionUUID;
      setSessionState(payload.state || "active");
      connectLevelEventsSocket(nextSessionUUID);
      await startPeerConnection(nextSessionUUID);
      setStatusMessage("Voice mode started. Audio is streamed to backend and input level is backend-driven.");
    } catch (error) {
      setStatusMessage(error instanceof Error ? error.message : "Unable to start voice mode");
      teardownVoiceRuntime();
    } finally {
      setIsBusy(false);
    }
  };

  const interruptVoice = async () => {
    if (!sessionUUID) return;
    setIsBusy(true);
    try {
      const response = await fetch(
        `/api/v1/chats/${encodeURIComponent(chatUUID)}/voice/sessions/${encodeURIComponent(sessionUUID)}/interrupt`,
        { method: "POST", credentials: "include" },
      );
      if (!response.ok) {
        throw new Error((await response.text()) || "Failed to interrupt voice session");
      }
      setSessionState("interrupted");
      setStatusMessage("Voice session interrupted.");
    } catch (error) {
      setStatusMessage(error instanceof Error ? error.message : "Unable to interrupt voice mode");
    } finally {
      setIsBusy(false);
    }
  };

  const endVoice = async () => {
    if (!sessionUUID) {
      teardownVoiceRuntime();
      return;
    }
    setIsBusy(true);
    try {
      const response = await fetch(
        `/api/v1/chats/${encodeURIComponent(chatUUID)}/voice/sessions/${encodeURIComponent(sessionUUID)}`,
        { method: "DELETE", credentials: "include" },
      );
      if (!response.ok) {
        throw new Error((await response.text()) || "Failed to close voice session");
      }
      setSessionState("closed");
      setStatusMessage("Voice mode stopped.");
      setSessionUUID("");
      sessionUUIDRef.current = "";
      teardownVoiceRuntime();
    } catch (error) {
      setStatusMessage(error instanceof Error ? error.message : "Unable to stop voice mode");
    } finally {
      setIsBusy(false);
    }
  };

  return (
    <div className="border-t border-border/70 bg-background/95 px-4 pb-4 pt-3 backdrop-blur-sm">
      <div className="mx-auto flex w-full max-w-3xl flex-col gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant="outline">Mode: voice</Badge>
          <Badge variant={isListening ? "secondary" : "outline"}>{isListening ? "Listening" : "Idle"}</Badge>
          {sessionUUID ? <Badge variant="outline">Session {sessionUUID.slice(0, 8)}</Badge> : null}
          <Badge variant="outline">State: {sessionState}</Badge>
        </div>
        {partialTranscript ? (
          <div className="rounded-lg border border-border/70 bg-muted/40 px-3 py-2 text-sm">
            <span className="font-medium">Annotating:</span> {partialTranscript}
          </div>
        ) : null}
        {committedTranscript ? (
          <div className="rounded-lg border border-border/70 bg-card px-3 py-2 text-sm">
            <span className="font-medium">Last committed:</span> {committedTranscript}
          </div>
        ) : null}
        <VoiceLevelIndicator level={level} />
        <div className="flex flex-wrap gap-2">
          {!isListening ? (
            <Button type="button" onClick={startVoice} disabled={isBusy}>
              Start voice
            </Button>
          ) : (
            <>
              <Button type="button" variant="outline" onClick={interruptVoice} disabled={isBusy}>
                Interrupt
              </Button>
              <Button type="button" variant="outline" onClick={endVoice} disabled={isBusy}>
                End voice
              </Button>
            </>
          )}
          <Button type="button" variant="ghost" onClick={() => navigate(`/chat/${encodeURIComponent(chatUUID)}`)}>
            Back to text chat
          </Button>
        </div>
        {statusMessage ? <p className="text-xs text-muted-foreground">{statusMessage}</p> : null}
        <audio ref={remoteAudioRef} autoPlay playsInline className="hidden" />
      </div>
    </div>
  );
}

export function VoiceChatPage({ chatUUID }: { chatUUID: string }) {
  const { data: chat } = useSWR<ChatSummary>(chatUUID ? `/api/v1/chats/${encodeURIComponent(chatUUID)}` : null, fetcher);
  const isAutomated = Boolean(chat?.partner?.is_automated || chat?.partner?.is_bot);

  return (
    <>
      <ChatBase chatUUID={chatUUID} navigateTo={(to: string) => navigate(to)}>
        {isAutomated ? (
          <MessagesView chatUUID={chatUUID} hideInput footerSlot={<VoiceComposer chatUUID={chatUUID} />} />
        ) : (
          <div className="mx-auto mt-10 w-full max-w-xl px-4">
            <Card>
              <CardHeader>
                <CardTitle>Voice mode unavailable</CardTitle>
                <CardDescription>
                  Voice mode currently supports automated chats only.
                </CardDescription>
              </CardHeader>
              <CardContent>
                <Button type="button" onClick={() => navigate(`/chat/${encodeURIComponent(chatUUID)}`)}>
                  Back to chat
                </Button>
              </CardContent>
            </Card>
          </div>
        )}
      </ChatBase>
      <WebsocketHandler />
    </>
  );
}
