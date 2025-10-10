import type { Meta, StoryObj } from "@storybook/react-vite";
import { Badge } from "./badge";

const meta = {
  component: Badge,
} satisfies Meta<typeof Badge>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  args: { children: "Online" },
};

export const Secondary: Story = {
  args: { children: "2FA enabled", variant: "secondary" },
};

export const Outline: Story = {
  args: { children: "Draft", variant: "outline" },
};

export const Destructive: Story = {
  args: { children: "Error", variant: "destructive" },
};
