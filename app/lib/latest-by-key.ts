export type LatestByKeyQueue<T> = {
  active: Set<string>
  queued: Map<string, T>
}

export function createLatestByKeyQueue<T>(): LatestByKeyQueue<T> {
  return { active: new Set(), queued: new Map() }
}

export function enqueueLatestByKey<T>(
  queue: LatestByKeyQueue<T>,
  key: string,
  value: T,
  run: (value: T) => Promise<void>
): Promise<void> | null {
  queue.queued.set(key, value)
  if (queue.active.has(key)) return null

  queue.active.add(key)
  return drain()

  async function drain() {
    let finalError: unknown
    let finalRunFailed = false
    try {
      while (queue.queued.has(key)) {
        const next = queue.queued.get(key) as T
        queue.queued.delete(key)
        try {
          await run(next)
          finalRunFailed = false
          finalError = undefined
        } catch (error) {
          finalRunFailed = true
          finalError = error
        }
      }
      if (finalRunFailed) throw finalError
    } finally {
      queue.active.delete(key)
    }
  }
}
