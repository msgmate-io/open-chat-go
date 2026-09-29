import { Moon, Sun } from "lucide-react";

import { cn } from "../lib/utils";
import { applyTheme, isThemeName, THEMES, type ThemeName } from "../lib/theme";
import { Text, TextTypes } from "./text";
import { Button } from "./button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "./dropdown-menu";

export type ThemeSelectorVariant = "inline" | "icon-dropdown";

export interface ThemeSelectorProps {
  value: ThemeName;
  onChange: (theme: ThemeName) => void;
  variant?: ThemeSelectorVariant;
  className?: string;
  /** When true, calls applyTheme() before onChange. Useful for standalone Storybook demos. */
  applyOnChange?: boolean;
}

const themeIcons = {
  light: Sun,
  dark: Moon,
} as const;

function handleChange(
  onChange: (theme: ThemeName) => void,
  applyOnChange: boolean,
  theme: ThemeName
) {
  if (applyOnChange) {
    applyTheme(theme);
  }
  onChange(theme);
}

function ThemeSelectorInline({
  value,
  onChange,
  applyOnChange,
  className,
}: ThemeSelectorProps) {
  return (
    <div className={cn("space-y-2", className)}>
      <Text
        type={TextTypes.Body7}
        color="muted"
        className="px-0.5 font-medium uppercase tracking-wide"
      >
        Theme
      </Text>
      <div className="surface-sunken flex gap-1 p-1">
        {THEMES.map((theme) => {
          const Icon = themeIcons[theme];
          const isActive = value === theme;
          return (
            <button
              key={theme}
              type="button"
              aria-pressed={isActive}
              onClick={() => handleChange(onChange, applyOnChange, theme)}
              className={cn(
                "flex flex-1 items-center justify-center gap-1.5 rounded-md px-2 py-1.5 capitalize transition-[color,box-shadow,background-color]",
                isActive
                  ? "bg-background text-foreground shadow-sm ring-1 ring-black/[0.04] dark:ring-white/[0.06]"
                  : "text-muted-foreground hover:bg-background/60 hover:text-foreground"
              )}
            >
              <Icon className="size-3.5 shrink-0" aria-hidden />
              <Text type={TextTypes.Body6} tag="span" bold={isActive}>
                {theme}
              </Text>
            </button>
          );
        })}
      </div>
    </div>
  );
}

function ThemeSelectorIconDropdown({
  value,
  onChange,
  applyOnChange,
  className,
}: ThemeSelectorProps) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className={cn(
            "relative text-foreground hover:bg-accent hover:text-accent-foreground",
            className
          )}
        >
          <Sun className="size-[1.1rem] rotate-0 scale-100 transition-all dark:-rotate-90 dark:scale-0" />
          <Moon className="absolute size-[1.1rem] rotate-90 scale-0 transition-all dark:rotate-0 dark:scale-100" />
          <span className="sr-only">Toggle theme</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-44">
        <DropdownMenuLabel>Theme</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuRadioGroup
          value={value}
          onValueChange={(next) => {
            if (isThemeName(next)) {
              handleChange(onChange, applyOnChange, next);
            }
          }}
        >
          {THEMES.map((theme) => {
            const Icon = themeIcons[theme];
            return (
              <DropdownMenuRadioItem key={theme} value={theme} className="capitalize">
                <Icon className="size-4" />
                {theme}
              </DropdownMenuRadioItem>
            );
          })}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function ThemeSelector({
  variant = "inline",
  ...props
}: ThemeSelectorProps) {
  if (variant === "icon-dropdown") {
    return <ThemeSelectorIconDropdown {...props} />;
  }
  return <ThemeSelectorInline {...props} />;
}

export { ThemeSelector };
