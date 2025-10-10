import type { OnPageTransitionEndAsync } from "vike/types";
import { applyDocumentTitle } from "@open-chat-go/ui";

export const onPageTransitionEnd: OnPageTransitionEndAsync = async () => {
  applyDocumentTitle(window.location.pathname);
  document.querySelector("body")?.classList.remove("page-is-transitioning");
};
