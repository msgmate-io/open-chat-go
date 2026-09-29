import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Input } from "./input";

const meta = {
  component: Input,
} satisfies Meta<typeof Input>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  args: { placeholder: "Search messages…", type: "search" },
  play: async ({ canvas }) => {
    const input = canvas.getByPlaceholderText(/search messages/i);
    await expect(input).toHaveAttribute("data-slot", "input");
  },
};

export const Email: Story = {
  args: { placeholder: "you@example.com", type: "email" },
};

export const Disabled: Story = {
  args: { placeholder: "Read only", disabled: true },
};
