import { describe, expect, it } from "vitest"
import { getPageMetadata } from "./page-metadata"

// ci#1143: the model management pages are being extracted out of the private
// `admin` integration into a dedicated `model_selector` integration. Both the
// legacy admin route and the new integration route must keep resolving to
// usable page metadata so the extraction cannot regress the document title or
// the sidebar label, and the new routes must stay under /integrations/<name>.

describe("getPageMetadata model management routes", () => {
  it("labels the extracted model_selector routes", () => {
    for (const path of [
      "/integrations/model_selector",
      "/integrations/model_selector/",
      "/integrations/model_selector/models",
      "/integrations/model_selector/models/",
    ]) {
      expect(getPageMetadata(path)).toEqual({
        pageName: "Models: Management",
        sidebarTitle: "Models",
      })
    }
  })

  it("keeps the legacy admin models route working during the migration window", () => {
    expect(getPageMetadata("/integrations/admin/models")).toEqual({
      pageName: "Admin: Models",
      sidebarTitle: "Admin",
    })
    expect(getPageMetadata("/integrations/admin/models/")).toEqual({
      pageName: "Admin: Models",
      sidebarTitle: "Admin",
    })
  })

  it("does not widen the admin metadata catch-all", () => {
    expect(getPageMetadata("/integrations/model_selector/unknown")).toEqual({
      pageName: "Integration",
      sidebarTitle: "Integrations",
    })
    expect(getPageMetadata("/integrations/admin/queues")).toEqual({
      pageName: "Admin: Queues",
      sidebarTitle: "Admin",
    })
  })
})
