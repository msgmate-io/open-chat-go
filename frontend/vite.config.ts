import react from "@vitejs/plugin-react";
import mdx from "@mdx-js/rollup";
import { defineConfig } from "vite";
import vike from "vike/plugin";
import path from "path";
import remarkGfm from "remark-gfm";

const cleanFrontmatterValue = (value: string) =>
  value.trim().replace(/^["']|["']$/g, "");

const parseMdxFrontmatter = (source: string) => {
  if (!source.startsWith("---")) {
    return { metadata: {}, content: source };
  }

  const closingMarkerIndex = source.indexOf("\n---", 3);
  if (closingMarkerIndex === -1) {
    return { metadata: {}, content: source };
  }

  const metadata = source
    .slice(3, closingMarkerIndex)
    .split("\n")
    .reduce<Record<string, string | number>>((frontmatter, line) => {
      const separatorIndex = line.indexOf(":");
      if (separatorIndex === -1) {
        return frontmatter;
      }

      const key = line.slice(0, separatorIndex).trim();
      const value = cleanFrontmatterValue(line.slice(separatorIndex + 1));

      frontmatter[key] = key === "order" && Number.isFinite(Number(value))
        ? Number(value)
        : value;

      return frontmatter;
    }, {});

  return {
    metadata,
    content: source.slice(closingMarkerIndex + "\n---".length).trimStart(),
  };
};

const mdxFrontmatter = () => ({
  name: "open-chat-go-mdx-frontmatter",
  enforce: "pre" as const,
  transform(source: string, id: string) {
    if (!id.endsWith(".mdx")) {
      return null;
    }

    const { metadata, content } = parseMdxFrontmatter(source);

    return {
      code: `export const metadata = ${JSON.stringify(metadata)};\n\n${content}`,
      map: null,
    };
  },
});

export default defineConfig({
  plugins: [
    mdxFrontmatter(),
    mdx({
      remarkPlugins: [remarkGfm],
    }),
    vike(),
    react({}),
  ],
  build: {
    target: "es2022",
  },
  resolve: {
    // Integration page sets are symlinked into pages/integrations by
    // `openchat-integrations frontend`. Without this, bare imports (e.g. "swr")
    // would resolve from the symlink target's real location, which is outside
    // this project's node_modules tree.

    alias: {
      "@": path.resolve(__dirname, "."),
      "@open-chat-go/ui": path.resolve(__dirname, "packages/ui/src/index.ts"),
    },
  },
  server: {
    allowedHosts: ["frontend", "localhost", "127.0.0.1", "0.0.0.0"],
    host: "0.0.0.0",
  },
});
