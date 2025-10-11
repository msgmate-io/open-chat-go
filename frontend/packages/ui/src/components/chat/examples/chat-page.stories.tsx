import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { useRef, useState } from "react";
import type { ThemeName } from "../../../lib/theme";
import { mswHandlers } from "../../../../.storybook/msw-handlers";
import { BotDisplay } from "../BotSelector";
import { ChatsList } from "../ChatsList";
import { MessageInputWithFiles } from "../MessageInputWithFiles";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "../Resizable";
import { mockChat } from "../story-data";
import { ThemeSelector } from "../theme-selector";

const pageHandlers = [
  ...mswHandlers.chatList,
  http.post("/api/v1/files/upload", async () =>
    HttpResponse.json({
      file_id: "story-file-1",
      file_name: "notes.pdf",
    })
  ),
];

function ThemeSlot() {
  const [theme, setTheme] = useState<ThemeName>("dark");
  return <ThemeSelector value={theme} onChange={setTheme} />;
}

function PlaceholderMessages() {
  return (
    <div className="flex flex-1 flex-col gap-4 overflow-y-auto px-4 py-6">
      <div className="max-w-[85%] self-start rounded-2xl bg-card px-4 py-3 text-card-foreground shadow-sm">
        <p className="text-sm">
          Hey — can you review the sidebar components in Storybook?
        </p>
        <p className="mt-1 text-xs text-muted-foreground">Alex · 10:42</p>
      </div>
      <div className="max-w-[85%] self-end rounded-2xl bg-primary px-4 py-3 text-primary-foreground shadow-sm">
        <p className="text-sm">
          On it. The composed example should show list + thread + input together.
        </p>
        <p className="mt-1 text-xs text-primary-foreground/70">You · 10:43</p>
      </div>
      <div className="max-w-[85%] self-start rounded-2xl bg-card px-4 py-3 text-card-foreground shadow-sm">
        <p className="text-sm">Looks good — ship it.</p>
        <p className="mt-1 text-xs text-muted-foreground">Alex · 10:44</p>
      </div>
    </div>
  );
}

function ConversationPanel() {
  const [text, setText] = useState("");
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const title =
    mockChat.settings?.title ||
    `${mockChat.partner.first_name} ${mockChat.partner.second_name}`;

  return (
    <div className="flex h-full w-full flex-col bg-secondary">
      <header className="flex shrink-0 items-center gap-3 border-b border-border bg-background/80 px-4 py-3 backdrop-blur-sm">
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-lg font-semibold text-foreground">
            {title}
          </h1>
          <p className="text-xs text-muted-foreground">Placeholder thread</p>
        </div>
        <BotDisplay selectedModel="gpt-4o" />
      </header>
      <PlaceholderMessages />
      <div className="shrink-0 border-t border-border bg-secondary px-2 pb-2 pt-1">
        <MessageInputWithFiles
          ref={inputRef}
          text={text}
          setText={setText}
          onSendMessage={() => setText("")}
          botConfig={{ reasoning: true, tools: ["search"], backend: "openai" }}
        />
      </div>
    </div>
  );
}

function ChatPageLayout({ activeChatId }: { activeChatId: string | null }) {
  return (
    <div className="h-[720px] w-full min-w-[960px] border border-border bg-background">
      <ResizablePanelGroup direction="horizontal" id="chat-page-demo">
        <ResizablePanel defaultSize={28} minSize={20} maxSize={40} order={1}>
          <ChatsList
            chatUUID={activeChatId}
            leftPannelCollapsed={false}
            onToggleCollapse={() => {}}
            navigateTo={() => {}}
            themeSelector={<ThemeSlot />}
          />
        </ResizablePanel>
        <ResizableHandle withHandle />
        <ResizablePanel defaultSize={72} minSize={50} order={2}>
          <ConversationPanel />
        </ResizablePanel>
      </ResizablePanelGroup>
    </div>
  );
}

const meta = {
  title: "Chat/Examples",
  parameters: {
    layout: "fullscreen",
    msw: { handlers: pageHandlers },
  },
} satisfies Meta;

export default meta;
type Story = StoryObj<typeof meta>;

/** Full chat shell: resizable sidebar, placeholder messages, composer with file upload. */
export const ChatPage: Story = {
  render: () => <ChatPageLayout activeChatId={mockChat.uuid} />,
};

/** Right-hand conversation column only (header, bubbles, input). */
export const ConversationPanelOnly: Story = {
  parameters: { layout: "fullscreen" },
  render: () => (
    <div className="mx-auto h-[640px] w-full max-w-[960px] border border-border">
      <ConversationPanel />
    </div>
  ),
};

/** Sidebar list + profile footer without the message pane. */
export const SidebarOnly: Story = {
  render: () => (
    <div className="h-[640px] w-[320px] border border-border bg-background">
      <ChatsList
        chatUUID={mockChat.uuid}
        leftPannelCollapsed={false}
        onToggleCollapse={() => {}}
        navigateTo={() => {}}
        themeSelector={<ThemeSlot />}
      />
    </div>
  ),
};
