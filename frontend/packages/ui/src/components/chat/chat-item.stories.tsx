import type { Meta, StoryObj } from "@storybook/react-vite";
import { ChatItemCompact } from "./ChatItem";
import { mockChat, mockBotChat } from "./story-data";

const meta = {
  title: "Chat/ChatItem",
  component: ChatItemCompact,
  parameters: { layout: "padded" },
} satisfies Meta<typeof ChatItemCompact>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Conversation: Story = {
  args: {
    chat: mockChat,
    isSelected: false,
    navigateTo: () => {},
  },
};

export const Selected: Story = {
  args: {
    chat: mockChat,
    isSelected: true,
    navigateTo: () => {},
  },
};

export const BotThread: Story = {
  args: {
    chat: mockBotChat,
    isSelected: false,
    navigateTo: () => {},
  },
};
