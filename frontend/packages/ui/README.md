# @open-chat-go/ui

Shared design system for Open Chat Go: primitives, chat UI, and theme tokens.

## Color scheme

All semantic colors are defined in one place:

- **`src/styles/semantic.css`** — CSS variables for `:root` (light) and `.dark`
- **`src/tokens/colors.ts`** — token names, descriptions, and Tailwind class mapping (for docs/tooling)

Edit `semantic.css` when changing the palette. `globals.css` imports it and registers tokens for Tailwind (`bg-primary`, `text-muted-foreground`, `bg-brand`, etc.).

View the palette in Storybook: **Design System → Color scheme**.

Chat UI lives under **Chat/** in the Storybook sidebar (e.g. `Chat/ChatsList`, `Chat/MessageInput`). Composed layouts are under **Chat/Examples** (`ChatPage`, `ConversationPanelOnly`, `SidebarOnly`).

The **Design System → Color scheme → Playground** story exposes every semantic token in the Controls panel (grouped by Surfaces, Actions, Feedback, etc.) plus radius.

### Deployed static Storybook

Production builds copy Storybook into `frontend/storybook/` and register it via generated manifests:

- `routes.json` — `/storybook/` (HTML shell, exact match via `/storybook/{$}`)
- `redirects.json` — `/storybook` → `/storybook/`
- `public-paths.json` — unauthenticated access under `/storybook`

Other Storybook files (`iframe.html`, JS, MSW worker) are served by the generic static-file fallback. Open **`/storybook/`** after `full_build.sh`.

### GitHub Pages

On every push to `main` (including merges), `.github/workflows/deploy-storybook-github-pages.yaml` builds and publishes Storybook to GitHub Pages.

- Enable **Settings → Pages → Build and deployment → Source: GitHub Actions** once per repository.
- Published URL: `https://<org>.github.io/open-chat-go/` (project site for this repo).

## Layout

```
src/
  styles/
    semantic.css       # ← edit colors here
    globals.css        # Tailwind + docs utilities
  tokens/colors.ts     # Token registry for Storybook
  components/          # Primitives (flat — no nested ui/ folder)
    button.tsx         # Variants: default, outline, ghost, brand, neutral, …
    card.tsx, badge.tsx, input.tsx, textarea.tsx, dropdown-menu.tsx, …
    chat/              # Chat-specific composed components
      ChatsList.tsx
      MessageInput.tsx
      ProfileCard.tsx
      …
  lib/
    utils.ts           # cn()
    theme.ts           # THEMES, applyTheme()
    date.ts            # isToday, isYesterday, …
  hooks/
  styles/globals.css
```

## Button variants

| Variant | Use |
|---------|-----|
| `default` | Primary actions (uses `--primary`) |
| `brand` | Marketing CTAs (uses `--brand`, e.g. “Go to chat”) |
| `neutral` | High-contrast pill (`foreground` on `background`) |
| `secondary`, `outline`, `ghost`, `destructive` | Standard shadcn semantics |

## Usage

```tsx
import {
  Button,
  Card,
  ChatsList,
  MessageInput,
  ThemeSelector,
  applyTheme,
} from "@open-chat-go/ui";
```

Theme wiring in the app uses `ConnectedThemeSelector` (`frontend/components/ConnectedThemeSelector.tsx`) with `useThemeStore` from `ThemeToggle`.

Account menus accept `themeSelector` and optional `avatarSrc` props so the package stays free of app assets.

## Commands

From `frontend/`:

```bash
npm run storybook
npm run test-storybook
```
