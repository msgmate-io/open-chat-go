import type { Meta, StoryObj } from "@storybook/react-vite";
import { OnlineIndicator } from "./OnlineIndicator";

const meta = {
  title: "Chat/OnlineIndicator",
  component: OnlineIndicator,
} satisfies Meta<typeof OnlineIndicator>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Online: Story = {
  args: { isOnline: true },
};

export const Offline: Story = {
  args: { isOnline: false },
};
