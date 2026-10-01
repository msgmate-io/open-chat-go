import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { useState } from "react";
import type { ThemeName } from "../../lib/theme";
import { mswHandlers } from "../../../.storybook/msw-handlers";
import { ChatsList } from "./ChatsList";
import {
  mockChatsListResponse,
  mockContactsResponse,
  mockDefaultBotResponse,
} from "./story-data";
import { ThemeSelector } from "./theme-selector";

const chatListHandlers = [
  ...mswHandlers.profile,
  http.get("/api/v1/chats/list", () => HttpResponse.json(mockChatsListResponse)),
  http.get("/api/v1/contacts/list", () => HttpResponse.json(mockContactsResponse)),
  http.get("/api/v1/contacts/default-bot", () => HttpResponse.json(mockDefaultBotResponse)),
];

const meta = {
  title: "Chat/ChatsList",
  component: ChatsList,
  parameters: {
    layout: "fullscreen",
    msw: { handlers: chatListHandlers },
  },
} satisfies Meta<typeof ChatsList>;

export default meta;
type Story = StoryObj<typeof meta>;

function ThemeSlot() {
  const [theme, setTheme] = useState<ThemeName>("dark");
  return <ThemeSelector value={theme} onChange={setTheme} />;
}

export const WithConversations: Story = {
  render: () => (
    <div className="h-[640px] w-[320px] border border-border bg-background">
      <ChatsList
        chatUUID={null}
        leftPannelCollapsed={false}
        onToggleCollapse={() => {}}
        navigateTo={() => {}}
        themeSelector={<ThemeSlot />}
      />
    </div>
  ),
};

export const IntegrationMode: Story = {  render: () => (
    <div className="h-[640px] w-[320px] border border-border bg-background">
      <ChatsList
        chatUUID={null}
        leftPannelCollapsed={false}
        onToggleCollapse={() => {}}
        navigateTo={() => {}}
        themeSelector={<ThemeSlot />}
        title="Integrations"
        showDefaultChats={false}
        showChats={false}
        topSection={
          <div className="flex flex-col gap-1.5">
            <div className="chat-list-row px-3 py-2">Chats</div>
            <div className="chat-list-divider">Integrations</div>
            <div className="chat-list-row px-3 py-2">git</div>
            <div className="chat-list-row px-3 py-2">ssh</div>
          </div>
        }
      />
    </div>
  ),
};

export const SmallSquareViewport: Story = {
  render: () => (
    <div className="h-[600px] w-[358px] border border-border bg-background">
      <ChatsList
        chatUUID={null}
        leftPannelCollapsed={false}
        onToggleCollapse={() => {}}
        navigateTo={() => {}}
        themeSelector={<ThemeSlot />}
      />
    </div>
  ),
};

export const DefaultBotUnavailable: Story = {
  parameters: {
    msw: {
      handlers: [
        ...mswHandlers.profile,
        http.get("/api/v1/chats/list", () => HttpResponse.json(mockChatsListResponse)),
        http.get("/api/v1/contacts/list", () => HttpResponse.json({ rows: [] })),
        http.get("/api/v1/contacts/default-bot", () =>
          HttpResponse.json({ error: "Default bot not found" }, { status: 404 }),
        ),
      ],
    },
  },
  render: () => (
    <div className="h-[640px] w-[320px] border border-border bg-background">
      <ChatsList
        chatUUID={null}
        leftPannelCollapsed={false}
        onToggleCollapse={() => {}}
        navigateTo={() => {}}
        themeSelector={<ThemeSlot />}
      />
    </div>
  ),
};
