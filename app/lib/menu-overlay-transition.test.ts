import { describe, expect, it, vi } from "vitest"

import { createMenuOverlayTransition } from "./menu-overlay-transition"

describe("createMenuOverlayTransition", () => {
  it("opens only after the menu reports that focus restoration completed", () => {
    const scheduled: Array<() => void> = []
    const transition = createMenuOverlayTransition((onOpen) =>
      scheduled.push(onOpen)
    )
    const onOpen = vi.fn()

    transition.queue(onOpen)

    expect(onOpen).not.toHaveBeenCalled()
    expect(scheduled).toHaveLength(0)

    transition.completeClose()

    expect(onOpen).not.toHaveBeenCalled()
    expect(scheduled).toHaveLength(1)
    scheduled[0]()
    expect(onOpen).toHaveBeenCalledOnce()
  })

  it("consumes a queued open exactly once", () => {
    const scheduled: Array<() => void> = []
    const transition = createMenuOverlayTransition((onOpen) =>
      scheduled.push(onOpen)
    )
    const onOpen = vi.fn()

    transition.queue(onOpen)
    transition.completeClose()
    transition.completeClose()

    expect(scheduled).toHaveLength(1)
    scheduled[0]()
    expect(onOpen).toHaveBeenCalledOnce()
  })
})
