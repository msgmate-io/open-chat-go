import type { StorybookConfig } from "@storybook/react-vite";
import path from "node:path";
import { fileURLToPath } from "node:url";

const dirname = path.dirname(fileURLToPath(import.meta.url));

// When Storybook is hosted behind the Go backend (dev proxy) it lives under a
// base path so its Vite assets don't collide with the app frontend. Empty => root.
const rawBase = process.env.STORYBOOK_BASE_PATH ?? "";
const basePath = rawBase
  ? `/${rawBase.replace(/^\/+|\/+$/g, "")}/`
  : "";

const addons = ["@storybook/addon-docs"];
// The Vitest addon needs Playwright browsers; skip it where they aren't available
// (e.g. the lightweight Storybook container in docker-compose).
if (process.env.STORYBOOK_DISABLE_VITEST !== "1") {
  addons.push("@storybook/addon-vitest");
}

const config: StorybookConfig = {
  stories: [
    "../src/components/welcome.stories.tsx",
    "../src/**/*.stories.@(ts|tsx)",
  ],
  addons,
  staticDirs: ["../public"],
  framework: {
    name: "@storybook/react-vite",
    options: {},
  },
  viteFinal: async (config) => {
    config.resolve ??= {};
    config.resolve.alias = {
      ...config.resolve.alias,
      "@ui": path.resolve(dirname, "../src"),
    };
    if (basePath) {
      config.base = basePath;
    }
    config.define = {
      ...config.define,
      "import.meta.env.STORYBOOK_BASE": JSON.stringify(
        basePath ? basePath.replace(/\/$/, "") : ""
      ),
    };
    return config;
  },
};

export default config;
