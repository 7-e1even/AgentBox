import { describe, expect, it } from "vitest"

import { isSandboxDesktopEnabled } from "./sandbox-desktop"

describe("isSandboxDesktopEnabled", () => {
  it("uses the provisioned sandbox snapshot", () => {
    expect(isSandboxDesktopEnabled({ desktop: false })).toBe(false)
    expect(isSandboxDesktopEnabled({ desktop: true })).toBe(true)
  })

  it("treats a missing legacy snapshot as unavailable", () => {
    expect(isSandboxDesktopEnabled({})).toBe(false)
    expect(isSandboxDesktopEnabled(undefined)).toBe(false)
  })
})
