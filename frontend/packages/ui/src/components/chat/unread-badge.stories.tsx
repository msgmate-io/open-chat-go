import type { Meta, StoryObj } from "@storybook/react-vite";
import { UnreadBadge } from "./UnreadBadge";

const meta = {
  title: "Chat/UnreadBadge",
  component: UnreadBadge,
} satisfies Meta<typeof UnreadBadge>;

export default meta;
type Story = StoryObj<typeof meta>;

export const None: Story = {
  args: { unreadCount: 0 },
};

export const WithCount: Story = {
  args: { unreadCount: 3 },
};
