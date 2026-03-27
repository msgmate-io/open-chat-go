// Per-user "pinned integration pages" model (ci#610).
//
// Integration pages used to be reachable only by walking
// `/integrations` -> `<integration>` -> `<page>` (3+ clicks). This module holds
// the pure, storage-agnostic logic behind pinning an integration page so it can
// be rendered directly in the profile menu, plus the helpers that decide where an
// integration entry navigates to (a pinned landing page, else the overview).
//
// It deliberately has no React / fetch / DOM dependency outside of the
// `Storage`-shaped adapter below, so integration-owned Vike pages, the public app
// sidebar and unit tests all share exactly the same rules. Persistence is a
// single seam (`createPinnedPagesStore`) so a backend-backed store can be dropped
// in without touching the UI.

export type PinnedIntegrationPage = {
  /** Backend integration name, e.g. `kubernetes`. */
  integration: string;
  /** Absolute route of the pinned page, e.g. `/integrations/kubernetes/clusters`. */
  path: string;
  /** Human readable label rendered in the sidebar. */
  title: string;
  /** Integration-provided readable title, when it supplied one. */
  integrationTitle?: string;
};

/** Input accepted by the mutation helpers: a full entry, or just a path. */
export type PinnedPageInput = {
  path: string;
  title?: string;
  integrationTitle?: string;
};

export const INTEGRATIONS_ROOT = "/integrations";
export const PINNED_PAGES_STORAGE_PREFIX = "openchat:pinned-integration-pages";
/** Guards the sidebar against unbounded growth. */
export const MAX_PINNED_PAGES = 25;

