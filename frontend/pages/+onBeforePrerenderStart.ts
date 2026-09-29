import type { OnBeforePrerenderStartSync } from "vike/types";

let alreadyReturned = false;

export const onBeforePrerenderStart: OnBeforePrerenderStartSync = () => {
  if (alreadyReturned) {
    return [];
  }
  alreadyReturned = true;

  return [
    "/chat/{chat_uuid}",
    "/chat/new/{contact_token}/init",
    "/chat/new/{contact_token}",
    "/interaction/{chat_share_uuid}",
    "/interaction/{chat_share_uuid}/newest_response",
  ];
};
