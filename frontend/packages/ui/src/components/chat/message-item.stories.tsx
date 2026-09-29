import type { Meta, StoryObj } from "@storybook/react-vite";
import { useRef, useState } from "react";
import { BotMessageShell, DefaultBotToolbar, PendingMessageItem, ShinyText, UserMessageShell } from "./message-item";
import { MessageInput } from "./MessageInput";
import { MessageInputWithFiles } from "./MessageInputWithFiles";

const meta = {
  title: "Chat/MessageItem",
  parameters: { layout: "padded" },
} satisfies Meta;

export default meta;
type Story = StoryObj<typeof meta>;

const sampleMessage = {
  text: "Msgmate.io can help with planning, writing, and debugging — always verify critical facts.",
  meta_data: {
    total_time: "2.4",
    token_usage: {
      prompt_tokens: 128,
      completion_tokens: 96,
      total_tokens: 224,
    },
  },
};

export const UserMessage: Story = {
  render: () => (
    <div className="mx-auto w-full max-w-3xl space-y-2">
      <UserMessageShell senderLabel="You" avatarLabel="You" avatarVariant="self">
        <p>Can you summarize our deployment checklist for staging?</p>
      </UserMessageShell>
    </div>
  ),
};

export const BotMessage: Story = {
  render: () => (
    <div className="mx-auto w-full max-w-3xl space-y-2">
      <BotMessageShell
        senderLabel="Msgmate"
        footer={<DefaultBotToolbar message={sampleMessage} onCopy={() => {}} />}
      >
        <p>
          Here is a concise staging checklist: run migrations, smoke-test auth, verify WebSocket
          connectivity, and confirm observability dashboards are receiving events.
        </p>
      </BotMessageShell>
    </div>
  ),
};

export const OutgoingBubble: Story = {
  render: () => (
    <div className="mx-auto w-full max-w-3xl">
      <UserMessageShell senderLabel="You" align="end">
        <p>Looks good — ship it when CI is green.</p>
      </UserMessageShell>
    </div>
  ),
};

export const Pending: Story = {
  render: () => (
    <div className="mx-auto w-full max-w-3xl">
      <PendingMessageItem text="Reasoning…" />
    </div>
  ),
};

export const ShinyLoading: Story = {
  render: () => (
    <div className="mx-auto w-full max-w-3xl rounded-2xl border border-border bg-muted/40 p-6">
      <ShinyText>Booting AI…</ShinyText>
    </div>
  ),
};

export const ConversationThread: Story = {
  render: () => (
    <div className="mx-auto flex w-full max-w-3xl flex-col gap-1 py-4">
      <UserMessageShell senderLabel="You" avatarVariant="self">
        <p>What changed in the latest design system update?</p>
      </UserMessageShell>
      <BotMessageShell
        senderLabel="Msgmate"
        footer={<DefaultBotToolbar message={sampleMessage} onCopy={() => {}} />}
      >
        <p>
          Message bubbles and the composer now share surface tokens, subtle shadows, and a refined
          toolbar for copy/regenerate actions.
        </p>
      </BotMessageShell>
      <UserMessageShell senderLabel="You" align="end">
        <p>Perfect — much cleaner.</p>
      </UserMessageShell>
      <PendingMessageItem text="Thinking…" />
    </div>
  ),
};

export const ComposerInThread: Story = {
  render: function Render() {
    const [text, setText] = useState("");
    const ref = useRef<HTMLTextAreaElement>(null);

    return (
      <div className="mx-auto flex w-full max-w-3xl flex-col gap-6">
        <UserMessageShell senderLabel="You" avatarVariant="self">
          <p>Show me the updated chat UI.</p>
        </UserMessageShell>
        <BotMessageShell senderLabel="Msgmate">
          <p>The composer and message rows below use the new chat styles.</p>
        </BotMessageShell>
        <MessageInput
          ref={ref}
          text={text}
          setText={setText}
          onSendMessage={() => setText("")}
          botConfig={{ reasoning: true, tools: ["search"] }}
        />
      </div>
    );
  },
};

export const ComposerWithFiles: Story = {
  render: function Render() {
    const [text, setText] = useState("Attach a log file for analysis");
    const ref = useRef<HTMLTextAreaElement>(null);

    return (
      <div className="mx-auto w-full max-w-3xl">
        <MessageInputWithFiles
          ref={ref}
          text={text}
          setText={setText}
          onSendMessage={() => setText("")}
          botConfig={{ backend: "openai", reasoning: true }}
        />
      </div>
    );
  },
};
