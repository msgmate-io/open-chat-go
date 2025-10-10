import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/postcss";

const projectRoot = path.dirname(fileURLToPath(import.meta.url));

// Integration page sets and chat widgets live in external (often private)
// integration repositories. `openchat-integrations frontend` links them into
// this aggregator as gitignored symlinks, and the monorepo also gitignores
// `clients/integrations/*`. Tailwind's scanner honours that gitignore even for
// explicit `@source` directives, so a CSS glob over
// `clients/integrations/*/frontend` emits no candidates. Register every
// checked-out integration's real `frontend` directory explicitly instead: an
// explicit directory base is still scanned because it sits inside the
// integration checkout's own git root.
const integrationTailwindSources = () => {
  const integrationsDir = path.resolve(projectRoot, "../clients/integrations");
  const layoutsDir = path.join(projectRoot, "layouts");
  const sources = [];
  try {
    for (const entry of fs.readdirSync(integrationsDir, { withFileTypes: true })) {
      if (!entry.isDirectory()) continue;
      const frontendDir = path.join(integrationsDir, entry.name, "frontend");
      if (fs.existsSync(frontendDir)) {
        sources.push(path.relative(layoutsDir, frontendDir));
      }
    }
  } catch {
    // Integration checkouts are only present in a prepared monorepo build
    // context; a standalone frontend build simply has no extra sources.
  }

  return {
    postcssPlugin: "openchat-integration-tailwind-sources",
    Once(root, { result }) {
      const from = (result.opts.from ?? "").replaceAll("\\", "/");
      if (!from.endsWith("layouts/tailwind.css")) return;
      for (const source of sources) {
        root.append({ name: "source", params: `"${source}"` });
      }
    },
  };
};

export default {
  plugins: [integrationTailwindSources(), tailwindcss()],
};
