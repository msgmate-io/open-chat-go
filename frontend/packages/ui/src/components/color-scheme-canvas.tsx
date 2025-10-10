import { cn } from "../lib/utils";
import type { CSSProperties } from "react";
import type { StorybookTheme } from "../tokens/color-scheme-args";

export function ColorSchemeCanvas({
  theme,
  overrideStyle,
  children,
}: {
  theme: StorybookTheme;
  overrideStyle?: CSSProperties;
  children: React.ReactNode;
}) {
  return (
    <div
      className={cn(
        "min-h-screen w-full bg-background text-foreground",
        theme === "dark" && "dark"
      )}
      style={overrideStyle}
    >
      {children}
    </div>
  );
}
