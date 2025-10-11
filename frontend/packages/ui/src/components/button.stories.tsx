import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Button } from "./button";

const meta = {
  component: Button,
  title: "Components/Button",
} satisfies Meta<typeof Button>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Primary: Story = {
  args: { children: "Save changes" },
  play: async ({ canvas }) => {
    await expect(
      canvas.getByRole("button", { name: /save changes/i })
    ).toHaveAttribute("data-slot", "button");
  },
};

export const Outline: Story = {
  args: { children: "Cancel", variant: "outline" },
};

export const Destructive: Story = {
  args: { children: "Delete", variant: "destructive" },
};

export const Large: Story = {
  args: { children: "Continue", size: "lg" },
};

export const Brand: Story = {
  args: { children: "Go to chat", variant: "brand" },
};

export const Neutral: Story = {
  args: { children: "Sign up", variant: "neutral" },
};

/** Proves Tailwind theme tokens loaded (dark --primary → light gray). */
export const CssCheck: Story = {
  args: { children: "Submit" },
  play: async ({ canvas }) => {
    const button = canvas.getByRole("button", { name: /submit/i });
    await expect(getComputedStyle(button).backgroundColor).toBe(
      "rgb(229, 231, 235)"
    );
  },
};
