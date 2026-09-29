import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";

import { cn } from "../lib/utils";
import {
  TextTypes,
  textStyleDefinitions,
  type TextColor,
  type TextTag,
  type TextType,
} from "../tokens/typography";

const textColorVariants = cva("", {
  variants: {
    color: {
      foreground: "text-foreground",
      muted: "text-muted-foreground",
      primary: "text-primary",
      destructive: "text-destructive",
      brand: "text-brand",
      success: "text-success",
      inherit: "text-inherit",
    },
  },
  defaultVariants: {
    color: "foreground",
  },
});

export interface TextProps
  extends Omit<React.HTMLAttributes<HTMLElement>, "color">,
    VariantProps<typeof textColorVariants> {
  type?: TextType;
  tag?: TextTag;
  bold?: boolean;
  center?: boolean;
}

const Text = React.forwardRef<HTMLElement, TextProps>(function Text(
  {
    type = TextTypes.Body5,
    tag,
    bold = false,
    center = false,
    color,
    className,
    children,
    ...props
  },
  ref
) {
  const styleDef = textStyleDefinitions[type];
  const Component = (tag ?? styleDef.defaultTag) as React.ElementType;

  return (
    <Component
      ref={ref}
      data-slot="text"
      data-type={type}
      className={cn(
        styleDef.className,
        textColorVariants({ color: color as TextColor | undefined }),
        bold && "font-bold",
        center && "text-center",
        className
      )}
      {...props}
    >
      {children}
    </Component>
  );
});
Text.displayName = "Text";

export { Text, TextTypes, textColorVariants };
