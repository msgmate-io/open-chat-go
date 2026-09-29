import type { Meta, StoryObj } from "@storybook/react-vite";
import { TypographyPage } from "./typography-page";
import { Text, TextTypes } from "./text";

const meta = {
  title: "Design System/Typography",
  component: TypographyPage,
  parameters: { layout: "fullscreen" },
} satisfies Meta<typeof TypographyPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Overview: Story = {};

export const Playground: Story = {
  render: () => (
    <div className="mx-auto max-w-2xl space-y-4 p-8">
      <Text type={TextTypes.Heading4}>Typography playground</Text>
      <Text type={TextTypes.Body5} color="muted">
        Adjust the theme toolbar to preview light and dark semantic text colors.
      </Text>
      <Text type={TextTypes.Body4} bold>
        Bold body text
      </Text>
      <Text type={TextTypes.Body5} center className="block">
        Centered body text
      </Text>
    </div>
  ),
  parameters: { layout: "centered" },
};
