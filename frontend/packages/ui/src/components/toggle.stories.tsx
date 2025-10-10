import type { Meta, StoryObj } from "@storybook/react-vite";
import { Bold } from "lucide-react";
import { Toggle } from "./toggle";

const meta = {
  component: Toggle,
} satisfies Meta<typeof Toggle>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  render: () => (
    <Toggle aria-label="Toggle bold">
      <Bold className="size-4" />
    </Toggle>
  ),
};

export const Pressed: Story = {
  render: () => (
    <Toggle aria-label="Toggle bold" defaultPressed>
      <Bold className="size-4" />
    </Toggle>
  ),
};

export const WithText: Story = {
  args: { children: "Notifications", "aria-label": "Notifications" },
};
