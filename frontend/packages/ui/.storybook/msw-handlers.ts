import { http, HttpResponse } from "msw";
import {
  mockChatsListResponse,
  mockContactsResponse,
  mockDefaultBotResponse,
} from "../src/components/chat/story-data";

/** Handlers for endpoints stories may call; extend when adding data-driven stories. */
export const mswHandlers = {
  default: [],
  profile: [
    http.get("/api/v1/user/self", () =>
      HttpResponse.json({
        id: "user-1",
        email: "demo@open-chat-go.test",
        name: "Demo User",
        is_admin: true,
      })
    ),
  ],
  chatList: [
    http.get("/api/v1/user/self", () =>
      HttpResponse.json({
        id: "user-1",
        email: "demo@open-chat-go.test",
        name: "Demo User",
      })
    ),
    http.get("/api/v1/chats/list", () => HttpResponse.json(mockChatsListResponse)),
    http.get("/api/v1/contacts/list", () => HttpResponse.json(mockContactsResponse)),
    http.get("/api/v1/contacts/default-bot", () => HttpResponse.json(mockDefaultBotResponse)),
  ],
};
