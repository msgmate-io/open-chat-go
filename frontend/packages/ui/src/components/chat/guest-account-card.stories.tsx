import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import type { ThemeName } from "../../lib/theme";
import { GuestAccountCard } from "./GuestAccountCard";
import { ThemeSelector } from "./theme-selector";

const meta = {
  title: "Chat/GuestAccountCard",
  component: GuestAccountCard,
  parameters: { layout: "padded" },
} satisfies Meta<typeof GuestAccountCard>;

export default meta;
type Story = StoryObj<typeof meta>;

function ThemeSlot() {
  const [theme, setTheme] = useState<ThemeName>("dark");
  return <ThemeSelector value={theme} onChange={setTheme} applyOnChange />;
}

export const Default: Story = {
  render: () => (
    <div className="w-72">
      <GuestAccountCard
        navigateTo={() => {}}
        themeSelector={<ThemeSlot />}
      />
    </div>
  ),
};
