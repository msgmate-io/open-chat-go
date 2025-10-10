import type { Preview } from "@storybook/react-vite";
import React from "react";
import MockDate from "mockdate";
import { initialize, mswLoader, type InitializeOptions } from "msw-storybook-addon";
import "../src/styles/globals.css";
import { mswHandlers } from "./msw-handlers";

/** Empty when served at root; `/storybook` when hosted under that base path. */
const STORYBOOK_BASE =
  (import.meta.env.STORYBOOK_BASE as string | undefined) || "";

function mswServiceWorkerUrl(): string {
  if (STORYBOOK_BASE) {
    return `${STORYBOOK_BASE}/mockServiceWorker.js`;
  }
  // Relative URL so MSW works on GitHub Pages project sites (e.g. /open-chat-go/).
  return "./mockServiceWorker.js";
}

function mswOptions(): InitializeOptions {
  return {
    onUnhandledRequest: "bypass",
    serviceWorker: {
      url: mswServiceWorkerUrl(),
      options: {
        scope: STORYBOOK_BASE ? `${STORYBOOK_BASE}/` : "./",
      },
    },
  };
}

initialize(mswOptions());

const preview: Preview = {
  parameters: {
    layout: "centered",
    msw: {
      handlers: mswHandlers.default,
    },
    options: {
      storySort: {
        order: [
          "Design System",
          ["Welcome", "Typography", "Color scheme", "*"],
          "Components",
          ["Text", "ThemeSelector", "*"],
          "Chat",
          "*",
        ],
      },
    },
  },
  loaders: [mswLoader],
  decorators: [
    (Story, context) => {
      const theme = (context.globals.theme as string) ?? "dark";
      document.documentElement.classList.toggle("dark", theme === "dark");
      document.documentElement.setAttribute("data-theme", theme);
      return (
        <div className="bg-background p-6 text-foreground">
          <Story />
        </div>
      );
    },
  ],
  globalTypes: {
    theme: {
      description: "Color theme",
      defaultValue: "dark",
      toolbar: {
        title: "Theme",
        items: [
          { value: "light", title: "Light" },
          { value: "dark", title: "Dark" },
        ],
      },
    },
  },
  async beforeEach() {
    document.documentElement.classList.add("dark");
    document.documentElement.setAttribute("data-theme", "dark");
    MockDate.set("2024-04-01T12:00:00Z");
  },
};

export default preview;
