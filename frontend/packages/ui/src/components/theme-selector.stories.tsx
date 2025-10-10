import type { Meta, StoryObj } from "@storybook/react-vite";
import { useEffect, useState } from "react";
import type { ThemeName } from "../lib/theme";
import { ThemeSelector } from "./theme-selector";

const meta = {
  component: ThemeSelector,
  title: "Components/ThemeSelector",
  parameters: { layout: "centered" },
} satisfies Meta<typeof ThemeSelector>;

export default meta;
type Story = StoryObj<typeof meta>;

function ThemeSelectorDemo({
  variant,
}: {
  variant: "inline" | "icon-dropdown";
}) {
  const [theme, setTheme] = useState<ThemeName>("dark");

  useEffect(() => {
    document.documentElement.classList.toggle("dark", theme === "dark");
    document.documentElement.setAttribute("data-theme", theme);
  }, [theme]);

  return (
    <div className="w-56 space-y-3">
      <ThemeSelector
        variant={variant}
        value={theme}
        onChange={setTheme}
        applyOnChange
      />
    </div>
  );
}

export const Inline: Story = {
  render: () => <ThemeSelectorDemo variant="inline" />,
};

export const IconDropdown: Story = {
  render: () => <ThemeSelectorDemo variant="icon-dropdown" />,
};

export const InlineInMenu: Story = {
  render: () => (
    <div className="surface-panel w-56 p-2">
      <ThemeSelectorDemo variant="inline" />
    </div>
  ),
  parameters: { layout: "centered" },
};