/** Turns `kubernetes_dashboard` / `k8s-clusters` into `Kubernetes Dashboard`. */
export function readableIntegrationTitle(name: string): string {
  const raw = typeof name === "string" ? name.trim() : "";
  if (!raw) {
    return "Integration";
  }
  return raw
    .split(/[_\-./\s]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

/**
 * Canonicalizes an integration page path, dropping query/hash and trailing
 * slashes. Returns `null` for anything that is not a page below
 * `/integrations/<name>` (the overview itself and unrelated routes included).
 */
export function normalizeIntegrationPath(path: unknown): string | null {
  if (typeof path !== "string") {
    return null;
  }
  let value = path.trim();
  if (!value) {
    return null;
  }
  if (!value.startsWith("/")) {
    value = `/${value}`;
  }
  const queryIndex = value.indexOf("?");
  if (queryIndex >= 0) {
    value = value.slice(0, queryIndex);
  }
  const hashIndex = value.indexOf("#");
  if (hashIndex >= 0) {
    value = value.slice(0, hashIndex);
  }
  // Normalize duplicate and trailing slashes without a backtracking regex on
  // an uncontrolled path (CodeQL polynomial-regex finding).
  value = `/${value.split("/").filter(Boolean).join("/")}`;
  if (!value.startsWith(`${INTEGRATIONS_ROOT}/`)) {
    return null;
  }
  const remainder = value.slice(INTEGRATIONS_ROOT.length + 1);
  // The integration overview (`/integrations/<name>`) is not a pin-able page.
  if (!remainder || !remainder.includes("/") || remainder.includes("/../")) {
    return null;
  }
  return value;
}

/**
 * Drops the query string and the hash from an untrusted path, keeping only the
 * route (`"/integrations/k8s/pods?ns=default#top"` -> `"/integrations/k8s/pods"`).
 *
 * Cut at the earliest `"?"`/`"#"` in a single scan; no regex is involved so a
 * path made of many repeated separators cannot make this super-linear
 * (CodeQL js/polynomial-redos, ci#737).
 */
export function stripPathQueryAndHash(path: string): string {
  if (typeof path !== "string" || !path) {
    return "";
  }
  let end = path.length;
  const queryIndex = path.indexOf("?");
  if (queryIndex >= 0 && queryIndex < end) {
    end = queryIndex;
  }
  const hashIndex = path.indexOf("#");
  if (hashIndex >= 0 && hashIndex < end) {
    end = hashIndex;
  }
  return end === path.length ? path : path.slice(0, end);
}

/**
 * Linear-time trailing-slash trim: `"/a/b///"` -> `"/a/b"`, `"///"` -> `""`.
 *
 * Replaces `value.replace(/\/+$/, "")`, whose backtracking quantifier is
 * super-linear on long runs of `"/"` (CodeQL js/polynomial-redos, ci#737).
 */
export function trimTrailingSlashes(path: string): string {
  if (typeof path !== "string" || !path) {
    return "";
  }
  let end = path.length;
  while (end > 0 && path.charCodeAt(end - 1) === 47 /* "/" */) {
    end -= 1;
  }
  return end === path.length ? path : path.slice(0, end);
}

/**
 * Comparison key for a sidebar route / page path: query string and hash are
 * dropped and trailing slashes trimmed, so `"/integrations/k8s/pods/"` and
 * `"/integrations/k8s/pods?ns=default"` both collapse to
 * `"/integrations/k8s/pods"`. Accepts raw (possibly non-string) input and
 * returns `""` for anything unusable. Always linear in the input length.
 */
export function normalizePathname(path: unknown): string {
  if (typeof path !== "string" || !path) {
    return "";
  }
  return trimTrailingSlashes(stripPathQueryAndHash(path));
}

/** Splits `/integrations/kubernetes/clusters` into `{ integration, rest }`. */
export function parseIntegrationPath(
  path: string,
): { integration: string; rest: string; isOverview: boolean } | null {
  const normalized = normalizeIntegrationPath(path);
  if (!normalized) {
    return null;
  }
  const [integration, ...tail] = normalized.slice(INTEGRATIONS_ROOT.length + 1).split("/");
  if (!integration) {
    return null;
  }
  const rest = tail.join("/");
  return { integration, rest, isOverview: rest.length === 0 };
}

/** Derives a readable label from the route when the caller supplied none. */
export function derivePinnedPageTitle(parsed: {
  integration: string;
  rest: string;
}): string {
  const parts = [parsed.integration, ...parsed.rest.split("/")].filter(Boolean);
  return parts.map(readableIntegrationTitle).join(" ");
}

/** Normalizes untrusted input (storage, API, page props) into a pinned entry. */
export function normalizePinnedPage(input: unknown): PinnedIntegrationPage | null {
  if (input && typeof input === "object") {
    const record = input as Record<string, unknown>;
    const parsed = parseIntegrationPath(
      typeof record.path === "string" ? record.path : "",
    );
    if (!parsed) {
      return null;
    }
    const providedTitle =
      typeof record.title === "string" && record.title.trim()
        ? record.title.trim()
        : "";
    const integrationTitleRaw = record.integrationTitle ?? record.integration_title;
    const integrationTitle =
      typeof integrationTitleRaw === "string" && integrationTitleRaw.trim()
        ? integrationTitleRaw.trim()
        : undefined;
    const page: PinnedIntegrationPage = {
      integration: parsed.integration,
      path: normalizeIntegrationPath(record.path as string) as string,
      title: providedTitle || derivePinnedPageTitle(parsed),
    };
    if (integrationTitle) {
      page.integrationTitle = integrationTitle;
    }
    return page;
  }
  if (typeof input === "string") {
    return normalizePinnedPage({ path: input });
  }
  return null;
}

/** Parses the persisted JSON blob, tolerating rotation/corruption. */
export function parsePinnedPages(raw: string | null | undefined): PinnedIntegrationPage[] {
  if (typeof raw !== "string" || !raw.trim()) {
    return [];
  }
  let decoded: unknown;
  try {
    decoded = JSON.parse(raw);
  } catch {
    return [];
  }
  const rows = Array.isArray(decoded)
    ? decoded
    : decoded && typeof decoded === "object" && Array.isArray((decoded as { pages?: unknown }).pages)
      ? ((decoded as { pages: unknown[] }).pages as unknown[])
      : [];
  return dedupePinnedPages(rows.map((row) => normalizePinnedPage(row)));
}

export function serializePinnedPages(pages: PinnedIntegrationPage[]): string {
  return JSON.stringify(dedupePinnedPages(pages.map((page) => normalizePinnedPage(page))));
}

function dedupePinnedPages(
  pages: Array<PinnedIntegrationPage | null>,
): PinnedIntegrationPage[] {
  const seen = new Set<string>();
  const result: PinnedIntegrationPage[] = [];
  for (const page of pages) {
    if (!page || seen.has(page.path)) {
      continue;
    }
    seen.add(page.path);
    result.push(page);
    if (result.length >= MAX_PINNED_PAGES) {
      break;
    }
  }
  return result;
}

export function isPagePinned(
  pages: Array<PinnedIntegrationPage | PinnedPageInput | string>,
  path: string,
): boolean {
  const normalized = normalizeIntegrationPath(path);
  if (!normalized) {
    return false;
  }
  return pages.some((page) => {
    const candidate = normalizePinnedPage(page);
    return candidate?.path === normalized;
  });
}

/**
 * Pins or unpins a page, returning a new list.
 *
 * Pinning an already pinned page refreshes its label in place (so a better title
 * from a later render wins) instead of creating a duplicate; unpinning an
 * unknown page is a no-op.
 */
export function togglePinnedPage(
  pages: Array<PinnedIntegrationPage | PinnedPageInput | string>,
  input: PinnedIntegrationPage | PinnedPageInput | string,
  pinned = true,
): PinnedIntegrationPage[] {
  const current = parsePinnedPages(serializePinnedPages(pages.map((page) => normalizePinnedPage(page) as PinnedIntegrationPage)));
  const entry = normalizePinnedPage(input);
  if (!entry) {
    return current;
  }
  const index = current.findIndex((page) => page.path === entry.path);
  if (!pinned) {
    return index < 0 ? current : current.filter((_, i) => i !== index);
  }
  if (index < 0) {
    return [...current, entry].slice(-MAX_PINNED_PAGES);
  }
  const next = [...current];
  next[index] = entry;
  return next;
}

/** All pinned pages of one integration, in pin order. */
export function pinnedPagesForIntegration(
  pages: PinnedIntegrationPage[],
  integration: string,
): PinnedIntegrationPage[] {
  const name = typeof integration === "string" ? integration.trim() : "";
  if (!name) {
    return [];
  }
  return dedupePinnedPages(
    pages.map((page) => normalizePinnedPage(page)),
  ).filter((page) => page.integration === name);
}

/**
 * Where an integration entry should navigate to: the first page the user pinned
 * for it, else `fallbackPath`, else the integration overview.
 */
export function resolveIntegrationLandingPath(
  integration: string,
  pages: PinnedIntegrationPage[],
  fallbackPath?: string | null,
): string {
  const pinned = pinnedPagesForIntegration(pages, integration);
  if (pinned.length > 0) {
    return pinned[0].path;
  }
  const fallback = normalizeIntegrationPath(fallbackPath);
  if (fallback) {
    return fallback;
  }
  const name = typeof integration === "string" ? integration.trim() : "";
  return name ? `${INTEGRATIONS_ROOT}/${name}` : INTEGRATIONS_ROOT;
}

export type IntegrationNavRow = {
  name: string
  /** Integration-provided readable title (`frontend_routes[].title` / manifest). */
  title?: string
  labels?: string[]
  admin_only?: boolean
  user_accessible?: boolean
}

/** Readable sidebar label: integration-provided title, else a humanized name. */
export function integrationDisplayTitle(row: IntegrationNavRow | string): string {
  if (typeof row === "string") {
    return readableIntegrationTitle(row)
  }
  const provided = typeof row?.title === "string" ? row.title.trim() : ""
  return provided || readableIntegrationTitle(row?.name ?? "")
}

/**
 * Case-insensitive sidebar filter matching the integration name, its readable
 * title and any of its labels. An empty query matches everything.
 */
export function matchesIntegrationQuery(row: IntegrationNavRow, query?: string | null): boolean {
  const needle = typeof query === "string" ? query.trim().toLowerCase() : ""
  if (!needle) {
    return true
  }
  const haystack = [row?.name, row?.title, ...(row?.labels ?? [])]
    .filter((value): value is string => typeof value === "string")
    .map((value) => value.toLowerCase())
  return haystack.some((value) => value.includes(needle))
}

/** Filters + sorts integrations for the sidebar list. */
export function filterIntegrationRows(
  rows: IntegrationNavRow[],
  query?: string | null,
): IntegrationNavRow[] {
  return (Array.isArray(rows) ? rows : [])
    .filter((row) => Boolean(row?.name))
    .filter((row) => matchesIntegrationQuery(row, query))
    .sort((a, b) => a.name.localeCompare(b.name))
}

export type PinnedPagesStorageLike = Pick<Storage, "getItem" | "setItem" | "removeItem">;

/** Storage key holding the pins of one user (numeric ids are accepted too). */
export function pinnedPagesStorageKey(userKey?: string | number | null): string {
  const key =
    typeof userKey === "number" && Number.isFinite(userKey)
      ? String(userKey)
      : typeof userKey === "string"
        ? userKey.trim()
        : "";
  return key ? `${PINNED_PAGES_STORAGE_PREFIX}:${key}` : PINNED_PAGES_STORAGE_PREFIX;
}

/**
 * Local-first pin store. Pass `null` explicitly to run in no-op mode (SSR /
 * privacy modes); omit the argument to use `window.localStorage` when present.
 */
export function createPinnedPagesStore(storage?: PinnedPagesStorageLike | null) {
  const resolve = (): PinnedPagesStorageLike | null => {
    if (storage !== undefined) {
      return storage ?? null;
    }
    try {
      return typeof window !== "undefined" ? window.localStorage : null;
    } catch {
      return null;
    }
  };
  return {
    load(userKey?: string | number | null): PinnedIntegrationPage[] {
      const target = resolve();
      if (!target) {
        return [];
      }
      try {
        return parsePinnedPages(target.getItem(pinnedPagesStorageKey(userKey)));
      } catch {
        return [];
      }
    },
    save(
      userKey: string | number | null | undefined,
      pages: PinnedIntegrationPage[],
    ): void {
      const target = resolve();
      if (!target) {
        return;
      }
      try {
        target.setItem(pinnedPagesStorageKey(userKey), serializePinnedPages(pages));
      } catch {
        // Storage full / disabled: pins stay in memory for this session.
      }
    },
    clear(userKey?: string | number | null): void {
      const target = resolve();
      if (!target) {
        return;
      }
      try {
        target.removeItem(pinnedPagesStorageKey(userKey));
      } catch {
        // ignore
      }
    },
  };
}
