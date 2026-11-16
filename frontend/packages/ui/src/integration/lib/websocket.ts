// Shared websocket lifecycle signal. The chat socket reconnects on its own
// (react-use-websocket), but a reconnected socket cannot replay the frames that
// were emitted while it was down. The chat surfaces listen for this event to
// revalidate the message list/status and re-hydrate the streaming snapshot, so a
// tab that lost the socket does not keep rendering a stale partial reply.
export const WEBSOCKET_RECONNECTED_EVENT = "openchat:ws-reconnected";

export function announceWebsocketReconnected(): void {
  if (typeof window === "undefined") {
    return;
  }
  window.dispatchEvent(new CustomEvent(WEBSOCKET_RECONNECTED_EVENT));
}
