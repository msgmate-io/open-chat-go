import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Checkbox } from "./checkbox";

const meta = {
  component: Checkbox,
} satisfies Meta<typeof Checkbox>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Unchecked: Story = {
  args: { "aria-label": "Select chat" },
};

export const Checked: Story = {
  args: { "aria-label": "Select chat", defaultChecked: true },
  play: async ({ canvas }) => {
    const checkbox = canvas.getByRole("checkbox", { name: /select chat/i });
    await expect(checkbox).toHaveAttribute("data-state", "checked");
  },
};

export const Disabled: Story = {
  args: { "aria-label": "Select chat", disabled: true },
};
