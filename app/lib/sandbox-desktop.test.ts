import { describe, expect, it } from "vitest"

import { isSandboxDesktopEnabled } from "./sandbox-desktop"

describe("isSandboxDesktopEnabled", () => {
  it("uses an explicit sandbox override", () => {
    expect(isSandboxDesktopEnabled({ desktop: false }, { desktop: true })).toBe(
      false
    )
    expect(isSandboxDesktopEnabled({ desktop: true }, { desktop: false })).toBe(
      true
    )
  })

  it("falls back to the runtime template for older sandboxes", () => {
    expect(isSandboxDesktopEnabled({}, { desktop: true })).toBe(true)
    expect(isSandboxDesktopEnabled({}, {})).toBe(false)
  })
})
