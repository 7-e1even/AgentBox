import { describe, expect, it } from "vitest"

import { getProjectEmoji } from "./project-emoji"

describe("getProjectEmoji", () => {
  it("returns the configured project emoji", () => {
    expect(getProjectEmoji({ id: "demo", spec: { emoji: "🚀" } })).toBe("🚀")
  })

  it("keeps a compound emoji intact and ignores trailing input", () => {
    expect(
      getProjectEmoji({ id: "demo", spec: { emoji: "👨‍💻 开发" } })
    ).toBe("👨‍💻")
  })

  it("uses a folder emoji for projects without a custom icon", () => {
    expect(getProjectEmoji({ id: "legacy-project", spec: {} })).toBe("📁")
    expect(getProjectEmoji({ id: "demo", spec: { emoji: "" } })).toBe("📁")
  })
})
