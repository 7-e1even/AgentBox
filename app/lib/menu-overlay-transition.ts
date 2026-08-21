type ScheduleOverlayOpen = (onOpen: () => void) => void

export type MenuOverlayTransition = {
  queue: (onOpen: () => void) => void
  completeClose: () => void
}

export function createMenuOverlayTransition(
  schedule: ScheduleOverlayOpen = (onOpen) => globalThis.setTimeout(onOpen, 0)
): MenuOverlayTransition {
  let pendingOpen: (() => void) | null = null

  return {
    queue(onOpen) {
      pendingOpen = onOpen
    },
    completeClose() {
      const onOpen = pendingOpen
      pendingOpen = null
      // Radix is still returning focus while onCloseAutoFocus runs.
      // Open the next modal in a new task so the two focus scopes never overlap.
      if (onOpen) schedule(onOpen)
    },
  }
}
