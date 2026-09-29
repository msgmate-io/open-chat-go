import type { Meta, StoryObj } from "@storybook/react-vite";
import { Typewriter } from "./typewriter";
import { openChatLandingTypewriterTexts, typewriterDemoTexts } from "../tokens/typewriter-texts";

const meta = {
  component: Typewriter,
  title: "Components/Typewriter",
  parameters: { layout: "centered" },
  argTypes: {
    typingSpeed: { control: { type: "range", min: 5, max: 200, step: 5 } },
    delay: { control: { type: "range", min: 500, max: 5000, step: 250 } },
    slideDuration: { control: { type: "range", min: 250, max: 3000, step: 250 } },
  },
  args: {
    texts: typewriterDemoTexts,
    typingSpeed: 40,
    delay: 2000,
    slideDuration: 1000,
  },
} satisfies Meta<typeof Typewriter>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Demo: Story = {};

export const LandingHero: Story = {
  args: {
    texts: openChatLandingTypewriterTexts,
    typingSpeed: 10,
    fullHeight: false,
  },
  decorators: [
    (Story) => (
      <div className="surface-panel flex h-[480px] w-full max-w-3xl items-center justify-center p-8">
        <Story />
      </div>
    ),
  ],
  parameters: { layout: "fullscreen" },
};

export const FastCycle: Story = {
  args: {
    texts: typewriterDemoTexts,
    typingSpeed: 20,
    delay: 800,
    slideDuration: 400,
  },
};
