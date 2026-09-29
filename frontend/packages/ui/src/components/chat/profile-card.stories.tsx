import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import type { ThemeName } from "../../lib/theme";
import { mswHandlers } from "../../../.storybook/msw-handlers";
import { ProfileCard } from "./ProfileCard";
import { ThemeSelector } from "./theme-selector";

const meta = {
  title: "Chat/ProfileCard",
  component: ProfileCard,
  parameters: {
    layout: "padded",
    msw: { handlers: mswHandlers.profile },
  },
} satisfies Meta<typeof ProfileCard>;

export default meta;
type Story = StoryObj<typeof meta>;

function ThemeSlot() {
  const [theme, setTheme] = useState<ThemeName>("dark");
  return <ThemeSelector value={theme} onChange={setTheme} applyOnChange />;
}

export const LoggedIn: Story = {
  render: () => (
    <div className="w-72">
      <ProfileCard navigateTo={() => {}} themeSelector={<ThemeSlot />} />
    </div>
  ),
};
