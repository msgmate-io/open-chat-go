// Ensure the generated integration chat-extension aggregator exists.
//
// The integration manager (`openchat-integrations frontend`) rewrites
// `integrations/extensions.gen.ts` for the selected profile. On a checkout
// where the manager has not run yet (fresh clone, host-only `npm run dev`),
// the app's static import in `layouts/LayoutDefault.tsx` would fail; this
// placeholder keeps dev and build working. The file is gitignored.
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const target = resolve(here, "..", "integrations", "extensions.gen.ts");

mkdirSync(dirname(target), { recursive: true });
if (!existsSync(target)) {
  writeFileSync(target, "");
}
