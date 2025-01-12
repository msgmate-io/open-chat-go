import React, { useEffect } from 'react';
import { THEMES } from "@/components/ThemeToggle";
import { useThemeStore } from "@/components/ThemeToggle";

export function ThemeSelector() {
  const changeTheme = useThemeStore(state => state.changeTheme);
  const theme = useThemeStore(state => state.theme);

  const onSetTheme = (theme: string) => {
    changeTheme(theme);
  }

  return (
    <select
      onChange={(e) => {
        onSetTheme(e.target.value);
      }}
      value={theme}
      className="select select-sm w-full"
    >
      {Object.values(THEMES).map((_theme) => (
        <option key={_theme} value={_theme}>
          {_theme}
        </option>
      ))}
    </select>)
}
