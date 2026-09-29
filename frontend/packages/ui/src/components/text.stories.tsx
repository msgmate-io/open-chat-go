import type { Meta, StoryObj } from "@storybook/react-vite";
import { Text, TextTypes } from "./text";

const meta = {
  component: Text,
  title: "Components/Text",
  argTypes: {
    type: {
      control: "select",
      options: Object.values(TextTypes),
    },
    color: {
      control: "select",
      options: ["foreground", "muted", "primary", "destructive", "brand", "success", "inherit"],
    },
    tag: {
      control: "select",
      options: ["p", "h1", "h2", "h3", "h4", "h5", "h6", "span", "label", "li", "div"],
    },
  },
  args: {
    children: "The quick brown fox jumps over the lazy dog",
    type: TextTypes.Body5,
  },
} satisfies Meta<typeof Text>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {};

export const Heading: Story = {
  args: {
    type: TextTypes.Heading3,
    tag: "h2",
    children: "Welcome to Open Chat",
  },
};

export const Muted: Story = {
  args: {
    color: "muted",
    children: "Secondary hint text",
  },
};

export const Brand: Story = {
  args: {
    type: TextTypes.Body4,
    color: "brand",
    bold: true,
    children: "Go to chat",
  },
};
