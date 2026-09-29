import * as React from "react"
import type { LucideIcon, LucideProps } from "lucide-react"
import {
  Bot,
  Check,
  ChevronDown,
  Circle,
  Clipboard,
  Eraser,
  Mail,
  Menu,
  Pencil,
  Trash2,
  Undo2,
  User,
} from "lucide-react"

import { cn } from "../lib/utils"

export const iconRegistry = {
  bot: Bot,
  check: Check,
  "chevron-down": ChevronDown,
  circle: Circle,
  clipboard: Clipboard,
  eraser: Eraser,
  mail: Mail,
  menu: Menu,
  pencil: Pencil,
  trash: Trash2,
  undo: Undo2,
  user: User,
} as const

export type IconName = keyof typeof iconRegistry

const iconSizes = {
  xs: 12,
  sm: 16,
  md: 20,
  lg: 24,
  xl: 32,
} as const

export type IconSize = keyof typeof iconSizes

export type IconProps = Omit<LucideProps, "size"> & {
  /** Semantic icon from the design-system registry. */
  name?: IconName
  /** Direct Lucide icon component (for one-offs outside the registry). */
  icon?: LucideIcon
  size?: IconSize | number
  /** Accessible label; omit when the icon is decorative. */
  label?: string
}

function Icon({
  name,
  icon,
  size = "md",
  label,
  className,
  ...props
}: IconProps) {
  const LucideComponent = icon ?? (name ? iconRegistry[name] : undefined)

  if (!LucideComponent) {
    return null
  }

  const pixelSize = typeof size === "number" ? size : iconSizes[size]

  return (
    <LucideComponent
      aria-hidden={label ? undefined : true}
      aria-label={label}
      role={label ? "img" : undefined}
      size={pixelSize}
      strokeWidth={2}
      className={cn("shrink-0", className)}
      {...props}
    />
  )
}

export { Icon }
