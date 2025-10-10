import { listChatUIMessageRenderers } from "./chat-ui";
import type { ChatUIMessageExtrasProps } from "./chat-ui/types";

/**
 * Generic dispatch point for privately-owned message content: integrations
 * register message renderers that read tool calls and meta_data on their own,
 * keeping per-integration knowledge (payloads, endpoints, protocols) out of
 * this public package.
 */
export function ChatUIMessageExtras(props: ChatUIMessageExtrasProps) {
  const renderers = listChatUIMessageRenderers();
  if (renderers.length === 0) {
    return null;
  }
  return (
    <>
      {renderers.map((Renderer, index) => (
        <Renderer key={index} {...props} />
      ))}
    </>
  );
}
