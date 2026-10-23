import { describe, expect, it } from "vitest"
import {
  MAX_PINNED_PAGES,
  PINNED_PAGES_STORAGE_PREFIX,
  createPinnedPagesStore,
  derivePinnedPageTitle,
  isPagePinned,
  normalizeIntegrationPath,
  normalizePinnedPage,
  parsePinnedPages,
  pinnedPagesForIntegration,
  pinnedPagesStorageKey,
  readableIntegrationTitle,
  resolveIntegrationLandingPath,
  serializePinnedPages,
  togglePinnedPage,
  type PinnedPagesStorageLike,
} from "./pinned-integrations"

function memoryStorage(initial: Record<string, string> = {}) {
  const data = new Map(Object.entries(initial))
  const storage: PinnedPagesStorageLike & { data: Map<string, string> } = {
    data,
    getItem: (key: string) => (data.has(key) ? (data.get(key) as string) : null),
    setItem: (key: string, value: string) => {
      data.set(key, value)
    },
    removeItem: (key: string) => {
      data.delete(key)
    },
  }
  return storage
}

describe("normalizeIntegrationPath", () => {
  it("canonicalizes slashes, query strings and hashes", () => {
    expect(normalizeIntegrationPath("/integrations/kubernetes/clusters/")).toBe(
      "/integrations/kubernetes/clusters",
    )
    expect(normalizeIntegrationPath("integrations/k8s//pods?ns=default#top")).toBe(
      "/integrations/k8s/pods",
    )
  })

  it("rejects the integrations root, the overview and unrelated routes", () => {
    expect(normalizeIntegrationPath("/integrations")).toBeNull()
    expect(normalizeIntegrationPath("/integrations/kubernetes")).toBeNull()
    expect(normalizeIntegrationPath("/chat")).toBeNull()
    expect(normalizeIntegrationPath("/integrationsx/foo")).toBeNull()
    expect(normalizeIntegrationPath("")).toBeNull()
    expect(normalizeIntegrationPath(undefined)).toBeNull()
    expect(normalizeIntegrationPath(42)).toBeNull()
  })
})

describe("titles", () => {
  it("humanizes integration and page segments", () => {
    expect(readableIntegrationTitle("kubernetes_dashboard")).toBe("Kubernetes Dashboard")
    expect(derivePinnedPageTitle({ integration: "k8s", rest: "cluster-overview" })).toBe(
      "K8s Cluster Overview",
    )
  })

  it("prefers an explicit title and keeps the integration title", () => {
    expect(
      normalizePinnedPage({
        path: "/integrations/k8s/pods",
        title: "Pods",
        integration_title: "Kubernetes",
      }),
    ).toEqual({
      integration: "k8s",
      path: "/integrations/k8s/pods",
      title: "Pods",
      integrationTitle: "Kubernetes",
    })
  })

  it("derives a title from the route when none was supplied", () => {
    expect(normalizePinnedPage("/integrations/k8s/pods")?.title).toBe("K8s Pods")
  })
})

describe("parsing and serializing", () => {
  it("round-trips through JSON and dedupes by path", () => {
    const pages = togglePinnedPage([], "/integrations/k8s/pods")
    const raw = serializePinnedPages(pages)
    expect(parsePinnedPages(raw)).toEqual(pages)
    expect(parsePinnedPages(serializePinnedPages([...pages, ...pages]))).toEqual(pages)
    expect(parsePinnedPages(serializePinnedPages([...pages, ...pages]))).toHaveLength(1)
  })

  it("tolerates garbage, empty values and legacy object envelopes", () => {
    expect(parsePinnedPages("{not json")).toEqual([])
    expect(parsePinnedPages("")).toEqual([])
    expect(parsePinnedPages(null)).toEqual([])
    expect(parsePinnedPages('{"pages":[{"path":"/integrations/k8s/pods"}]}')).toEqual([
      { integration: "k8s", path: "/integrations/k8s/pods", title: "K8s Pods" },
    ])
    expect(parsePinnedPages('["/integrations/k8s/pods", "/chat", null]')).toHaveLength(1)
  })

  it("caps the number of pinned pages", () => {
    const many = Array.from({ length: MAX_PINNED_PAGES + 5 }, (_, i) => `/integrations/k8s/page-${i}`)
    const pages = many.reduce<ReturnType<typeof togglePinnedPage>>(
      (acc, path) => togglePinnedPage(acc, path),
      [],
    )
    expect(pages).toHaveLength(MAX_PINNED_PAGES)
  })
})

