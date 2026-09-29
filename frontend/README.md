# Open-Chat frontend

The React/Vike frontend for [Open-Chat](https://github.com/msgmate-io/open-chat-go).
This directory is the **in-tree core frontend** of the open-chat-go monorepo:
it owns the shared component contract ([`@open-chat-go/ui`](./packages/ui)), the
generic app pages and the build that prerenders and serves them.

Per-integration React code does **not** live here. Each integration repository
owns its own pages under `frontend/pages/` and only gets linked into this
aggregator (via symlinks) for the selected integration profile, so private
integration code never lands in the public monorepo.

## Integration page linking

The `openchat-integrations` manager (from
[`open-chat-go-build-tools`](https://github.com/msgmate-io/open-chat-go-build-tools))
drives the whole flow:

```bash
# from the open-chat-go repository root
.venv/bin/openchat-integrations setup   --profile full   # repos/symlinks + private manifest
.venv/bin/openchat-integrations frontend --profile full  # link integration pages
npm --prefix frontend run build                          # prerender (Vike)
.venv/bin/openchat-integrations export  --profile full --dist-dir frontend/dist/client
```

An integration declares its contribution in `integration.frontend.json` at its
repository root:

```json
{
  "name": "my_integration",
  "mounts": [
    { "source": "pages", "dest": "integrations/my_integration" },
    { "source": "pages/sign-up", "dest": "sign-up" }
  ],
  "pages": [
    { "source": "integrations/my_integration/projects", "asset": "projects/index.html" },
    { "source": "sign-up", "asset": "sign-up/index.html" }
  ],
  "extension": "my-extension.tsx"
}
```

- **`mounts`** link directories from the integration's `frontend/` into
  `frontend/pages/<dest>`; the destination determines the Vike route, so pages
  can live under the default `/integrations/<name>` prefix or at arbitrary
  **root routes** (e.g. `/sign-up`). Without `mounts`, the integration's
  `frontend/pages` mounts under `integrations/<name>`.
- **`pages`** list what `export` copies from the prerendered `dist/client/`
  into the integration's embedded `frontend_assets`.
- **`extension`** (optional) registers cross-integration chat UI at app start.

The manager also:

- creates a `frontend/node_modules` anchor symlink inside each linked
  integration checkout so Vite resolves dependencies from the aggregator, and
  prunes stale integration links before re-linking when the profile changes.

### Hot reload / local development

- `npm run dev` / `npm run build` set `VIKE_CRAWL='{"git":false}'` so Vike
  discovers the (gitignored) page-set symlinks; do not remove that env prefix.
- Re-run `openchat-integrations frontend --profile <profile>` after adding or
  renaming a page set, then restart the Vite dev server.
- Inside the development compose this runs automatically in the
  `integration-sync` service.

See the integration development guide in the docs
(`open-chat-go/frontend/pages/docs/content/integration-development.mdx`) for the
full walkthrough.

## Development

```bash
npm install
npm run dev     # Vite/Vike dev server
npm run build   # production prerender
```

Documentation pages live in [`pages/docs/content`](./pages/docs/content) and are
published at [`msgmate.io/docs`](https://msgmate.io/docs).

## License

Except for the brand assets below, the frontend is licensed
**AGPL-3.0-or-later** — see the repository-root
[`LICENSE`](../LICENSE).

**Excluded from the open-source license:** the brand assets under
[`assets/`](./assets) and
[`packages/ui/src/integration/assets/`](./packages/ui/src/integration/assets)
(including `logo.png` and `msgmate_logo.png`) are proprietary and
confidential, all rights reserved — see the repository-root
[`NOTICE`](../NOTICE). They may not be used, copied, modified or redistributed
without prior written permission.

Integration code lives in the integration repositories and carries its own
license; private integrations are proprietary and are never part of this
public monorepo.
