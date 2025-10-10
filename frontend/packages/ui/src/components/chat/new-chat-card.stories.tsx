import type { Meta, StoryObj } from "@storybook/react-vite";
import { NewChatCard } from "./NewChatCard";

const meta = {
  title: "Chat/NewChatCard",
  component: NewChatCard,
  parameters: { layout: "padded" },
} satisfies Meta<typeof NewChatCard>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Expanded: Story = {
  args: {
    leftPannelCollapsed: false,
    onToggleCollapse: () => {},
    navigateTo: () => {},
  },
};

export const Collapsed: Story = {
  args: {
    leftPannelCollapsed: true,
    onToggleCollapse: () => {},
    navigateTo: () => {},
  },
};