describe("togglePinnedPage", () => {
  it("pins, refreshes labels in place and unpins", () => {
    let pages = togglePinnedPage([], { path: "/integrations/k8s/pods", title: "Pods" })
    expect(pages.map((page) => page.path)).toEqual(["/integrations/k8s/pods"])

    pages = togglePinnedPage(pages, { path: "/integrations/k8s/pods", title: "All Pods" })
    expect(pages).toHaveLength(1)
    expect(pages[0].title).toBe("All Pods")

    pages = togglePinnedPage(pages, { path: "/integrations/k8s/nodes", title: "Nodes" })
    expect(pages.map((page) => page.path)).toEqual([
      "/integrations/k8s/pods",
      "/integrations/k8s/nodes",
    ])

    pages = togglePinnedPage(pages, "/integrations/k8s/pods", false)
    expect(pages.map((page) => page.path)).toEqual(["/integrations/k8s/nodes"])
  })

  it("ignores invalid and unrelated paths", () => {
    const pages = togglePinnedPage([], "/chat")
    expect(pages).toEqual([])
    expect(togglePinnedPage(pages, "/chat", false)).toEqual([])
  })

  it("is re-pinnable after unpinning (no stale tombstone)", () => {
    let pages = togglePinnedPage([], "/integrations/k8s/pods")
    pages = togglePinnedPage(pages, "/integrations/k8s/pods", false)
    pages = togglePinnedPage(pages, "/integrations/k8s/pods")
    expect(pages).toHaveLength(1)
    expect(isPagePinned(pages, "/integrations/k8s/pods")).toBe(true)
    expect(isPagePinned(pages, "/integrations/k8s/pods/")).toBe(true)
    expect(isPagePinned(pages, "/integrations/k8s/nodes")).toBe(false)
  })
})

describe("resolveIntegrationLandingPath", () => {
  it("prefers the first pinned page of the integration", () => {
    const pages = [
      ...togglePinnedPage([], "/integrations/k8s/pods"),
      ...togglePinnedPage([], "/integrations/k8s/nodes"),
    ]
    expect(resolveIntegrationLandingPath("k8s", pages)).toBe("/integrations/k8s/pods")
    expect(pinnedPagesForIntegration(pages, "k8s").map((page) => page.path)).toEqual([
      "/integrations/k8s/pods",
      "/integrations/k8s/nodes",
    ])
  })

  it("falls back to an explicit path, else the overview", () => {
    const pages = togglePinnedPage([], "/integrations/k8s/pods")
    expect(resolveIntegrationLandingPath("other", pages, "/integrations/other/setup")).toBe(
      "/integrations/other/setup",
    )
    expect(resolveIntegrationLandingPath("other", pages, "/chat")).toBe("/integrations/other")
    expect(resolveIntegrationLandingPath("", pages)).toBe("/integrations")
  })
})

describe("createPinnedPagesStore", () => {
  it("is namespaced per user in localStorage", () => {
    expect(pinnedPagesStorageKey()).toBe(PINNED_PAGES_STORAGE_PREFIX)
    expect(pinnedPagesStorageKey(null)).toBe(PINNED_PAGES_STORAGE_PREFIX)
    expect(pinnedPagesStorageKey(" 7 ")).toBe(`${PINNED_PAGES_STORAGE_PREFIX}:7`)
  })

  it("round-trips pins and keeps users isolated", () => {
    const storage = memoryStorage()
    const store = createPinnedPagesStore(storage)
    store.save(1, togglePinnedPage([], "/integrations/k8s/pods"))
    store.save(2, togglePinnedPage([], "/integrations/k8s/nodes"))

    expect(store.load(1).map((page) => page.path)).toEqual(["/integrations/k8s/pods"])
    expect(store.load(2).map((page) => page.path)).toEqual(["/integrations/k8s/nodes"])
    expect(store.load(3)).toEqual([])

    store.clear(1)
    expect(store.load(1)).toEqual([])
    expect(store.load(2)).toHaveLength(1)
  })

  it("degrades to a no-op without storage rather than throwing", () => {
    const store = createPinnedPagesStore(null)
    expect(() => store.save(1, togglePinnedPage([], "/integrations/k8s/pods"))).not.toThrow()
    expect(store.load(1)).toEqual([])
    expect(() => store.clear(1)).not.toThrow()
  })

  it("survives storage that throws", () => {
    const hostile = {
      getItem: () => {
        throw new Error("SecurityError")
      },
      setItem: () => {
        throw new Error("QuotaExceededError")
      },
      removeItem: () => {
        throw new Error("SecurityError")
      },
    }
    const store = createPinnedPagesStore(hostile)
    expect(store.load(1)).toEqual([])
    expect(() => store.save(1, [])).not.toThrow()
    expect(() => store.clear(1)).not.toThrow()
  })
})
