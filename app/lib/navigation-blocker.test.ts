import { describe, expect, it } from "vitest"

import {
  historyEntryIndex,
  historyRestoreDelta,
  withHistoryEntryIndex,
} from "./navigation-blocker"

describe("navigation blocker history", () => {
  it("preserves Next.js state while stamping an entry index", () => {
    const state = withHistoryEntryIndex({ __NA: true, marker: "next" }, 7)

    expect(state).toMatchObject({ __NA: true, marker: "next" })
    expect(historyEntryIndex(state)).toBe(7)
  })

  it("restores backward and forward traversals in the opposite direction", () => {
    expect(historyRestoreDelta(5, 3)).toBe(2)
    expect(historyRestoreDelta(3, 5)).toBe(-2)
  })

  it("rejects missing or invalid entry indexes", () => {
    expect(historyEntryIndex(null)).toBeNull()
    expect(historyEntryIndex({ __agentboxHistoryIndex: -1 })).toBeNull()
    expect(historyEntryIndex({ __agentboxHistoryIndex: 1.5 })).toBeNull()
  })
})
