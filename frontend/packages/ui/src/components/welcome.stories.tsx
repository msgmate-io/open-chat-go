import type { Meta, StoryObj } from "@storybook/react-vite";
import { WelcomePage } from "./welcome-page";

const meta = {
  title: "Design System/Welcome",
  component: WelcomePage,
  parameters: { layout: "fullscreen" },
} satisfies Meta<typeof WelcomePage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Overview: Story = {};
