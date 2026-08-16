import { describe, expect, it } from "vitest"

import { APP_SECTION_PATHS, appSectionPath } from "./app-section"

describe("app section routes", () => {
  it("gives every console section a unique absolute path", () => {
    const paths = Object.values(APP_SECTION_PATHS)

    expect(new Set(paths).size).toBe(paths.length)
    expect(paths.every((path) => path.startsWith("/"))).toBe(true)
  })

  it("uses stable user-facing paths for primary navigation", () => {
    expect(appSectionPath("overview")).toBe("/overview")
    expect(appSectionPath("projects")).toBe("/projects")
    expect(appSectionPath("runtimes")).toBe("/environment-templates")
    expect(appSectionPath("automations")).toBe("/automations")
    expect(appSectionPath("mcp")).toBe("/mcp-servers")
    expect(appSectionPath("access")).toBe("/model-services")
    expect(appSectionPath("proxies")).toBe("/network-proxies")
  })

  it("does not expose the removed Agent configuration route", () => {
    const paths = Object.values(APP_SECTION_PATHS)

    expect(paths).not.toContain("/agents")
  })
})
