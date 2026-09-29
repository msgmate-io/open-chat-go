/**
 * Typography scale.
 * CSS classes live in `src/styles/typography.css`.
 */

export const TextTypes = {
  Heading1: "Heading1",
  Heading2: "Heading2",
  Heading3: "Heading3",
  Heading4: "Heading4",
  Heading5: "Heading5",
  Heading6: "Heading6",
  Heading7: "Heading7",
  Body1: "Body1",
  Body2: "Body2",
  Body3: "Body3",
  Body4: "Body4",
  Body5: "Body5",
  Body6: "Body6",
  Body7: "Body7",
} as const;

export type TextType = keyof typeof TextTypes;

export type TextStyleCategory = "heading" | "body";

export type TextTag =
  | "p"
  | "h1"
  | "h2"
  | "h3"
  | "h4"
  | "h5"
  | "h6"
  | "li"
  | "label"
  | "span"
  | "strong"
  | "div";

export type TextColor =
  | "foreground"
  | "muted"
  | "primary"
  | "destructive"
  | "brand"
  | "success"
  | "inherit";

export interface TextStyleDefinition {
  category: TextStyleCategory;
  className: string;
  fontFamily: "heading" | "body";
  fontSize: string;
  desktopFontSize?: string;
  lineHeight?: string;
  fontWeight: string;
  defaultTag: TextTag;
}

export const fontFamilies = {
  heading: {
    name: "Signika Negative",
    cssVar: "--font-heading",
    tailwind: "font-heading",
    usage: "Headings and display text",
  },
  body: {
    name: "DM Sans",
    cssVar: "--font-body",
    tailwind: "font-body",
    usage: "Body copy, labels, and UI text",
  },
} as const;

export const textStyleDefinitions: Record<TextType, TextStyleDefinition> = {
  Heading1: {
    category: "heading",
    className: "type-heading1",
    fontFamily: "heading",
    fontSize: "4rem",
    desktopFontSize: "5rem",
    lineHeight: "1.1",
    fontWeight: "600",
    defaultTag: "h1",
  },
  Heading2: {
    category: "heading",
    className: "type-heading2",
    fontFamily: "heading",
    fontSize: "3.5rem",
    desktopFontSize: "4rem",
    lineHeight: "1.1",
    fontWeight: "600",
    defaultTag: "h2",
  },
  Heading3: {
    category: "heading",
    className: "type-heading3",
    fontFamily: "heading",
    fontSize: "2.5rem",
    desktopFontSize: "3rem",
    lineHeight: "1.15",
    fontWeight: "600",
    defaultTag: "h3",
  },
  Heading4: {
    category: "heading",
    className: "type-heading4",
    fontFamily: "heading",
    fontSize: "2rem",
    desktopFontSize: "2rem",
    lineHeight: "1.2",
    fontWeight: "600",
    defaultTag: "h4",
  },
  Heading5: {
    category: "heading",
    className: "type-heading5",
    fontFamily: "heading",
    fontSize: "1.5rem",
    fontWeight: "600",
    defaultTag: "h5",
  },
  Heading6: {
    category: "heading",
    className: "type-heading6",
    fontFamily: "heading",
    fontSize: "1.25rem",
    fontWeight: "600",
    defaultTag: "h6",
  },
  Heading7: {
    category: "heading",
    className: "type-heading7",
    fontFamily: "heading",
    fontSize: "1rem",
    fontWeight: "600",
    defaultTag: "h6",
  },
  Body1: {
    category: "body",
    className: "type-body1",
    fontFamily: "body",
    fontSize: "2rem",
    desktopFontSize: "2.5rem",
    lineHeight: "1.2",
    fontWeight: "400",
    defaultTag: "p",
  },
  Body2: {
    category: "body",
    className: "type-body2",
    fontFamily: "body",
    fontSize: "1.75rem",
    desktopFontSize: "2rem",
    lineHeight: "1.25",
    fontWeight: "400",
    defaultTag: "p",
  },
  Body3: {
    category: "body",
    className: "type-body3",
    fontFamily: "body",
    fontSize: "1.5rem",
    fontWeight: "400",
    defaultTag: "p",
  },
  Body4: {
    category: "body",
    className: "type-body4",
    fontFamily: "body",
    fontSize: "1.25rem",
    fontWeight: "400",
    defaultTag: "p",
  },
  Body5: {
    category: "body",
    className: "type-body5",
    fontFamily: "body",
    fontSize: "1rem",
    fontWeight: "400",
    defaultTag: "p",
  },
  Body6: {
    category: "body",
    className: "type-body6",
    fontFamily: "body",
    fontSize: "0.8725rem",
    fontWeight: "400",
    defaultTag: "p",
  },
  Body7: {
    category: "body",
    className: "type-body7",
    fontFamily: "body",
    fontSize: "0.75rem",
    fontWeight: "400",
    defaultTag: "p",
  },
};

export const headingTextTypes = Object.entries(textStyleDefinitions)
  .filter(([, def]) => def.category === "heading")
  .map(([type]) => type as TextType);

export const bodyTextTypes = Object.entries(textStyleDefinitions)
  .filter(([, def]) => def.category === "body")
  .map(([type]) => type as TextType);

export function formatTextStyleDetails(def: TextStyleDefinition): string {
  const size = def.desktopFontSize
    ? `Mobile: ${def.fontSize}, Desktop: ${def.desktopFontSize}`
    : def.fontSize;
  const parts = [`Font size: ${size}`, `Weight: ${def.fontWeight}`];
  if (def.lineHeight) parts.push(`Line height: ${def.lineHeight}`);
  return parts.join(" · ");
}
