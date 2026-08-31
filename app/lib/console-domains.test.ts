import { describe, expect, it } from "vitest"
import { domainsForEditor, domainsForSection } from "./console-domains"

describe("console domain isolation", () => {
  it("logs and run history do not load unrelated management domains", () => {
    expect(domainsForSection("logs", true)).toEqual([])
    expect(domainsForSection("automationRuns", true)).toEqual([])
    expect(domainsForSection("settings", true)).toEqual(["resources"])
  })
  it("credentials and servers belong to separate page dependencies", () => {
    expect(domainsForSection("access", true)).toEqual([
      "credentials",
      "catalog",
    ])
    expect(domainsForSection("servers", true)).not.toContain("credentials")
    expect(domainsForSection("access", true)).not.toContain("servers")
    expect(domainsForSection("users", false)).toEqual([])
  })
  it("requires every reference domain before runtime or sandbox editing", () => {
    expect(domainsForEditor("runtime")).toEqual([
      "resources",
      "servers",
      "credentials",
      "proxies",
    ])
    expect(domainsForEditor("sandbox")).toEqual(domainsForEditor("runtime"))
    expect(domainsForEditor("project")).toEqual([])
    expect(domainsForEditor(undefined)).toEqual([])
  })
})
