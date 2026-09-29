import type { ComponentType } from "react";

export type DocMetadata = {
  title: string;
  path: string;
  description?: string;
  order?: number;
  adminOnly?: boolean;
  section?: string;
  parent?: string;
  sourceUrl?: string;
};

export type DocSection = DocMetadata & {
  component: ComponentType<{ components?: Record<string, ComponentType<any>> }>;
};

type DocModule = {
  default: DocSection["component"];
  metadata?: Partial<DocMetadata>;
};

const parseBoolean = (value: unknown, fallback = false) => {
  if (typeof value === "boolean") return value;
  if (typeof value === "string") {
    const normalized = value.trim().toLowerCase();
    if (normalized === "true") return true;
    if (normalized === "false") return false;
  }
  return fallback;
};

const parseOrder = (value: unknown) => {
  if (typeof value === "number") return value;
  if (typeof value === "string") {
    const parsed = Number.parseInt(value, 10);
    if (!Number.isNaN(parsed)) return parsed;
  }
  return undefined;
};

const docModules = import.meta.glob<DocModule>("./content/*.mdx", {
  eager: true,
});

const getFallbackPath = (filePath: string) =>
  filePath
    .replace(/^\.\/content\//, "")
    .replace(/\.mdx$/, "");

const getFallbackTitle = (filePath: string) =>
  getFallbackPath(filePath)
    .split(/[_-]/)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");

const docsSourceBase = "https://github.com/msgmate-io/open-chat-go-frontend/blob/main/pages/docs/content";

const getSourceUrl = (filePath: string) => {
  const fileName = filePath.replace(/^\.\/content\//, "");
  return `${docsSourceBase}/${fileName}`;
};

export const docs = Object.entries(docModules)
  .map(([filePath, module]) => {
    const metadata = module.metadata ?? {};

    return {
      title: metadata.title?.trim() || getFallbackTitle(filePath),
      path: metadata.path?.trim() || getFallbackPath(filePath),
      description: metadata.description?.trim(),
      order: parseOrder(metadata.order),
      adminOnly: parseBoolean(metadata.adminOnly, false),
      section: metadata.section?.trim() || "General",
      parent: metadata.parent?.trim(),
      sourceUrl: metadata.sourceUrl?.trim() || getSourceUrl(filePath),
      component: module.default,
    };
  })
  .sort((left, right) => {
    const leftOrder = left.order ?? Number.MAX_SAFE_INTEGER;
    const rightOrder = right.order ?? Number.MAX_SAFE_INTEGER;

    if (leftOrder !== rightOrder) {
      return leftOrder - rightOrder;
    }

    return left.title.localeCompare(right.title);
  });
