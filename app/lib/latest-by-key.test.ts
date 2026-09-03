import { describe, expect, it, vi } from "vitest"

import { createLatestByKeyQueue, enqueueLatestByKey } from "./latest-by-key"

function deferred() {
  let resolve: () => void = () => undefined
  const promise = new Promise<void>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

describe("latest-by-key queue", () => {
  it("coalesces edits made during a save and persists the latest value next", async () => {
    const queue = createLatestByKeyQueue<string>()
    const firstWrite = deferred()
    const writes: string[] = []
    const run = vi.fn(async (content: string) => {
      writes.push(content)
      if (content === "first") await firstWrite.promise
    })

    const saving = enqueueLatestByKey(queue, "/main.go", "first", run)
    expect(saving).not.toBeNull()
    expect(enqueueLatestByKey(queue, "/main.go", "second", run)).toBeNull()
    expect(enqueueLatestByKey(queue, "/main.go", "latest", run)).toBeNull()

    firstWrite.resolve()
    await saving

    expect(writes).toEqual(["first", "latest"])
    expect(queue.active.size).toBe(0)
    expect(queue.queued.size).toBe(0)
  })

  it("keeps saves for different paths independent", async () => {
    const queue = createLatestByKeyQueue<string>()
    const run = vi.fn(async () => undefined)

    await Promise.all([
      enqueueLatestByKey(queue, "/a", "A", run),
      enqueueLatestByKey(queue, "/b", "B", run),
    ])

    expect(run).toHaveBeenCalledTimes(2)
  })
})
