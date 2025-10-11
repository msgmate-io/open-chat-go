import type { Meta, StoryObj } from "@storybook/react-vite";
import { SendMessageButton } from "./SendMessageButton";

const meta = {
  title: "Chat/SendMessageButton",
  component: SendMessageButton,
} satisfies Meta<typeof SendMessageButton>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  args: { onClick: () => {}, isLoading: false },
};

export const Loading: Story = {
  args: { onClick: () => {}, isLoading: true },
};
